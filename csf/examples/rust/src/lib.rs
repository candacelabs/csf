//! Optional differential interpreter for numeric profile v1.
//! Types and ProtoJSON descriptors are generated from the owning protobuf.

use contract::{
    Action, Instruction, Observation, Opcode, Program, RequestKind, RuntimeRequest, RuntimeResponse,
};
use prost::Name;
use prost_reflect::{DescriptorPool, DynamicMessage};
use std::sync::OnceLock;

mod json_keys;

// Protobuf prose is rendered mechanically; lint handwritten documentation only.
#[allow(clippy::doc_lazy_continuation)]
pub mod contract {
    include!(concat!(env!("OUT_DIR"), "/candace.brainspine.v1.rs"));
}

pub const VALUE_LIMIT: i64 = 1_000_000_000;
pub const SCALE_LIMIT: i64 = 1_000_000;
pub const INPUT_LIMIT: i64 = 10_000;
pub const OUTPUT_LIMIT: i64 = 1_000;
pub const MAX_INSTRUCTIONS: usize = 128;
pub const MAX_DEPTH: u8 = 16;

fn pool() -> &'static DescriptorPool {
    static POOL: OnceLock<DescriptorPool> = OnceLock::new();
    POOL.get_or_init(|| {
        DescriptorPool::decode(include_bytes!(concat!(env!("OUT_DIR"), "/descriptor.bin")).as_ref())
            .expect("build-generated protobuf descriptor must decode")
    })
}

/// Decode using the canonical ProtoJSON names, enum and integer rules.
/// Unknown fields and unknown enum names are rejected by prost-reflect.
pub fn decode_json<T: Name + Default>(json: &str) -> Result<T, String> {
    let descriptor = pool()
        .get_message_by_name(&T::full_name())
        .ok_or_else(|| format!("unknown protobuf message: {}", T::full_name()))?;
    json_keys::check(descriptor.clone(), json)?;
    let mut parser = serde_json::Deserializer::from_str(json);
    let message =
        DynamicMessage::deserialize(descriptor, &mut parser).map_err(|error| error.to_string())?;
    parser.end().map_err(|error| error.to_string())?;
    message.transcode_to().map_err(|error| error.to_string())
}

pub fn encode_response(response: &RuntimeResponse) -> Result<String, String> {
    let descriptor = pool()
        .get_message_by_name(&RuntimeResponse::full_name())
        .expect("RuntimeResponse belongs to the canonical contract");
    let mut message = DynamicMessage::new(descriptor);
    message
        .transcode_from(response)
        .map_err(|error| error.to_string())?;
    serde_json::to_string(&message).map_err(|error| error.to_string())
}

/// Interpret a candidate independently of activation, freshness and fallback.
/// Admission policy remains owned by the Go runtime.
pub fn evaluate(program: &Program, observation: &Observation) -> Result<Action, String> {
    if program.schema_version != 1 {
        return Err("schema_version must equal 1".into());
    }
    if program.steering.len() > MAX_INSTRUCTIONS || program.acceleration.len() > MAX_INSTRUCTIONS {
        return Err("output program exceeds 128 instructions".into());
    }
    if observation.features.len() != 4 {
        return Err("observation requires exactly four features".into());
    }
    if observation
        .features
        .iter()
        .any(|value| !(-INPUT_LIMIT..=INPUT_LIMIT).contains(value))
    {
        return Err("observation feature exceeds numeric profile bounds".into());
    }
    let steering = evaluate_output(&program.steering, &observation.features)
        .map_err(|error| format!("steering: {error}"))?;
    let acceleration = evaluate_output(&program.acceleration, &observation.features)
        .map_err(|error| format!("acceleration: {error}"))?;
    Ok(Action {
        steering: steering.clamp(-OUTPUT_LIMIT, OUTPUT_LIMIT),
        acceleration: acceleration.clamp(-OUTPUT_LIMIT, OUTPUT_LIMIT),
        ..Default::default()
    })
}

