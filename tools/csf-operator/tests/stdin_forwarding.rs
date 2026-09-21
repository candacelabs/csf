#![cfg(unix)]

use std::fs;
use std::io::Write;
use std::os::unix::fs::PermissionsExt;
use std::process::{Command, Stdio};
use tempfile::TempDir;

#[test]
fn call_forwards_piped_json_through_the_system_runner() {
    let temporary = TempDir::new().unwrap();
    let source = temporary.path().join("source");
    let state = temporary.path().join("state");
    let bin = temporary.path().join("bin");
    let captured = temporary.path().join("captured.json");
    fs::create_dir_all(source.join("infra")).unwrap();
    fs::create_dir_all(source.join("app/csf/cmd")).unwrap();
    fs::create_dir_all(&state).unwrap();
    fs::create_dir_all(&bin).unwrap();
    fs::write(source.join("go.mod"), "module example.invalid/csf\n").unwrap();
    fs::write(source.join("app/csf/cmd/main.go"), "package main\n").unwrap();
    fs::write(source.join("app/csf/Dockerfile"), "FROM scratch\n").unwrap();
    fs::write(source.join("infra/docker-compose.yaml"), "name: csf\n").unwrap();
    fs::write(
        state.join("credentials.env"),
        "CSF_RUNTIME_ADDRESS=127.0.0.1\nCSF_RUNTIME_PORT=14111\n",
    )
    .unwrap();

    let docker = bin.join("docker");
    fs::write(
        &docker,
        format!(
            "#!/bin/sh\ncat > '{}'\nprintf 'forwarded\\n'\n",
            captured.display()
        ),
    )
    .unwrap();
    fs::set_permissions(&docker, fs::Permissions::from_mode(0o755)).unwrap();

    let input = b"{\"query\":\"hello\"}\n";
    let mut child = Command::new(env!("CARGO_BIN_EXE_candace"))
        .args(["csf", "call", "Search"])
        .env(
            "PATH",
            format!("{}:{}", bin.display(), std::env::var("PATH").unwrap()),
        )
        .env("CANDACE_CSF_ROOT", &source)
        .env("CANDACE_CSF_STATE_DIR", &state)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    child.stdin.take().unwrap().write_all(input).unwrap();
    let output = child.wait_with_output().unwrap();

    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(fs::read(&captured).unwrap(), input);
    assert_eq!(String::from_utf8_lossy(&output.stdout), "forwarded\n");
}
