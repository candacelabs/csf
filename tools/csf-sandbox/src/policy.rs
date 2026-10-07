// Copyright 2026 Candace Labs

//! The sandbox policy for one session, as the harness writes it and the
//! launcher reads it.

use std::path::{Path, PathBuf};

use anyhow::{bail, Context, Result};
use serde::{Deserialize, Serialize};

/// The resource limits applied to a session's cgroup. Each is written verbatim
/// to the matching `cgroup` file; [`Limits::derivation`] records how the
/// harness chose them, so a receipt can show the basis for every bound.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Limits {
    /// The `cpu.max` value: either `"max"` or `"<quota> <period>"` in
    /// microseconds, as cgroup v2 spells it.
    pub cpu_max: String,
    /// The `memory.max` value in bytes.
    pub memory_max_bytes: u64,
    /// The `memory.swap.max` value in bytes; zero forbids swap.
    pub memory_swap_max_bytes: u64,
    /// The `pids.max` value: the maximum number of tasks in the cgroup.
    pub pids_max: u64,
    /// How the harness derived these bounds, recorded beside them.
    pub derivation: String,
}

/// Everything the launcher needs to confine one session and exec its executor.
///
/// The executor's own argument vector is not part of the policy: it follows the
/// launcher's arguments after `--`, exactly as the harness assembled the
/// executor command before the sandbox prefixed it.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct SessionPolicy {
    /// The session (assignment) identifier this policy confines.
    pub session_id: String,
    /// The session's own cgroup: an existing leaf directory under the harness's
    /// delegated subtree that the launcher moves itself into.
    pub cgroup: PathBuf,
    /// The resource bounds the launcher writes into [`SessionPolicy::cgroup`].
    pub limits: Limits,
    /// Paths the session may read and write: its worktree, run directory, its
    /// Bazel and Go caches and a private tmp.
    pub read_write_paths: Vec<PathBuf>,
    /// Paths the session may read and execute but not write: the system and
    /// toolchain paths and the harness state it reads logs from.
    pub read_only_paths: Vec<PathBuf>,
    /// The Docker proxy socket this session's `DOCKER_HOST` points at, if the
    /// session is granted the container capability at all.
    #[serde(default)]
    pub docker_proxy_socket: Option<PathBuf>,
}

impl SessionPolicy {
    /// Reads and validates a policy from a JSON file.
    pub fn load(path: &Path) -> Result<Self> {
        let content = std::fs::read(path)
            .with_context(|| format!("read sandbox policy {}", path.display()))?;
        let policy: SessionPolicy = serde_json::from_slice(&content)
            .with_context(|| format!("decode sandbox policy {}", path.display()))?;
        policy.validate()?;
        Ok(policy)
    }

    /// Rejects a policy the launcher cannot act on: a relative cgroup path, a
    /// relative allow-list path, or an empty session identifier. Catching these
    /// before any enforcement keeps a half-applied sandbox from ever existing.
    pub fn validate(&self) -> Result<()> {
        if self.session_id.is_empty() {
            bail!("sandbox policy: session_id is empty");
        }
        if !self.cgroup.is_absolute() {
            bail!("sandbox policy: cgroup {} is not absolute", self.cgroup.display());
        }
        for path in self.read_write_paths.iter().chain(self.read_only_paths.iter()) {
            if !path.is_absolute() {
                bail!("sandbox policy: allow-list path {} is not absolute", path.display());
            }
        }
        if self.read_write_paths.is_empty() {
            bail!("sandbox policy: no read-write paths; the session could not write its worktree");
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn sample() -> SessionPolicy {
        SessionPolicy {
            session_id: "s1".into(),
            cgroup: PathBuf::from("/sys/fs/cgroup/x/sessions/s1"),
            limits: Limits {
                cpu_max: "1600000 100000".into(),
                memory_max_bytes: 8 << 30,
                memory_swap_max_bytes: 0,
                pids_max: 4096,
                derivation: "p95 peak x1.5".into(),
            },
            read_write_paths: vec![PathBuf::from("/run/worktree")],
            read_only_paths: vec![PathBuf::from("/usr")],
            docker_proxy_socket: Some(PathBuf::from("/run/s1/docker.sock")),
        }
    }

    #[test]
    fn round_trips_through_json() {
        let policy = sample();
        let encoded = serde_json::to_vec(&policy).unwrap();
        let decoded: SessionPolicy = serde_json::from_slice(&encoded).unwrap();
        assert_eq!(policy, decoded);
    }

    #[test]
    fn docker_socket_is_optional() {
        let json = r#"{
            "session_id": "s1",
            "cgroup": "/sys/fs/cgroup/x/sessions/s1",
            "limits": {"cpu_max": "max", "memory_max_bytes": 1, "memory_swap_max_bytes": 0, "pids_max": 1, "derivation": "d"},
            "read_write_paths": ["/run/worktree"],
            "read_only_paths": ["/usr"]
        }"#;
        let policy: SessionPolicy = serde_json::from_str(json).unwrap();
        assert_eq!(policy.docker_proxy_socket, None);
        policy.validate().unwrap();
    }

    #[test]
    fn rejects_relative_cgroup() {
        let mut policy = sample();
        policy.cgroup = PathBuf::from("relative/cgroup");
        assert!(policy.validate().is_err());
    }

    #[test]
    fn rejects_relative_allow_list_path() {
        let mut policy = sample();
        policy.read_only_paths.push(PathBuf::from("relative/path"));
        assert!(policy.validate().is_err());
    }

    #[test]
    fn rejects_empty_read_write_paths() {
        let mut policy = sample();
        policy.read_write_paths.clear();
        assert!(policy.validate().is_err());
    }
}