fn opcode(instruction: &Instruction) -> Result<Opcode, String> {
    let opcode = Opcode::try_from(instruction.opcode).map_err(|_| "unknown opcode".to_string())?;
    let uses_value = matches!(opcode, Opcode::Constant | Opcode::Scale);
    let uses_index = opcode == Opcode::Input;
    let uses_bounds = opcode == Opcode::Clamp;
    if (!uses_value && instruction.value != 0)
        || (!uses_index && instruction.input_index != 0)
        || (!uses_bounds && (instruction.lower != 0 || instruction.upper != 0))
    {
        return Err("nonzero operand is unused by opcode".into());
    }
    match opcode {
        Opcode::Unspecified => return Err("unspecified opcode".into()),
        Opcode::Constant if !(-VALUE_LIMIT..=VALUE_LIMIT).contains(&instruction.value) => {
            return Err("constant exceeds numeric profile bounds".into());
        }
        Opcode::Input if instruction.input_index >= 4 => {
            return Err("input_index must be less than four".into());
        }
        Opcode::Scale if !(-SCALE_LIMIT..=SCALE_LIMIT).contains(&instruction.value) => {
            return Err("scale coefficient exceeds numeric profile bounds".into());
        }
        Opcode::Clamp
            if instruction.lower < -VALUE_LIMIT
                || instruction.upper > VALUE_LIMIT
                || instruction.lower > instruction.upper =>
        {
            return Err("invalid clamp bounds".into());
        }
        _ => {}
    }
    Ok(opcode)
}

fn saturate(value: i64) -> i64 {
    value.clamp(-VALUE_LIMIT, VALUE_LIMIT)
}

fn evaluate_output(instructions: &[Instruction], features: &[i64]) -> Result<i64, String> {
    // Values and depths travel together so postfix programs cannot bypass the
    // source expression depth restriction. Bounded arity bounds stack usage.
    let mut stack: Vec<(i64, u8)> = Vec::with_capacity(instructions.len());
    for (index, instruction) in instructions.iter().enumerate() {
        let operation =
            opcode(instruction).map_err(|error| format!("instruction {index}: {error}"))?;
        let entry = match operation {
            Opcode::Constant => (instruction.value, 1),
            Opcode::Input => (features[instruction.input_index as usize], 1),
            Opcode::Add => {
                let right = stack.pop().ok_or("ADD stack underflow")?;
                let left = stack.pop().ok_or("ADD stack underflow")?;
                (saturate(left.0 + right.0), left.1.max(right.1) + 1)
            }
            Opcode::Scale => {
                let value = stack.pop().ok_or("SCALE stack underflow")?;
                // Bounds above keep the product within 10^15, below i64::MAX.
                // Rust signed integer division truncates toward zero.
                (saturate(value.0 * instruction.value / 1_000), value.1 + 1)
            }
            Opcode::Clamp => {
                let value = stack.pop().ok_or("CLAMP stack underflow")?;
                (
                    value.0.clamp(instruction.lower, instruction.upper),
                    value.1 + 1,
                )
            }
            Opcode::Unspecified => unreachable!("opcode validation rejects unspecified"),
        };
        if entry.1 > MAX_DEPTH {
            return Err("expression exceeds depth 16".into());
        }
        stack.push(entry);
    }
    match stack.as_slice() {
        [(value, _)] => Ok(*value),
        [] => Err("output program is empty".into()),
        _ => Err("output program leaves unconsumed stack entries".into()),
    }
}

/// Only the canonical EVALUATE request is supported. This is a pure arithmetic
/// conformance backend; it does not own controller activation or fallback.
pub fn handle_line(line: &str) -> RuntimeResponse {
    let result = (|| {
        let request = decode_json::<RuntimeRequest>(line)?;
        if request.kind != RequestKind::Evaluate as i32 {
            return Err("only REQUEST_KIND_EVALUATE is supported".into());
        }
        let program = request.program.as_ref().ok_or("program is required")?;
        let observation = request
            .observation
            .as_ref()
            .ok_or("observation is required")?;
        evaluate(program, observation)
    })();
    match result {
        Ok(action) => RuntimeResponse {
            action: Some(action),
            ..Default::default()
        },
        Err(error) => RuntimeResponse {
            error,
            ..Default::default()
        },
    }
}
