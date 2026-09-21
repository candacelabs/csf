# Deploying warden to a fleet (systemd)

warden runs as one identical binary on every fleet node. Nodes differ only in
`node_id`. This directory holds the systemd unit (`warden.service`); the steps
below build the binary once and install it on all four nodes.

## Fleet

The table below is the shape of a fleet, not a real one: substitute your own
node ids, tailnet addresses and login users. The addresses are documentation
range (RFC 5737 TEST-NET-3).

| node_id  | tailnet IP     | login user | notes              |
| -------- | -------------- | ---------- | ------------------ |
| `node-a` | `203.0.113.11` | `deploy`   | hypervisor host    |
| `node-b` | `203.0.113.12` | `deploy`   | application host   |
| `node-c` | `203.0.113.13` | `deploy`   | application host   |
| `node-d` | `203.0.113.14` | `deploy`   | edge / proxy host  |

All four listen on `:7717`. warden speaks HTTP/JSON over the tailnet, so every
node must be able to reach every other node's `<tailnet-ip>:7717`.

## 1. Build once (static linux/amd64 binary)

From the Go module root — this repository's root — on any build machine:

```bash
VERSION="$(git describe --tags --always --dirty)"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
  -o /tmp/warden ./app/warden/cmd

/tmp/warden -version   # sanity check: prints the version string
```

The binary is CGO-free and statically linked, so it runs on any of the fleet
nodes without shared-library concerns.

## 2. Per-node config

Each node gets:

- `/etc/warden/warden.yaml` — identical everywhere **except `node_id`**. Start
  from [`../warden.example.yaml`](../warden.example.yaml) and replace its
  example peer list with your fleet's.
- `/etc/warden/warden.env` — optional; the place for secrets like `SMTP_PASS`
  so they stay out of the world-readable YAML. See
  [`../.env.example`](../.env.example).

`node_id` may live in the YAML *or* be supplied as `WARDEN_NODE_ID` in the env
file (env overrides YAML), which lets you ship a single byte-identical
`warden.yaml` to every node and set only the env file per host.

## 3. Install on each node

Run this block once per node, substituting the login user and the node_id.
Example shown for `node-c`:

```bash
NODE=node-c
USER_AT_HOST=deploy@203.0.113.13

# binary
scp /tmp/warden "${USER_AT_HOST}:/tmp/warden"
ssh "${USER_AT_HOST}" 'sudo install -o root -g root -m 0755 /tmp/warden /usr/local/bin/warden'

# config dir + files
ssh "${USER_AT_HOST}" 'sudo mkdir -p /etc/warden'
scp candace/app/warden/warden.example.yaml "${USER_AT_HOST}:/tmp/warden.yaml"
# set this node's id in the copy before installing it:
ssh "${USER_AT_HOST}" "sudo sh -c 'sed \"s/^node_id:.*/node_id: ${NODE}/\" /tmp/warden.yaml > /etc/warden/warden.yaml && chmod 0644 /etc/warden/warden.yaml'"

# optional secrets (SMTP app password etc.)
scp candace/app/warden/.env.example "${USER_AT_HOST}:/tmp/warden.env"
ssh "${USER_AT_HOST}" 'sudo install -m 0600 /tmp/warden.env /etc/warden/warden.env'   # then edit it

# unit
scp candace/app/warden/deploy/warden.service "${USER_AT_HOST}:/tmp/warden.service"
ssh "${USER_AT_HOST}" 'sudo install -m 0644 /tmp/warden.service /etc/systemd/system/warden.service'

# enable + start
ssh "${USER_AT_HOST}" 'sudo systemctl daemon-reload && sudo systemctl enable --now warden'
```

Repeat for every remaining node, substituting its login user, tailnet address
and node_id: `node-a` (`203.0.113.11`), `node-b` (`203.0.113.12`) and `node-d`
(`203.0.113.14`).

> The unit uses `DynamicUser=yes` + `StateDirectory=warden`, so systemd creates
> and owns `/var/lib/warden` (0700) on each start — no manual user/dir setup.

## 4. Verify

On any node (or over the tailnet):

```bash
# service health
ssh "${USER_AT_HOST}" 'systemctl status warden --no-pager'
ssh "${USER_AT_HOST}" 'journalctl -u warden -n 50 --no-pager'

# cluster state as JSON (run against each node's tailnet IP)
curl -s http://203.0.113.13:7717/api/status | jq .

# metrics (leader flag, term, per-peer status/latency)
curl -s http://203.0.113.13:7717/metrics | grep -E '^warden_'

# dashboard (browser, over the tailnet)
#   http://203.0.113.13:7717/
```

