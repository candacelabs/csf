//! prost-reflect accepts duplicate keys. Preflight only message keys, using the
//! generated descriptor to give snake_case and lowerCamelCase the same identity.
//! Values, enum names and the remaining ProtoJSON rules stay with prost-reflect.

use prost_reflect::{Kind, MessageDescriptor};
use serde::de::{DeserializeSeed, Error, IgnoredAny, MapAccess, SeqAccess, Visitor};
use std::{collections::HashSet, fmt};

pub fn check(descriptor: MessageDescriptor, json: &str) -> Result<(), String> {
    let mut parser = serde_json::Deserializer::from_str(json);
    MessageKeys(descriptor)
        .deserialize(&mut parser)
        .map_err(|error| error.to_string())?;
    parser.end().map_err(|error| error.to_string())
}

#[derive(Clone)]
struct MessageKeys(MessageDescriptor);

impl<'de> DeserializeSeed<'de> for MessageKeys {
    type Value = ();

    fn deserialize<D: serde::Deserializer<'de>>(self, deserializer: D) -> Result<(), D::Error> {
        deserializer.deserialize_any(self)
    }
}

impl<'de> Visitor<'de> for MessageKeys {
    type Value = ();

    fn expecting(&self, formatter: &mut fmt::Formatter) -> fmt::Result {
        formatter.write_str("a protobuf message, repeated messages, or null")
    }

    fn visit_unit<E: Error>(self) -> Result<(), E> {
        Ok(())
    }

    fn visit_seq<A: SeqAccess<'de>>(self, mut values: A) -> Result<(), A::Error> {
        while values.next_element_seed(self.clone())?.is_some() {}
        Ok(())
    }

    fn visit_map<A: MapAccess<'de>>(self, mut fields: A) -> Result<(), A::Error> {
        let mut seen = HashSet::new();
        while let Some(key) = fields.next_key::<String>()? {
            let field = self
                .0
                .get_field_by_json_name(&key)
                .or_else(|| self.0.get_field_by_name(&key))
                .ok_or_else(|| A::Error::custom(format!("unknown field {key}")))?;
            if !seen.insert(field.number()) {
                return Err(A::Error::custom(format!("duplicate field {key}")));
            }
            match field.kind() {
                Kind::Message(descriptor) => fields.next_value_seed(MessageKeys(descriptor))?,
                _ => {
                    fields.next_value::<IgnoredAny>()?;
                }
            }
        }
        Ok(())
    }
}
