package config

// Environment-variable names recognized by applyEnv, in the same precedence
// tier as the README's configuration table (env beats file beats defaults).
// These are the single source of truth for the variable names: applyEnv reads
// them and the package's own tests set them. config_contract_test.go re-states
// each name as an independent literal golden, so a rename here surfaces there.
const (
	envNodeID   = "WARDEN_NODE_ID"
	envBind     = "WARDEN_BIND"
	envDataDir  = "WARDEN_DATA_DIR"
	envLogLevel = "WARDEN_LOG_LEVEL"
	envPeers    = "WARDEN_PEERS"

	envHeartbeatInterval  = "WARDEN_HEARTBEAT_INTERVAL"
	envSuspectAfter       = "WARDEN_SUSPECT_AFTER"
	envDeadAfter          = "WARDEN_DEAD_AFTER"
	envElectionTimeoutMin = "WARDEN_ELECTION_TIMEOUT_MIN"
	envElectionTimeoutMax = "WARDEN_ELECTION_TIMEOUT_MAX"
	envRPCTimeout         = "WARDEN_RPC_TIMEOUT"

	envCooldown       = "WARDEN_COOLDOWN"
	envNotifyRecovery = "WARDEN_NOTIFY_RECOVERY"
	envNotifyMode     = "WARDEN_NOTIFY_MODE"
	envNotifyFile     = "WARDEN_NOTIFY_FILE"

	envSMTPHost = "SMTP_HOST"
	envSMTPPort = "SMTP_PORT"
	envSMTPUser = "SMTP_USER"
	envSMTPPass = "SMTP_PASS"
	envSMTPFrom = "SMTP_FROM"
	envSMTPTo   = "SMTP_TO"

	envDiscoveryMode    = "WARDEN_DISCOVERY_MODE"
	envClusterID        = "WARDEN_CLUSTER_ID"
	envJoinStability    = "WARDEN_JOIN_STABILITY"
	envRemoveAfter      = "WARDEN_REMOVE_AFTER"
	envDiscoveryFile    = "WARDEN_DISCOVERY_FILE"
	envFilePollInterval = "WARDEN_FILE_POLL_INTERVAL"

	envTSSocket       = "WARDEN_TS_SOCKET"
	envTSTag          = "WARDEN_TS_TAG"
	envTSHostPattern  = "WARDEN_TS_HOST_PATTERN"
	envTSPollInterval = "WARDEN_TS_POLL_INTERVAL"

	envAdvertiseAddr = "WARDEN_ADVERTISE_ADDR"
)