A healthy fleet shows exactly one node with `warden_is_leader 1`, all peers
`warden_peer_up 1`, and the same authoritative `ClusterView` on every node's
`/api/status` (`"authoritative": true` on non-leaders means they are receiving
the leader's piggybacked view).

## 5. Update

```bash
# rebuild (step 1), then per node:
scp /tmp/warden "${USER_AT_HOST}:/tmp/warden"
ssh "${USER_AT_HOST}" 'sudo install -m 0755 /tmp/warden /usr/local/bin/warden && sudo systemctl restart warden'
```

Restarting one node at a time is safe: the fleet re-elects around it (a single
node down is a minority; majority quorum holds).

## 6. Dynamic membership (optional): discovery

By default warden runs in `discovery.mode: static` — membership is exactly the
`peers` seed and only changes when you edit config and roll the fleet (steps
2–5). That is the right default for a fixed four-node fleet. Two dynamic modes
let nodes be discovered and admitted at runtime without editing every node's
config. In **both** dynamic modes the `peers` seed is still required (warden
boots from it), and **quorum is always over the persisted voter set** — see the
[Membership & discovery](../README.md#membership--discovery) section for the
full model and safety argument.

### tailscale mode

warden already dials peers over the tailnet, so it can also *find* them there.
Advertise a shared ACL tag on every warden node and let each warden select
peers carrying that tag.

1. Grant the tag an owner in your tailnet ACL (Tailscale admin → Access
   controls), e.g.:

   ```jsonc
   "tagOwners": {
     "tag:candacenet": ["autogroup:admin"]
   }
   ```

2. Advertise the tag from each node (requires re-auth/approval the first time):

   ```bash
   sudo tailscale up --advertise-tags=tag:candacenet
   ```

3. Turn on tailscale discovery in each node's env (or YAML):

   ```bash
   # /etc/warden/warden.env
   WARDEN_DISCOVERY_MODE=tailscale
   WARDEN_TS_TAG=tag:candacenet
   # WARDEN_TS_SOCKET defaults to /var/run/tailscale/tailscaled.sock
   ```

   The warden process must be able to read the tailscaled socket. Note the
   hardened unit already allows `AF_UNIX` (`RestrictAddressFamilies`) so the
   LocalAPI call works; if you tightened `ReadOnlyPaths`/sandboxing further,
   make sure the socket path stays reachable.

**Hostname-pattern alternative.** If you would rather not manage a tag, match on
hostname instead (anchored RE2 against each peer's tailscale HostName):

```bash
WARDEN_DISCOVERY_MODE=tailscale
WARDEN_TS_HOST_PATTERN=warden-.*        # e.g. hosts named warden-1, warden-2, …
```

Tag and pattern can be combined (a peer matching **either** is selected).

### file mode (manual dynamic / tooling-driven)

Point warden at a JSON roster file it re-reads on a poll; edit the file (by hand
or from a provisioning tool) to add/remove candidates without restarting:

```bash
WARDEN_DISCOVERY_MODE=file
WARDEN_DISCOVERY_FILE=/var/lib/warden/roster.json
```

```json
{ "nodes": [
  { "id": "node-c", "addr": "203.0.113.13:7717" },
  { "id": "node-d", "addr": "203.0.113.14:7717" }
] }
```

A missing or malformed file is ignored (warden keeps its last roster); a valid
empty `{"nodes":[]}` is honored as an empty roster.

### Adding a brand-new node (joiner)

A new node ships the **same** config as the fleet: the same `peers` seed with
its own `node_id` simply not in it. Because it is not in the seed, tell the
fleet where to reach it:

```bash
# on the joiner, /etc/warden/warden.env
WARDEN_NODE_ID=node-e
WARDEN_ADVERTISE_ADDR=203.0.113.15:7717     # this node's routable ip:port
WARDEN_DISCOVERY_MODE=tailscale
WARDEN_TS_TAG=tag:candacenet
```

It starts as an **observer** (no vote), the leader verifies it via identify, and
after `WARDEN_JOIN_STABILITY` (default 30s) admits it as a voter. No change is
needed on the existing nodes.

### Verify discovery

```bash
# membership + per-node kind (voter / observer / discovered)
curl -s http://203.0.113.13:7717/api/status | jq '{membership, peers: [.peers[] | {id: .node.id, member, status}]}'
```

Watch a joiner move `discovered → observer → voter`, and confirm
`membership.version` bumps by one per admission. The dashboard shows the same
transitions. The leader logs each committed change; discovery-source failures
log a single rate-limited warning and change nothing.
