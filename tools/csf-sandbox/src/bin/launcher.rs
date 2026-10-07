// Copyright 2026 Candace Labs

//! `csf-session-launcher`: the per-session sandbox entry point.
//!
//! The harness places this binary in front of a session's turn executor as an
//! argv prefix, through ipc/proc:
//!
//! ```text
//! csf-session-launcher --policy <file> -- <executor> <executor args...>
//! ```
//!
//! It is single-threaded. Given the policy it moves itself into the session's
//! cgroup and writes the limits, sets `no_new_privs`, applies the Landlock
//! filesystem view and the seccomp policy, then execs the executor. Because the
//! launcher execs rather than forks, the executor and everything it starts stay
//! in the session's cgroup and under the same sandbox, so cancel can kill the
//! whole subtree with one write to `cgroup.kill`.

use std::os::unix::process::CommandExt;
use std::path::PathBuf;
use std::process::Command;

use anyhow::{bail, Context, Result};
use clap::Parser;

use csf_sandbox::{cgroup, landlock_rules, policy::SessionPolicy, seccomp};

#[derive(Parser)]
#[command(name = "csf-session-launcher", about = "Confine and exec one harness session's turn executor")]
struct Args {
    /// The JSON sandbox policy for this session.
    #[arg(long)]
    policy: PathBuf,
    /// The executor command and its arguments, after `--`.
    #[arg(last = true, required = true)]
    executor: Vec<String>,
}

fn main() {
    if let Err(error) = run() {
        eprintln!("csf-session-launcher: {error:#}");
        std::process::exit(1);
    }
}

fn run() -> Result<()> {
    let args = Args::parse();
    let policy = SessionPolicy::load(&args.policy)?;
    if args.executor.is_empty() {
        bail!("no executor command after --");
    }

    // 1. Resource bounds. Join first, so the limits and every later child bind
    //    this process; write the limits the harness derived.
    cgroup::join(&policy.cgroup)?;
    cgroup::write_limits(&policy.cgroup, &policy.limits)?;

    // 2. No new privileges: required before Landlock and seccomp may be applied
    //    without a capability, and it closes setuid escalation for every child.
    nix::sys::prctl::set_no_new_privs().context("set no_new_privs")?;

    // 3. Filesystem view. Non-existent allow-list entries are skipped rather
    //    than failing the whole ruleset.
    let (read_only, missing_ro) = landlock_rules::existing(&policy.read_only_paths);
    let (read_write, missing_rw) = landlock_rules::existing(&policy.read_write_paths);
    for path in missing_ro.iter().chain(missing_rw.iter()) {
        eprintln!("csf-session-launcher: allow-list path {} does not exist; skipped", path.display());
    }
    let status = landlock_rules::restrict(&read_only, &read_write)?;
    eprintln!("csf-session-launcher: landlock {status:?}");

    // 4. Syscall policy. Applied last of the confinements, so nothing above
    //    trips its own denials.
    let program = seccomp::program()?;
    seccompiler::apply_filter(&program).context("apply the seccomp filter")?;

    // 5. Exec the executor. It inherits the cgroup, the Landlock view, the
    //    seccomp filter and no_new_privs across the exec.
    let mut command = Command::new(&args.executor[0]);
    command.args(&args.executor[1..]);
    if let Some(socket) = &policy.docker_proxy_socket {
        command.env("DOCKER_HOST", format!("unix://{}", socket.display()));
    }
    let error = command.exec();
    Err(error).with_context(|| format!("exec {}", args.executor[0]))
}
