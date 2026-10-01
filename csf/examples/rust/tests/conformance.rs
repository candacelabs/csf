use brain_spine_conformance::{
    contract::{Instruction, Observation, Opcode, Program, RuntimeResponse},
    decode_json, encode_response, evaluate, handle_line, MAX_INSTRUCTIONS, VALUE_LIMIT,
};

fn instruction(opcode: Opcode, value: i64) -> Instruction {
    Instruction {
        opcode: opcode as i32,
        value,
        ..Default::default()
    }
}

fn program(steering: Vec<Instruction>) -> Program {
    Program {
        schema_version: 1,
        steering,
        acceleration: vec![instruction(Opcode::Constant, 0)],
        ..Default::default()
    }
}

fn observation() -> Observation {
    Observation {
        features: vec![333, -333, 10_000, -10_000],
        epoch: 7,
        sequence: 8,
        tick: 9,
    }
}

fn output(instructions: Vec<Instruction>) -> i64 {
    evaluate(&program(instructions), &observation())
        .unwrap()
        .steering
}

#[test]
fn evaluates_every_operation_and_truncates_negative_products_toward_zero() {
    assert_eq!(
        output(vec![
            instruction(Opcode::Input, 0),
            instruction(Opcode::Scale, -500),
        ]),
        -166
    );
    assert_eq!(
        output(vec![
            instruction(Opcode::Constant, -17),
            instruction(Opcode::Scale, 100),
        ]),
        -1
    );
    assert_eq!(
        output(vec![
            instruction(Opcode::Constant, 17),
            instruction(Opcode::Scale, -100),
        ]),
        -1
    );
    assert_eq!(
        output(vec![
            instruction(Opcode::Constant, -17),
            instruction(Opcode::Scale, -100),
        ]),
        1
    );
    let clamp = Instruction {
        opcode: Opcode::Clamp as i32,
        lower: -25,
        upper: 25,
        ..Default::default()
    };
    for (value, expected) in [(-30, -25), (-4, -4), (30, 25)] {
        assert_eq!(
            output(vec![instruction(Opcode::Constant, value), clamp]),
            expected
        );
    }
    let mut both = program(vec![instruction(Opcode::Input, 0)]);
    both.acceleration = vec![Instruction {
        opcode: Opcode::Input as i32,
        input_index: 1,
        ..Default::default()
    }];
    let action = evaluate(&both, &observation()).unwrap();
    assert_eq!((action.steering, action.acceleration), (333, -333));
    assert_eq!((action.epoch, action.sequence), (0, 0));
}

#[test]
fn saturates_intermediate_operations_before_later_cancellation() {
    for sign in [-1, 1] {
        assert_eq!(
            output(vec![
                instruction(Opcode::Constant, sign * VALUE_LIMIT),
                instruction(Opcode::Constant, sign * VALUE_LIMIT),
                instruction(Opcode::Add, 0),
                instruction(Opcode::Constant, -sign * VALUE_LIMIT),
                instruction(Opcode::Add, 0),
            ]),
            0
        );
        assert_eq!(
            output(vec![
                instruction(Opcode::Constant, sign * VALUE_LIMIT),
                instruction(Opcode::Scale, 1_000_000),
                instruction(Opcode::Constant, -sign * VALUE_LIMIT),
                instruction(Opcode::Add, 0),
            ]),
            0
        );
        assert_eq!(
            output(vec![instruction(Opcode::Constant, sign * VALUE_LIMIT)]),
            sign * 1000
        );
    }
}

