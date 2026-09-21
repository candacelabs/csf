use serde_json::{Map, Value};
use sha2::{Digest, Sha256};
use std::env;
use std::fs::{self, File};
use std::io::{self, Read, Write};
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use tempfile::NamedTempFile;

struct Args {
    component: String,
    version: String,
    target: String,
    source: String,
    source_sha256: String,
    payload: PathBuf,
}

fn main() {
    if let Err(error) = run() {
        eprintln!("native payload receipt: {error}");
        std::process::exit(1);
    }
}

fn run() -> Result<(), Box<dyn std::error::Error>> {
    let args = parse_args()?;
    let payload = args.payload.canonicalize()?;
    let mut files = Vec::new();
    collect(&payload, &payload, &mut files)?;
    files.sort_by(|left: &Value, right| left["path"].as_str().cmp(&right["path"].as_str()));
    if files.is_empty() {
        return Err("payload contains no files".into());
    }

    let mut receipt = Map::new();
    receipt.insert("component".into(), args.component.into());
    receipt.insert("files".into(), Value::Array(files));
    receipt.insert("source".into(), args.source.into());
    receipt.insert("source_sha256".into(), args.source_sha256.into());
    receipt.insert("target".into(), args.target.into());
    receipt.insert("version".into(), args.version.into());
    let destination = payload.join("PAYLOAD_RECEIPT.json");
    write_receipt(&destination, &Value::Object(receipt))?;
    Ok(())
}

fn write_receipt(destination: &Path, receipt: &Value) -> io::Result<()> {
    let parent = destination
        .parent()
        .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidInput, "receipt has no parent"))?;
    let mut output = NamedTempFile::new_in(parent)?;
    serde_json::to_writer_pretty(output.as_file_mut(), receipt)
        .map_err(|error| io::Error::other(format!("serialize receipt: {error}")))?;
    output.write_all(b"\n")?;
    output.as_file().sync_all()?;
    output
        .persist(destination)
        .map(|_| ())
        .map_err(|error| error.error)
}

fn parse_args() -> Result<Args, String> {
    let mut values = std::collections::BTreeMap::new();
    let mut args = env::args().skip(1);
    while let Some(key) = args.next() {
        let value = args
            .next()
            .ok_or_else(|| format!("missing value for {key}"))?;
        if ![
            "--component",
            "--version",
            "--target",
            "--source",
            "--source-sha256",
            "--payload",
        ]
        .contains(&key.as_str())
        {
            return Err(format!("unknown argument: {key}"));
        }
        if values.insert(key.clone(), value).is_some() {
            return Err(format!("duplicate argument: {key}"));
        }
    }
    let mut take = |key: &str| {
        values
            .remove(key)
            .ok_or_else(|| format!("missing required argument: {key}"))
    };
    Ok(Args {
        component: take("--component")?,
        version: take("--version")?,
        target: take("--target")?,
        source: take("--source")?,
        source_sha256: take("--source-sha256")?,
        payload: take("--payload")?.into(),
    })
}

fn collect(
    root: &Path,
    directory: &Path,
    files: &mut Vec<Value>,
) -> Result<(), Box<dyn std::error::Error>> {
    let mut entries = fs::read_dir(directory)?.collect::<Result<Vec<_>, _>>()?;
    entries.sort_by_key(|entry| entry.file_name());
    for entry in entries {
        let path = entry.path();
        if path
            .file_name()
            .is_some_and(|name| name == "PAYLOAD_RECEIPT.json")
        {
            continue;
        }
        let metadata = fs::symlink_metadata(&path)?;
        if metadata.file_type().is_dir() {
            collect(root, &path, files)?;
            continue;
        }
        if !metadata.file_type().is_file() && !metadata.file_type().is_symlink() {
            continue;
        }
        let relative = path
            .strip_prefix(root)?
            .to_str()
            .ok_or("non-UTF-8 payload path")?;
        let mut record = Map::new();
        record.insert(
            "mode".into(),
            (metadata.permissions().mode() & 0o7777).into(),
        );
        record.insert("path".into(), relative.into());
        if metadata.file_type().is_symlink() {
            let target = fs::read_link(&path)?;
            if target.is_absolute() {
                return Err(format!("absolute payload symlink: {relative}").into());
            }
            let resolved = path.canonicalize().map_err(|error| {
                format!("dangling or cyclic payload symlink: {relative}: {error}")
            })?;
            let resolved_metadata = fs::metadata(&resolved)?;
            let parent = path
                .parent()
                .ok_or("symlink has no parent")?
                .canonicalize()?;
            if !resolved.starts_with(root)
                || (!resolved_metadata.is_file() && !resolved_metadata.is_dir())
                || (resolved_metadata.is_dir() && parent.starts_with(&resolved))
            {
                return Err(format!("escaping payload symlink: {relative}").into());
            }
            let target_text = target.to_str().ok_or("non-UTF-8 symlink target")?;
            record.insert(
                "sha256".into(),
                hex(&Sha256::digest(target_text.as_bytes())).into(),
            );
            record.insert("target".into(), target_text.into());
            record.insert("type".into(), "symlink".into());
        } else {
            record.insert("sha256".into(), hash_file(&path)?.into());
            record.insert("type".into(), "file".into());
        }
        files.push(Value::Object(record));
    }
    Ok(())
}

