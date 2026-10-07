// Copyright 2026 Candace Labs

//! The seccomp policy applied to a confined session.
//!
//! It is a denylist: every syscall is allowed except the ones a sandboxed
//! session has no legitimate use for and that would let it inspect or escape
//! its confinement. A denied syscall returns `EPERM` rather than killing the
//! process, so an incidental call fails cleanly instead of taking the executor
//! down. The seccomp filter is compiled with the `seccompiler` crate; the raw
//! BPF is never hand-assembled.

use std::collections::BTreeMap;

use anyhow::{Context, Result};
use seccompiler::{BpfProgram, SeccompAction, SeccompFilter};

/// The syscalls the sandbox denies, each with its number on this build's
/// architecture. The list is exactly the operator's: debugging and
/// cross-process memory access (`ptrace`, `process_vm_readv`,
/// `process_vm_writev`), namespace and mount manipulation (`mount`, `unshare`,
/// `setns`), the BPF and perf interfaces (`bpf`, `perf_event_open`), kernel
/// replacement (`kexec_load`, `kexec_file_load`) and the key retention service
/// (`keyctl`).
pub fn denied() -> Vec<(&'static str, i64)> {
    vec![
        ("ptrace", libc::SYS_ptrace as i64),
        ("process_vm_readv", libc::SYS_process_vm_readv as i64),
        ("process_vm_writev", libc::SYS_process_vm_writev as i64),
        ("mount", libc::SYS_mount as i64),
        ("bpf", libc::SYS_bpf as i64),
        ("unshare", libc::SYS_unshare as i64),
        ("setns", libc::SYS_setns as i64),
        ("perf_event_open", libc::SYS_perf_event_open as i64),
        ("kexec_load", libc::SYS_kexec_load as i64),
        ("kexec_file_load", libc::SYS_kexec_file_load as i64),
        ("keyctl", libc::SYS_keyctl as i64),
    ]
}

#[cfg(target_arch = "x86_64")]
const TARGET_ARCH: seccompiler::TargetArch = seccompiler::TargetArch::x86_64;
#[cfg(target_arch = "aarch64")]
const TARGET_ARCH: seccompiler::TargetArch = seccompiler::TargetArch::aarch64;

/// Compiles the denylist into a BPF program ready for
/// [`seccompiler::apply_filter`]. Every denied syscall gets an unconditional
/// rule (an empty rule set matches any arguments); everything else falls
/// through to the mismatch action, which is to allow.
pub fn program() -> Result<BpfProgram> {
    let rules = denied()
        .into_iter()
        .map(|(_, number)| (number, Vec::new()))
        .collect::<BTreeMap<_, _>>();
    let filter = SeccompFilter::new(
        rules,
        SeccompAction::Allow,
        SeccompAction::Errno(libc::EPERM as u32),
        TARGET_ARCH,
    )
    .context("build the seccomp filter")?;
    let program: BpfProgram = filter.try_into().context("compile the seccomp filter")?;
    Ok(program)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn denies_every_operator_named_syscall() {
        let names: Vec<&str> = denied().into_iter().map(|(name, _)| name).collect();
        for expected in [
            "ptrace",
            "process_vm_readv",
            "process_vm_writev",
            "mount",
            "bpf",
            "unshare",
            "setns",
            "perf_event_open",
            "kexec_load",
            "kexec_file_load",
            "keyctl",
        ] {
            assert!(names.contains(&expected), "{expected} must be denied");
        }
    }

    #[test]
    fn syscall_numbers_are_distinct() {
        let mut numbers: Vec<i64> = denied().into_iter().map(|(_, number)| number).collect();
        let before = numbers.len();
        numbers.sort_unstable();
        numbers.dedup();
        assert_eq!(before, numbers.len(), "no syscall is listed twice");
    }

    #[test]
    fn compiles_to_a_non_empty_program() {
        let program = program().unwrap();
        assert!(!program.is_empty());
    }
}