#[test]
fn rejects_malformed_postfix_programs_and_operands_without_panicking() {
    let malformed = [
        vec![],
        vec![instruction(Opcode::Unspecified, 0)],
        vec![Instruction {
            opcode: 99,
            ..Default::default()
        }],
        vec![instruction(Opcode::Add, 0)],
        vec![
            instruction(Opcode::Constant, 1),
            instruction(Opcode::Add, 0),
        ],
        vec![instruction(Opcode::Scale, 1)],
        vec![instruction(Opcode::Clamp, 0)],
        vec![
            instruction(Opcode::Constant, 1),
            instruction(Opcode::Constant, 2),
        ],
        vec![instruction(Opcode::Constant, i64::MAX)],
        vec![instruction(Opcode::Constant, i64::MIN)],
        vec![
            instruction(Opcode::Constant, 1),
            instruction(Opcode::Scale, 1_000_001),
        ],
        vec![
            instruction(Opcode::Constant, 1),
            instruction(Opcode::Scale, -1_000_001),
        ],
        vec![Instruction {
            opcode: Opcode::Input as i32,
            input_index: 4,
            ..Default::default()
        }],
        vec![Instruction {
            opcode: Opcode::Input as i32,
            value: 1,
            ..Default::default()
        }],
        vec![Instruction {
            opcode: Opcode::Constant as i32,
            input_index: 1,
            ..Default::default()
        }],
        vec![Instruction {
            opcode: Opcode::Constant as i32,
            lower: 1,
            ..Default::default()
        }],
        vec![
            instruction(Opcode::Constant, 1),
            Instruction {
                opcode: Opcode::Clamp as i32,
                lower: 1,
                upper: -1,
                ..Default::default()
            },
        ],
        vec![
            instruction(Opcode::Constant, 1),
            Instruction {
                opcode: Opcode::Clamp as i32,
                lower: i64::MIN,
                ..Default::default()
            },
        ],
        vec![
            instruction(Opcode::Constant, 1),
            Instruction {
                opcode: Opcode::Clamp as i32,
                upper: i64::MAX,
                ..Default::default()
            },
        ],
    ];
    for instructions in malformed {
        assert!(
            evaluate(&program(instructions.clone()), &observation()).is_err(),
            "accepted {instructions:?}"
        );
    }
    let mut invalid_version = program(vec![instruction(Opcode::Constant, 0)]);
    invalid_version.schema_version = 2;
    assert!(evaluate(&invalid_version, &observation()).is_err());
}

fn balanced_expression(leaves: usize) -> Vec<Instruction> {
    if leaves == 1 {
        return vec![instruction(Opcode::Constant, 0)];
    }
    let mut result = balanced_expression(leaves / 2);
    result.extend(balanced_expression(leaves - leaves / 2));
    result.push(instruction(Opcode::Add, 0));
    result
}

#[test]
fn enforces_depth_and_per_output_instruction_limits_at_the_boundary() {
    let mut deep = vec![instruction(Opcode::Constant, 1)];
    deep.extend(std::iter::repeat_n(instruction(Opcode::Scale, 1000), 15));
    assert_eq!(output(deep.clone()), 1);
    deep.push(instruction(Opcode::Scale, 1000));
    assert!(evaluate(&program(deep), &observation()).is_err());

    let mut exact_budget = balanced_expression(64);
    exact_budget.push(instruction(Opcode::Scale, 1000));
    assert_eq!(exact_budget.len(), MAX_INSTRUCTIONS);
    let both = Program {
        schema_version: 1,
        steering: exact_budget.clone(),
        acceleration: exact_budget.clone(),
        ..Default::default()
    };
    assert!(evaluate(&both, &observation()).is_ok());
    exact_budget.push(instruction(Opcode::Scale, 1000));
    assert!(evaluate(&program(exact_budget), &observation()).is_err());
}

#[test]
fn rejects_invalid_observations() {
    let candidate = program(vec![instruction(Opcode::Constant, 0)]);
    for features in [
        vec![],
        vec![0; 3],
        vec![0; 5],
        vec![10_001, 0, 0, 0],
        vec![0, -10_001, 0, 0],
    ] {
        let input = Observation {
            features,
            ..Default::default()
        };
        assert!(evaluate(&candidate, &input).is_err());
    }
}

fn valid_request() -> serde_json::Value {
    serde_json::json!({
        "kind": "REQUEST_KIND_EVALUATE",
        "program": {
            "schemaVersion": 1,
            "steering": [{ "opcode": "OPCODE_INPUT" }, { "opcode": "OPCODE_SCALE", "value": "-500" }],
            "acceleration": [{ "opcode": "OPCODE_CONSTANT", "value": "250" }]
        },
        "observation": { "features": ["333", "0", "0", "0"] }
    })
}