fn hash_file(path: &Path) -> io::Result<String> {
    let mut source = File::open(path)?;
    let mut digest = Sha256::new();
    let mut buffer = [0_u8; 1024 * 1024];
    loop {
        let count = source.read(&mut buffer)?;
        if count == 0 {
            break;
        }
        digest.update(&buffer[..count]);
    }
    Ok(hex(&digest.finalize()))
}

fn hex(bytes: &[u8]) -> String {
    const DIGITS: &[u8; 16] = b"0123456789abcdef";
    let mut result = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        result.push(DIGITS[(byte >> 4) as usize] as char);
        result.push(DIGITS[(byte & 0x0f) as usize] as char);
    }
    result
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::symlink;

    fn build_receipt(directory: &Path) -> Result<Value, Box<dyn std::error::Error>> {
        let mut files = Vec::new();
        let root = directory.canonicalize()?;
        collect(&root, &root, &mut files)?;
        files.sort_by(|left: &Value, right| left["path"].as_str().cmp(&right["path"].as_str()));
        let mut receipt = Map::new();
        receipt.insert("component".into(), "test".into());
        receipt.insert("files".into(), Value::Array(files));
        receipt.insert("source".into(), "fixture".into());
        receipt.insert("source_sha256".into(), "abc".into());
        receipt.insert("target".into(), "linux/amd64".into());
        receipt.insert("version".into(), "1".into());
        Ok(Value::Object(receipt))
    }

    #[test]
    fn hashes_regular_files_and_relative_symlinks_deterministically() {
        let directory = tempfile::tempdir().unwrap();
        fs::write(directory.path().join("z-file"), b"payload").unwrap();
        symlink("z-file", directory.path().join("a-link")).unwrap();
        let receipt = build_receipt(directory.path()).unwrap();
        let files = receipt["files"].as_array().unwrap();
        assert_eq!(files[0]["path"], "a-link");
        assert_eq!(files[0]["type"], "symlink");
        assert_eq!(files[0]["target"], "z-file");
        assert_eq!(
            files[0]["sha256"],
            "94256cf278c7ab79b9281d6f905e55004c425a133e8c911574cef6fa6bafe0ee"
        );
        assert_eq!(
            files[1]["sha256"],
            "239f59ed55e737c77147cf55ad0c1b030b6d7ee748a7426952f9b852d5a935e5"
        );
    }

    #[test]
    fn rejects_absolute_dangling_and_escaping_symlinks() {
        let directory = tempfile::tempdir().unwrap();
        let outside = tempfile::tempdir().unwrap();
        fs::write(outside.path().join("outside"), b"x").unwrap();
        symlink(
            outside.path().join("outside"),
            directory.path().join("escape"),
        )
        .unwrap();
        assert!(collect(
            &directory.path().canonicalize().unwrap(),
            directory.path(),
            &mut Vec::new()
        )
        .is_err());
        fs::remove_file(directory.path().join("escape")).unwrap();
        symlink("missing", directory.path().join("dangling")).unwrap();
        assert!(collect(
            &directory.path().canonicalize().unwrap(),
            directory.path(),
            &mut Vec::new()
        )
        .is_err());
        fs::remove_file(directory.path().join("dangling")).unwrap();
        symlink("/tmp", directory.path().join("absolute")).unwrap();
        assert!(collect(
            &directory.path().canonicalize().unwrap(),
            directory.path(),
            &mut Vec::new()
        )
        .is_err());
        fs::remove_file(directory.path().join("absolute")).unwrap();
        symlink("loop-b", directory.path().join("loop-a")).unwrap();
        symlink("loop-a", directory.path().join("loop-b")).unwrap();
        assert!(collect(
            &directory.path().canonicalize().unwrap(),
            directory.path(),
            &mut Vec::new()
        )
        .is_err());
    }

    #[test]
    fn keeps_the_python_writer_json_format_byte_for_byte() {
        let directory = tempfile::tempdir().unwrap();
        let payload = directory.path().join("payload");
        fs::write(&payload, b"payload").unwrap();
        fs::set_permissions(&payload, fs::Permissions::from_mode(0o644)).unwrap();
        let receipt = build_receipt(directory.path()).unwrap();
        let rendered = serde_json::to_string_pretty(&receipt).unwrap() + "\n";
        assert_eq!(
            rendered,
            concat!(
                "{\n",
                "  \"component\": \"test\",\n",
                "  \"files\": [\n",
                "    {\n",
                "      \"mode\": 420,\n",
                "      \"path\": \"payload\",\n",
                "      \"sha256\": \"239f59ed55e737c77147cf55ad0c1b030b6d7ee748a7426952f9b852d5a935e5\",\n",
                "      \"type\": \"file\"\n",
                "    }\n",
                "  ],\n",
                "  \"source\": \"fixture\",\n",
                "  \"source_sha256\": \"abc\",\n",
                "  \"target\": \"linux/amd64\",\n",
                "  \"version\": \"1\"\n",
                "}\n"
            )
        );
    }

    #[test]
    fn atomically_replaces_receipt_symlinks_without_writing_through_them() {
        let directory = tempfile::tempdir().unwrap();
        let outside = tempfile::tempdir().unwrap();
        let outside_receipt = outside.path().join("receipt.json");
        fs::write(&outside_receipt, b"keep me").unwrap();
        let destination = directory.path().join("PAYLOAD_RECEIPT.json");
        symlink(&outside_receipt, &destination).unwrap();
        let receipt = build_receipt(directory.path()).unwrap();

        write_receipt(&destination, &receipt).unwrap();

        assert_eq!(fs::read_to_string(&outside_receipt).unwrap(), "keep me");
        assert!(!fs::symlink_metadata(&destination)
            .unwrap()
            .file_type()
            .is_symlink());
        let rendered: Value = serde_json::from_reader(File::open(destination).unwrap()).unwrap();
        assert_eq!(rendered, receipt);
    }

    #[test]
    fn atomically_replaces_dangling_receipt_symlink() {
        let directory = tempfile::tempdir().unwrap();
        let destination = directory.path().join("PAYLOAD_RECEIPT.json");
        symlink("missing-receipt.json", &destination).unwrap();
        let receipt = build_receipt(directory.path()).unwrap();

        write_receipt(&destination, &receipt).unwrap();

        assert!(!fs::symlink_metadata(&destination)
            .unwrap()
            .file_type()
            .is_symlink());
        let rendered: Value = serde_json::from_reader(File::open(destination).unwrap()).unwrap();
        assert_eq!(rendered, receipt);
    }

    #[test]
    fn atomically_replaces_receipt_hardlinks_without_overwriting_the_linked_file() {
        let directory = tempfile::tempdir().unwrap();
        let outside = tempfile::tempdir().unwrap();
        let outside_receipt = outside.path().join("receipt.json");
        fs::write(&outside_receipt, b"keep me").unwrap();
        let destination = directory.path().join("PAYLOAD_RECEIPT.json");
        fs::hard_link(&outside_receipt, &destination).unwrap();
        let receipt = build_receipt(directory.path()).unwrap();

        write_receipt(&destination, &receipt).unwrap();

        assert_eq!(fs::read_to_string(&outside_receipt).unwrap(), "keep me");
        let rendered: Value = serde_json::from_reader(File::open(destination).unwrap()).unwrap();
        assert_eq!(rendered, receipt);
    }
}
