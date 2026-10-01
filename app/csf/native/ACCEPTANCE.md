# Native payload acceptance

The lightweight native payload path was exercised on 2026-09-18 at source
commit `37fe9fdbef8edbf1e7e79a28dfd1503cd195362a`, target
`debian-12-linux-amd64`. This is an observed receipt, not a golden hash for
later source revisions.

| Evidence | Observed value |
|---|---|
| CSF source tree | `git-tree:32a0522b36f7c54463a284438bf2ba47b34d4035` |
| CSF binary SHA-256 | `6e17d6268bb772bcc31c870643c79b3b1f04078b0e776d27c5d088982f963d3e` |
| CSF payload receipt SHA-256 | `92235f6d27724661f9632305fae46562f150f2fd4b9738072a8ddd064a0df6fa` |
| Redis source SHA-256 | `4ddebbf09061cbb589011786febdb34f29767dd7f89dbe712d2b68e808af6a1f` |
| Redis binary SHA-256 | `d32f133d7195ebce6b648d0787d0342372f136e9557cf579d403b1fd16566433` |
| Redis payload receipt SHA-256 | `faba541d1a1d0a3a3d47332eeb6cc4499b4d3bf7d716e87aa26dee9c5a6bec6e` |
| CSF + Redis archive SHA-256 | `6aad70679a7c5a7d42dbbf5d02d268017d994e6cd02512754d20de40f291f111` |

The two payloads were built with `build-payload.sh`. The CSF build fetched the
checksum-locked module graph, verified it, and compiled with networking
disabled. Two assemblies with reversed request order were byte-identical. The
embedded receipt covered 30 files, including the CSF and Redis systemd service
destinations and Redis's internal relative symlinks.

The CSF binary started the minimal in-memory server in the pinned Debian 12
container with networking disabled and reported its loopback listener. The
Redis binary reported version 7.4.2 in the same Debian baseline after installing
the documented `libssl3` runtime dependency. No host unit was installed or
started.

This run did not build or boot PostgreSQL, OpenSearch, ClickHouse, Langfuse, or
MinIO. It did not exercise an AWS account, S3 authorization, systemd first boot,
or Langfuse migrations. Those boundaries require separate acceptance on a
builder or target host with the selected configuration and credentials.