#[test]
fn jsonl_uses_protojson_contract_and_rejects_unknown_fields_and_request_kinds() {
    let request = valid_request();
    let response = handle_line(&request.to_string());
    assert!(response.error.is_empty(), "{}", response.error);
    let action = response.action.as_ref().unwrap();
    assert_eq!((action.steering, action.acceleration), (-166, 250));
    let encoded = encode_response(&response).unwrap();
    assert!(encoded.contains("\"steering\":\"-166\""));
    let decoded: RuntimeResponse = decode_json(&encoded).unwrap();
    assert_eq!(decoded, response);

    for path in [vec![], vec!["program"], vec!["observation"]] {
        let mut unknown = request.clone();
        let mut target = &mut unknown;
        for key in path {
            target = &mut target[key];
        }
        target["unknown"] = serde_json::json!(true);
        assert!(handle_line(&unknown.to_string()).action.is_none());
    }
    let mut unknown_instruction = request.clone();
    unknown_instruction["program"]["steering"][0]["arguments"] = serde_json::json!([]);
    assert!(handle_line(&unknown_instruction.to_string())
        .action
        .is_none());
    for opcode in [
        serde_json::json!(999),
        serde_json::json!("OPCODE_EXECUTE_SHELL"),
    ] {
        let mut bad = request.clone();
        bad["program"]["steering"][0]["opcode"] = opcode;
        assert!(handle_line(&bad.to_string()).action.is_none());
    }
    for kind in [
        serde_json::json!("REQUEST_KIND_STEP"),
        serde_json::json!(99),
    ] {
        let mut bad = request.clone();
        bad["kind"] = kind;
        assert!(handle_line(&bad.to_string()).action.is_none());
    }
    for field in ["program", "observation"] {
        let mut missing = request.clone();
        missing.as_object_mut().unwrap().remove(field);
        assert!(handle_line(&missing.to_string()).action.is_none());
    }
    for malformed in ["", "null", "[]", "{} {}", "{", "{\"kind\":true}"] {
        assert!(handle_line(malformed).action.is_none());
    }
}

fn run_binary(input: &[u8]) -> Vec<RuntimeResponse> {
    use std::io::Write;
    use std::process::{Command, Stdio};
    let mut child = Command::new(env!("CARGO_BIN_EXE_brain-spine-conformance"))
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .spawn()
        .unwrap();
    child.stdin.take().unwrap().write_all(input).unwrap();
    let output = child.wait_with_output().unwrap();
    assert!(output.status.success());
    String::from_utf8(output.stdout)
        .unwrap()
        .lines()
        .map(|line| decode_json(line).unwrap())
        .collect()
}

#[test]
fn executable_recovers_after_invalid_lines_and_emits_one_response_per_line() {
    let request = valid_request().to_string();
    let replies = run_binary(format!("{request}\ninvalid JSON\n{request}\n").as_bytes());
    assert_eq!(replies.len(), 3);
    assert!(replies[0].action.is_some());
    assert!(!replies[1].error.is_empty());
    assert_eq!(replies[2], replies[0]);
}

#[test]
fn rejects_duplicate_json_fields_recursively_including_protojson_aliases() {
    let request = valid_request().to_string();
    let duplicate_requests = [
        request.replacen(
            "\"kind\":\"REQUEST_KIND_EVALUATE\"",
            "\"kind\":\"REQUEST_KIND_STEP\",\"kind\":\"REQUEST_KIND_EVALUATE\"",
            1,
        ),
        request.replacen(
            "\"schemaVersion\":1",
            "\"schemaVersion\":2,\"schemaVersion\":1",
            1,
        ),
        request.replacen(
            "\"schemaVersion\":1",
            "\"schema_version\":2,\"schemaVersion\":1",
            1,
        ),
        request.replacen(
            "\"value\":\"-500\"",
            "\"value\":\"1000\",\"value\":\"-500\"",
            1,
        ),
        request.replacen("\"features\":", "\"features\":null,\"features\":", 1),
        request.replacen("\"features\":", "\"features\":[],\"features\":", 1),
    ];
    for duplicate in duplicate_requests {
        let response = handle_line(&duplicate);
        assert!(response.action.is_none(), "accepted {duplicate}");
        assert!(
            response.error.contains("duplicate field"),
            "{}",
            response.error
        );
    }
    // An alternate spelling used once remains valid, as required by ProtoJSON.
    let alias = request.replace("\"schemaVersion\":", "\"schema_version\":");
    assert_eq!(handle_line(&alias), handle_line(&request));
    // Repeated messages each have their own key scope; equal keys across array
    // elements are legitimate and must not be mistaken for object duplicates.
    assert!(handle_line(&request).action.is_some());
}

#[test]
fn executable_rejects_oversized_lines_with_bounded_read() {
    let replies = run_binary(&vec![b' '; 65_537]);
    assert_eq!(replies.len(), 1);
    assert_eq!(replies[0].error, "request exceeds 65536 bytes");
}
