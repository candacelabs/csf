use brain_spine_conformance::{contract::RuntimeResponse, encode_response, handle_line};
use std::io::{self, BufRead, Read, Write};

const MAX_LINE_BYTES: usize = 65_536;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let mut input = io::stdin().lock();
    let mut output = io::BufWriter::new(io::stdout().lock());
    loop {
        // Read a capped line. Oversized input terminates after one error rather
        // than accumulating an unbounded allocation or an unbounded drain.
        let mut bytes = Vec::new();
        let mut limited = (&mut input).take((MAX_LINE_BYTES + 1) as u64);
        let read = limited.read_until(b'\n', &mut bytes)?;
        if read == 0 {
            break;
        }
        let oversized = read > MAX_LINE_BYTES;
        let response = if oversized {
            RuntimeResponse {
                error: "request exceeds 65536 bytes".into(),
                ..Default::default()
            }
        } else {
            match std::str::from_utf8(&bytes) {
                Ok(line) => handle_line(line),
                Err(error) => RuntimeResponse {
                    error: format!("invalid UTF-8: {error}"),
                    ..Default::default()
                },
            }
        };
        writeln!(output, "{}", encode_response(&response)?)?;
        output.flush()?;
        if oversized {
            break;
        }
    }
    Ok(())
}
