// Copyright 2026 Candace Labs

//! Writing one session's cgroup v2 limits and moving the launcher into it.
//!
//! The harness owns the cgroup hierarchy: it runs in a delegated subtree and
//! creates an empty leaf for each session before it launches the executor. The
//! launcher's only cgroup job is to join that leaf and write the limits, so
//! every process the executor forks is born into the session's cgroup and one
//! write to `cgroup.kill` ends them all.

use std::path::Path;

use anyhow::{Context, Result};

use crate::policy::Limits;

/// The cgroup files the launcher writes and the value each receives, in the
/// order they are written. Pure, so a test pins the exact bytes a policy maps
/// to without touching the filesystem.
pub fn limit_files(limits: &Limits) -> Vec<(&'static str, String)> {
    vec![
        ("cpu.max", limits.cpu_max.clone()),
        ("memory.max", limits.memory_max_bytes.to_string()),
        ("memory.swap.max", limits.memory_swap_max_bytes.to_string()),
        ("pids.max", limits.pids_max.to_string()),
    ]
}

/// Moves the current process into `cgroup` by writing its pid to `cgroup.procs`.
/// Done before writing the limits so the limits already bind this process.
pub fn join(cgroup: &Path) -> Result<()> {
    let procs = cgroup.join("cgroup.procs");
    std::fs::write(&procs, std::process::id().to_string())
        .with_context(|| format!("join cgroup via {}", procs.display()))
}

/// Writes every limit from [`limit_files`] into `cgroup`.
pub fn write_limits(cgroup: &Path, limits: &Limits) -> Result<()> {
    for (name, value) in limit_files(limits) {
        let path = cgroup.join(name);
        std::fs::write(&path, &value)
            .with_context(|| format!("write {} = {}", path.display(), value))?;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn limits() -> Limits {
        Limits {
            cpu_max: "1600000 100000".into(),
            memory_max_bytes: 8_589_934_592,
            memory_swap_max_bytes: 0,
            pids_max: 4096,
            derivation: "measured".into(),
        }
    }

    #[test]
    fn renders_the_four_limit_files_in_order() {
        assert_eq!(
            limit_files(&limits()),
            vec![
                ("cpu.max", "1600000 100000".to_string()),
                ("memory.max", "8589934592".to_string()),
                ("memory.swap.max", "0".to_string()),
                ("pids.max", "4096".to_string()),
            ]
        );
    }

    #[test]
    fn write_limits_writes_each_file() {
        let dir = tempfile::tempdir().unwrap();
        for (name, _) in limit_files(&limits()) {
            std::fs::write(dir.path().join(name), "placeholder").unwrap();
        }
        write_limits(dir.path(), &limits()).unwrap();
        assert_eq!(
            std::fs::read_to_string(dir.path().join("memory.max")).unwrap(),
            "8589934592"
        );
        assert_eq!(
            std::fs::read_to_string(dir.path().join("pids.max")).unwrap(),
            "4096"
        );
    }
}
