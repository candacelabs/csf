// Copyright 2026 Candace Labs

//! The Landlock filesystem view applied to a confined session.
//!
//! The session may read, execute and write only its own worktree, run
//! directory, caches and private tmp; it may read and execute the system and
//! toolchain paths; it may open nothing else. The rest of the home directory and
//! other sessions' run directories are outside the allow-list and cannot be
//! opened. Landlock is unprivileged, so this needs no capability and composes
//! with `no_new_privs`.
//!
//! Landlock ABI 4 governs filesystem *opens* and TCP bind/connect; it has no
//! access right for connecting to a Unix-domain socket by path. So Landlock does
//! NOT block `connect(2)` to the Docker or lxd sockets: a confined process can
//! still reach them directly, bypassing the Docker proxy. Closing that requires
//! a connect wall the launcher does not yet apply (a seccomp user-notification
//! filter, or the cgroup connect_unix eBPF program); until then the proxy is the
//! routed path, not an enforced one.

use std::path::{Path, PathBuf};

use anyhow::{Context, Result};
use landlock::{
    path_beneath_rules, Access, AccessFs, CompatLevel, Compatible, Ruleset, RulesetAttr,
    RulesetCreatedAttr, RulesetStatus, ABI,
};

/// The Landlock ABI this host supports (measured: ABI 4 on kernel 6.8).
const SUPPORTED_ABI: ABI = ABI::V4;

/// Keeps only the paths that exist, so a missing allow-list entry is skipped
/// rather than failing the whole ruleset; the launcher logs what it skipped.
pub fn existing(paths: &[PathBuf]) -> (Vec<PathBuf>, Vec<PathBuf>) {
    paths
        .iter()
        .cloned()
        .partition(|path| path.exists())
}

/// Restricts the current process to the given paths and returns whether the
/// kernel enforced the ruleset fully, partially or not at all. The caller is
/// responsible for having already set `no_new_privs`.
pub fn restrict(read_only: &[PathBuf], read_write: &[PathBuf]) -> Result<RulesetStatus> {
    let status = Ruleset::default()
        .set_compatibility(CompatLevel::BestEffort)
        .handle_access(AccessFs::from_all(SUPPORTED_ABI))
        .context("declare the Landlock access set")?
        .create()
        .context("create the Landlock ruleset")?
        .add_rules(path_beneath_rules(read_only, AccessFs::from_read(SUPPORTED_ABI)))
        .context("add the read-only rules")?
        .add_rules(path_beneath_rules(read_write, AccessFs::from_all(SUPPORTED_ABI)))
        .context("add the read-write rules")?
        .restrict_self()
        .context("apply the Landlock ruleset")?;
    Ok(status.ruleset)
}

/// Builds and adds the rules over a ruleset without restricting the current
/// process, so a test can exercise rule construction without confining the
/// shared test runner. Returns an error if a path cannot be turned into a rule.
pub fn check_rules(read_only: &[PathBuf], read_write: &[PathBuf]) -> Result<()> {
    let _created = Ruleset::default()
        .set_compatibility(CompatLevel::BestEffort)
        .handle_access(AccessFs::from_all(SUPPORTED_ABI))
        .context("declare the Landlock access set")?
        .create()
        .context("create the Landlock ruleset")?
        .add_rules(path_beneath_rules(read_only, AccessFs::from_read(SUPPORTED_ABI)))
        .context("add the read-only rules")?
        .add_rules(path_beneath_rules(read_write, AccessFs::from_all(SUPPORTED_ABI)))
        .context("add the read-write rules")?;
    Ok(())
}

/// True when `path` is beneath, or equal to, one of `roots`: the test the
/// launcher's allow-list expresses and the predicate a reviewer can check a
/// policy against.
pub fn is_within(path: &Path, roots: &[PathBuf]) -> bool {
    roots.iter().any(|root| path == root || path.starts_with(root))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn read_only_and_read_write_differ() {
        assert_ne!(AccessFs::from_read(SUPPORTED_ABI), AccessFs::from_all(SUPPORTED_ABI));
    }

    #[test]
    fn existing_splits_present_from_absent() {
        let dir = tempfile::tempdir().unwrap();
        let present = dir.path().to_path_buf();
        let absent = dir.path().join("missing");
        let (there, not) = existing(&[present.clone(), absent.clone()]);
        assert_eq!(there, vec![present]);
        assert_eq!(not, vec![absent]);
    }

    #[test]
    fn check_rules_accepts_existing_paths() {
        let dir = tempfile::tempdir().unwrap();
        let worktree = dir.path().join("worktree");
        std::fs::create_dir(&worktree).unwrap();
        check_rules(&[dir.path().to_path_buf()], &[worktree]).unwrap();
    }

    #[test]
    fn is_within_matches_subpaths_only() {
        let roots = vec![PathBuf::from("/run/s1/worktree")];
        assert!(is_within(Path::new("/run/s1/worktree"), &roots));
        assert!(is_within(Path::new("/run/s1/worktree/src/main.go"), &roots));
        assert!(!is_within(Path::new("/run/s2/worktree"), &roots));
        assert!(!is_within(Path::new("/var/run/docker.sock"), &roots));
    }
}
