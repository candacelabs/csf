// Copyright 2026 Candace Labs

//! The CSF per-session sandbox: the library behind the session launcher and
//! the Docker proxy.
//!
//! A harness session runs as the operator's user, which on this host is in the
//! `docker`, `sudo` and `lxd` groups. Left unconfined a session is effectively
//! root on the machine. This crate confines one session's turn executor to:
//!
//!   - a per-session cgroup with cpu, memory and pids limits, so one session
//!     cannot starve the host and so cancel can kill the whole subtree with one
//!     write to `cgroup.kill` ([`cgroup`]);
//!   - a Landlock filesystem view that grants the session's own worktree, run
//!     directory and caches and nothing else, so the rest of the home and other
//!     sessions' directories cannot be opened ([`landlock_rules`]);
//!   - a seccomp policy that denies the syscalls a confined session has no
//!     business making ([`seccomp`]);
//!   - its Docker access routed through a filtering proxy (via `DOCKER_HOST`)
//!     that forwards a fixed allow-list of calls and refuses anything that would
//!     widen the session's authority ([`dockerfilter`]).
//!
//! One wall is not yet in place: nothing stops a confined process from
//! `connect(2)`-ing directly to the Docker or lxd socket and bypassing the
//! proxy. Landlock does not cover Unix-socket connect, and the enforcing layer
//! (a seccomp user-notification connect filter, or the cgroup connect_unix eBPF
//! program) is a follow-up. Until it lands the proxy is the routed path, not an
//! enforced boundary.
//!
//! The policy for one session is [`policy::SessionPolicy`], read from a JSON
//! file the harness writes. The launcher enforces it and execs the executor;
//! nothing here starts a session on its own.

pub mod cgroup;
pub mod dockerfilter;
pub mod landlock_rules;
pub mod policy;
pub mod seccomp;
