use std::process::Command;

#[test]
fn help_succeeds_on_stdout_without_starting_the_runtime() {
    for args in [
        ["--help"].as_slice(),
        ["up", "--help"].as_slice(),
        ["docs", "check", "--help"].as_slice(),
    ] {
        let output = Command::new(env!("CARGO_BIN_EXE_csf"))
            .args(args)
            .output()
            .unwrap();
        assert!(output.status.success(), "{output:?}");
        assert!(String::from_utf8_lossy(&output.stdout).contains("Usage:"));
        assert!(output.stderr.is_empty());
    }
}

#[test]
fn docs_check_reports_missing_renderer_without_discovering_runtime() {
    let directory = tempfile::TempDir::new().unwrap();
    let output = Command::new(env!("CARGO_BIN_EXE_csf"))
        .args(["docs", "check", "--repo"])
        .arg(directory.path())
        .arg("--work")
        .arg(directory.path().join("work"))
        .arg("--site")
        .arg(directory.path().join("site"))
        .env("CANDACE_CSF_ROOT", "/does-not-exist")
        .output()
        .unwrap();
    assert_eq!(output.status.code(), Some(1));
    assert!(
        String::from_utf8_lossy(&output.stderr).contains("documentation renderer is unavailable")
    );
    assert!(!directory.path().join("work").exists());
}

#[test]
fn invalid_arguments_fail_on_stderr() {
    let output = Command::new(env!("CARGO_BIN_EXE_csf"))
        .arg("--not-a-real-option")
        .output()
        .unwrap();
    assert_eq!(output.status.code(), Some(2));
    assert!(String::from_utf8_lossy(&output.stderr).contains("unexpected argument"));
    assert!(output.stdout.is_empty());
}
