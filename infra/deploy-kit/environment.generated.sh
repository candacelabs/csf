# Code generated from Candacefile by tools/candace_environment.py; DO NOT EDIT.
# Regenerate with: python3 tools/candace_environment.py write

readonly deploy_env_postgres_database=POSTGRES_DB
readonly deploy_default_postgres_database=deploy
readonly deploy_env_postgres_user=POSTGRES_USER
readonly deploy_default_postgres_user=deploy
readonly deploy_env_postgres_password=POSTGRES_PASSWORD
readonly deploy_env_state_root=DEPLOY_STATE_ROOT
readonly deploy_env_uid=DEPLOY_UID
readonly deploy_env_gid=DEPLOY_GID
readonly deploy_env_host_workspace=DEPLOY_HOST_WORKSPACE
readonly deploy_env_agent_token=NODEEXEC_TOKEN
readonly deploy_env_copilot_connection_token=DEPLOY_COPILOT_CONNECTION_TOKEN
readonly deploy_env_opencode_password=DEPLOY_OPENCODE_PASSWORD
readonly deploy_env_copilot_github_token=COPILOT_GITHUB_TOKEN
readonly deploy_default_copilot_github_token=''
readonly deploy_env_opencode_model=DEPLOY_OPENCODE_MODEL
readonly deploy_default_opencode_model=''
readonly deploy_env_agent_revision_max_entries=NODEEXEC_REVISION_MAX_ENTRIES
readonly deploy_default_agent_revision_max_entries=128
readonly deploy_env_agent_revision_max_bytes=NODEEXEC_REVISION_MAX_BYTES
readonly deploy_default_agent_revision_max_bytes=4294967296
readonly deploy_env_copilot_bin=DEPLOY_COPILOT_BIN
readonly deploy_env_copilot_sha256=DEPLOY_COPILOT_SHA256
readonly deploy_env_gh_token=GH_TOKEN
readonly deploy_env_github_token=GITHUB_TOKEN
readonly deploy_env_live_confirm=DEPLOY_LIVE_CONFIRM
readonly deploy_env_legacy_mode=DEPLOY_MODE
readonly deploy_env_openai_api_key=OPENAI_API_KEY
readonly deploy_env_anthropic_api_key=ANTHROPIC_API_KEY
readonly deploy_env_openrouter_api_key=OPENROUTER_API_KEY
readonly deploy_env_agent_bind=NODEEXEC_BIND
readonly deploy_default_agent_bind=0.0.0.0:8094
readonly deploy_env_agent_node_id=NODEEXEC_NODE_ID
readonly deploy_default_agent_node_id=deploy-demo
readonly deploy_env_agent_state_file=NODEEXEC_STATE_FILE
readonly deploy_default_agent_state_file=/var/lib/nodeexec/state.json
readonly deploy_env_agent_revision_root=NODEEXEC_REVISION_ROOT
readonly deploy_default_agent_revision_root=/var/lib/nodeexec/revisions
readonly deploy_env_docker_config=DOCKER_CONFIG
readonly deploy_default_docker_config=/tmp/docker-config
readonly deploy_env_agent_dry_workspace=NODEEXEC_DRY_WORKSPACE
readonly deploy_default_agent_dry_workspace=/workspace
readonly deploy_env_agent_live_workspace=NODEEXEC_LIVE_WORKSPACE
readonly deploy_default_agent_live_workspace=/workspace
readonly deploy_env_agent_dry_run_enabled=NODEEXEC_DRY_RUN_ENABLED
readonly deploy_default_agent_dry_run_enabled=true
readonly deploy_env_agent_dry_run_disabled=NODEEXEC_DRY_RUN_DISABLED
readonly deploy_default_agent_dry_run_disabled=false
readonly deploy_env_warden_config=WARDEN_CONFIG
readonly deploy_default_warden_config=/etc/warden/warden.yaml
readonly deploy_env_warden_log_format=WARDEN_LOG_FORMAT
readonly deploy_default_warden_log_format=console
readonly deploy_env_copilot_home=DEPLOY_COPILOT_HOME
readonly deploy_default_copilot_home=/var/lib/copilot
readonly deploy_env_harness_backend=DEPLOY_HARNESS_BACKEND
readonly deploy_default_harness_backend=copilot-cli
readonly deploy_env_core_bind=DEPLOY_BIND
readonly deploy_default_core_bind=0.0.0.0:7780
readonly deploy_env_core_data_dir=DEPLOY_DATA_DIR
readonly deploy_default_core_data_dir=/var/lib/deploy
readonly deploy_env_core_workspace=DEPLOY_WORKSPACE
readonly deploy_default_core_workspace=/workspace
readonly deploy_env_core_database_url=DEPLOY_DATABASE_URL
readonly deploy_default_core_database_url=''
readonly deploy_env_core_warden_url=DEPLOY_WARDEN_URL
readonly deploy_default_core_warden_url=http://127.0.0.1:7717
readonly deploy_env_core_agent_url=NODEEXEC_URL
readonly deploy_default_core_agent_url=''
readonly deploy_env_core_agent_port=NODEEXEC_PORT
readonly deploy_default_core_agent_port=8094
readonly deploy_env_core_node_labels=DEPLOY_NODE_LABELS
readonly deploy_default_core_node_labels='{}'
readonly deploy_env_core_approval_timeout=DEPLOY_APPROVAL_TIMEOUT
readonly deploy_default_core_approval_timeout=15m
readonly deploy_env_core_fleet_poll_interval=DEPLOY_FLEET_POLL_INTERVAL
readonly deploy_default_core_fleet_poll_interval=2s
readonly deploy_env_copilot_cli=DEPLOY_COPILOT_CLI
readonly deploy_default_copilot_cli=/usr/local/bin/copilot
readonly deploy_env_copilot_url=DEPLOY_COPILOT_URL
readonly deploy_default_copilot_url=''
readonly deploy_env_copilot_model=DEPLOY_COPILOT_MODEL
readonly deploy_default_copilot_model=gpt-5.4
readonly deploy_env_ollama_url=DEPLOY_OLLAMA_URL
readonly deploy_default_ollama_url=''
readonly deploy_env_ollama_model=DEPLOY_OLLAMA_MODEL
readonly deploy_default_ollama_model=''
readonly deploy_env_ollama_model_digest=DEPLOY_OLLAMA_MODEL_DIGEST
readonly deploy_default_ollama_model_digest=''
readonly deploy_env_ollama_context_tokens=DEPLOY_OLLAMA_CONTEXT_TOKENS
readonly deploy_default_ollama_context_tokens=16384
readonly deploy_env_ollama_max_tool_calls=DEPLOY_OLLAMA_MAX_TOOL_CALLS
readonly deploy_default_ollama_max_tool_calls=16
readonly deploy_env_ollama_turn_timeout=DEPLOY_OLLAMA_TURN_TIMEOUT
readonly deploy_default_ollama_turn_timeout=10m
readonly deploy_env_opencode_url=DEPLOY_OPENCODE_URL
readonly deploy_default_opencode_url=http://127.0.0.1:4096
readonly deploy_env_opencode_username=DEPLOY_OPENCODE_USERNAME
readonly deploy_default_opencode_username=opencode
readonly deploy_env_opencode_session_id=DEPLOY_OPENCODE_SESSION_ID
readonly deploy_default_opencode_session_id=''
readonly deploy_env_opencode_request_timeout=DEPLOY_OPENCODE_REQUEST_TIMEOUT
readonly deploy_default_opencode_request_timeout=10s
readonly deploy_env_opencode_poll_interval=DEPLOY_OPENCODE_POLL_INTERVAL
readonly deploy_default_opencode_poll_interval=1s
readonly deploy_env_opencode_queue_capacity=DEPLOY_OPENCODE_QUEUE_CAPACITY
readonly deploy_default_opencode_queue_capacity=32
readonly deploy_env_live_confirm_phrase=DEPLOY_LIVE_CONFIRM_PHRASE
readonly deploy_default_live_confirm_phrase=I_UNDERSTAND_DOCKER_SOCKET_IS_ROOT
readonly deploy_profile_local=local
readonly deploy_profile_demo=demo
readonly deploy_profile_copilot=copilot
readonly deploy_profile_opencode=opencode

declare -gra deploy_environment_state_symbols=(
  postgres_password
  state_root
  uid
  gid
  host_workspace
  agent_token
  copilot_connection_token
  opencode_password
  copilot_github_token
  opencode_model
  agent_revision_max_entries
  agent_revision_max_bytes
)
declare -grA deploy_environment_names=(
  [postgres_database]=POSTGRES_DB
  [postgres_user]=POSTGRES_USER
  [postgres_password]=POSTGRES_PASSWORD
  [state_root]=DEPLOY_STATE_ROOT
  [uid]=DEPLOY_UID
  [gid]=DEPLOY_GID
  [host_workspace]=DEPLOY_HOST_WORKSPACE
  [agent_token]=NODEEXEC_TOKEN
  [copilot_connection_token]=DEPLOY_COPILOT_CONNECTION_TOKEN
  [opencode_password]=DEPLOY_OPENCODE_PASSWORD
  [copilot_github_token]=COPILOT_GITHUB_TOKEN
  [opencode_model]=DEPLOY_OPENCODE_MODEL
  [agent_revision_max_entries]=NODEEXEC_REVISION_MAX_ENTRIES
  [agent_revision_max_bytes]=NODEEXEC_REVISION_MAX_BYTES
  [copilot_bin]=DEPLOY_COPILOT_BIN
  [copilot_sha256]=DEPLOY_COPILOT_SHA256
  [gh_token]=GH_TOKEN
  [github_token]=GITHUB_TOKEN
  [live_confirm]=DEPLOY_LIVE_CONFIRM
  [legacy_mode]=DEPLOY_MODE
  [openai_api_key]=OPENAI_API_KEY
  [anthropic_api_key]=ANTHROPIC_API_KEY
  [openrouter_api_key]=OPENROUTER_API_KEY
  [agent_bind]=NODEEXEC_BIND
  [agent_node_id]=NODEEXEC_NODE_ID
  [agent_state_file]=NODEEXEC_STATE_FILE
  [agent_revision_root]=NODEEXEC_REVISION_ROOT
  [docker_config]=DOCKER_CONFIG
  [agent_dry_workspace]=NODEEXEC_DRY_WORKSPACE
  [agent_live_workspace]=NODEEXEC_LIVE_WORKSPACE
  [agent_dry_run_enabled]=NODEEXEC_DRY_RUN_ENABLED
  [agent_dry_run_disabled]=NODEEXEC_DRY_RUN_DISABLED
  [warden_config]=WARDEN_CONFIG
  [warden_log_format]=WARDEN_LOG_FORMAT
  [copilot_home]=DEPLOY_COPILOT_HOME
  [harness_backend]=DEPLOY_HARNESS_BACKEND
  [core_bind]=DEPLOY_BIND
  [core_data_dir]=DEPLOY_DATA_DIR
  [core_workspace]=DEPLOY_WORKSPACE
  [core_database_url]=DEPLOY_DATABASE_URL
  [core_warden_url]=DEPLOY_WARDEN_URL
  [core_agent_url]=NODEEXEC_URL
  [core_agent_port]=NODEEXEC_PORT
  [core_node_labels]=DEPLOY_NODE_LABELS
  [core_approval_timeout]=DEPLOY_APPROVAL_TIMEOUT
  [core_fleet_poll_interval]=DEPLOY_FLEET_POLL_INTERVAL
  [copilot_cli]=DEPLOY_COPILOT_CLI
  [copilot_url]=DEPLOY_COPILOT_URL
  [copilot_model]=DEPLOY_COPILOT_MODEL
  [ollama_url]=DEPLOY_OLLAMA_URL
  [ollama_model]=DEPLOY_OLLAMA_MODEL
  [ollama_model_digest]=DEPLOY_OLLAMA_MODEL_DIGEST
  [ollama_context_tokens]=DEPLOY_OLLAMA_CONTEXT_TOKENS
  [ollama_max_tool_calls]=DEPLOY_OLLAMA_MAX_TOOL_CALLS
  [ollama_turn_timeout]=DEPLOY_OLLAMA_TURN_TIMEOUT
  [opencode_url]=DEPLOY_OPENCODE_URL
  [opencode_username]=DEPLOY_OPENCODE_USERNAME
  [opencode_session_id]=DEPLOY_OPENCODE_SESSION_ID
  [opencode_request_timeout]=DEPLOY_OPENCODE_REQUEST_TIMEOUT
  [opencode_poll_interval]=DEPLOY_OPENCODE_POLL_INTERVAL
  [opencode_queue_capacity]=DEPLOY_OPENCODE_QUEUE_CAPACITY
  [live_confirm_phrase]=DEPLOY_LIVE_CONFIRM_PHRASE
)
declare -grA deploy_environment_lifecycles=(
  [postgres_password]=secret
  [state_root]=host
  [uid]=host
  [gid]=host
  [host_workspace]=host
  [agent_token]=secret
  [copilot_connection_token]=secret
  [opencode_password]=secret
  [copilot_github_token]=operator
  [opencode_model]=operator
  [agent_revision_max_entries]=operator
  [agent_revision_max_bytes]=operator
)
declare -grA deploy_environment_values=(
  [postgres_password]=random_hex_32
  [state_root]=state_root
  [uid]=uid
  [gid]=gid
  [host_workspace]=apps_dir
  [agent_token]=random_hex_32
  [copilot_connection_token]=random_hex_32
  [opencode_password]=random_hex_32
  [copilot_github_token]=''
  [opencode_model]=''
  [agent_revision_max_entries]=128
  [agent_revision_max_bytes]=4294967296
)
declare -grA deploy_environment_required=(
  [postgres_password]=true
  [state_root]=true
  [uid]=true
  [gid]=true
  [host_workspace]=true
  [agent_token]=true
  [copilot_connection_token]=true
  [opencode_password]=true
  [copilot_github_token]=false
  [opencode_model]=false
  [agent_revision_max_entries]=true
  [agent_revision_max_bytes]=true
)
declare -grA deploy_environment_state_names=(
  [POSTGRES_PASSWORD]=postgres_password
  [DEPLOY_STATE_ROOT]=state_root
  [DEPLOY_UID]=uid
  [DEPLOY_GID]=gid
  [DEPLOY_HOST_WORKSPACE]=host_workspace
  [NODEEXEC_TOKEN]=agent_token
  [DEPLOY_COPILOT_CONNECTION_TOKEN]=copilot_connection_token
  [DEPLOY_OPENCODE_PASSWORD]=opencode_password
  [COPILOT_GITHUB_TOKEN]=copilot_github_token
  [DEPLOY_OPENCODE_MODEL]=opencode_model
  [NODEEXEC_REVISION_MAX_ENTRIES]=agent_revision_max_entries
  [NODEEXEC_REVISION_MAX_BYTES]=agent_revision_max_bytes
)

deploy_environment_reconcile() {
  local env_file=$1 state_root=$2 apps_dir=$3
  local line key symbol name lifecycle generator required value incoming temporary
  local -A existing=() seen=()

  [[ ! -L "$env_file" ]] || { printf 'environment file must not be a symbolic link: %s\n' "$env_file" >&2; return 1; }
  if [[ -f "$env_file" ]]; then
    [[ "$(stat -c '%a' "$env_file")" == 600 ]] || { printf 'environment file must have mode 600: %s\n' "$env_file" >&2; return 1; }
    while IFS= read -r line || [[ -n "$line" ]]; do
      [[ -z "$line" || "$line" == \#* ]] && continue
      [[ "$line" == *=* ]] || { printf 'malformed environment state in %s\n' "$env_file" >&2; return 1; }
      key=${line%%=*}
      [[ "$key" =~ ^[A-Z][A-Z0-9_]*$ ]] || { printf 'invalid environment name %s in %s\n' "$key" "$env_file" >&2; return 1; }
      [[ -z "${seen[$key]+present}" ]] || { printf 'duplicate environment name %s in %s\n' "$key" "$env_file" >&2; return 1; }
      seen[$key]=true
      if [[ -n "${deploy_environment_state_names[$key]+present}" ]]; then
        existing[$key]=${line#*=}
      fi
    done <"$env_file"
  fi

  command -v openssl >/dev/null || { printf 'openssl is required to generate local secrets\n' >&2; return 1; }
  umask 077
  temporary=$(mktemp "${env_file}.tmp.XXXXXX") || return 1
  : >"$temporary"
  for symbol in "${deploy_environment_state_symbols[@]}"; do
    name=${deploy_environment_names[$symbol]}
    lifecycle=${deploy_environment_lifecycles[$symbol]}
    generator=${deploy_environment_values[$symbol]}
    required=${deploy_environment_required[$symbol]}
    case "$lifecycle" in
      secret)
        value=${existing[$name]-}
        case "$generator" in
          random_hex_32)
            if [[ -z "$value" ]]; then
              value=$(openssl rand -hex 32) || { rm -f "$temporary"; printf 'could not generate %s\n' "$name" >&2; return 1; }
            fi
            if [[ ! "$value" =~ ^[0-9a-f]{64}$ ]]; then
              rm -f "$temporary"
              printf '%s is malformed in %s; expected 64 lowercase hexadecimal characters\n' "$name" "$env_file" >&2
              return 1
            fi
            ;;
          *) rm -f "$temporary"; printf 'unsupported secret generator %s\n' "$generator" >&2; return 1 ;;
        esac
        ;;
      host)
        case "$generator" in
          uid) value=$(id -u) ;;
          gid) value=$(id -g) ;;
          apps_dir) value=$apps_dir ;;
          state_root) value=$state_root ;;
          *) rm -f "$temporary"; printf 'unsupported host generator %s\n' "$generator" >&2; return 1 ;;
        esac
        ;;
      operator)
        incoming=${!name-}
        if [[ -n "$incoming" ]]; then
          value=$incoming
        elif [[ -n "${existing[$name]+present}" ]]; then
          value=${existing[$name]}
        else
          value=$generator
        fi
        ;;
      *) rm -f "$temporary"; printf 'unsupported environment lifecycle %s\n' "$lifecycle" >&2; return 1 ;;
    esac
    if [[ "$required" == true && -z "$value" ]]; then
      rm -f "$temporary"
      printf '%s is required by Candacefile\n' "$name" >&2
      return 1
    fi
    case "$value" in
      *[[:space:]]*|*'#'*|*'$'*|*'"'*|*"'"*|*$'\x5c'*)
        rm -f "$temporary"
        printf '%s contains characters that Docker Compose env files cannot represent safely\n' "$name" >&2
        return 1
        ;;
    esac
    printf '%s=%s\n' "$name" "$value" >>"$temporary"
    printf -v "$name" '%s' "$value"
    export "$name"
  done
  chmod 600 "$temporary"
  mv "$temporary" "$env_file"
}

deploy_environment_apply_defaults() {
  if [[ -z "${POSTGRES_DB:-}" ]]; then
    printf -v POSTGRES_DB %s deploy
    export POSTGRES_DB
  fi
  if [[ -z "${POSTGRES_USER:-}" ]]; then
    printf -v POSTGRES_USER %s deploy
    export POSTGRES_USER
  fi
  if [[ -z "${NODEEXEC_BIND:-}" ]]; then
    printf -v NODEEXEC_BIND %s 0.0.0.0:8094
    export NODEEXEC_BIND
  fi
  if [[ -z "${NODEEXEC_NODE_ID:-}" ]]; then
    printf -v NODEEXEC_NODE_ID %s deploy-demo
    export NODEEXEC_NODE_ID
  fi
  if [[ -z "${NODEEXEC_STATE_FILE:-}" ]]; then
    printf -v NODEEXEC_STATE_FILE %s /var/lib/nodeexec/state.json
    export NODEEXEC_STATE_FILE
  fi
  if [[ -z "${NODEEXEC_REVISION_ROOT:-}" ]]; then
    printf -v NODEEXEC_REVISION_ROOT %s /var/lib/nodeexec/revisions
    export NODEEXEC_REVISION_ROOT
  fi
  if [[ -z "${DOCKER_CONFIG:-}" ]]; then
    printf -v DOCKER_CONFIG %s /tmp/docker-config
    export DOCKER_CONFIG
  fi
  if [[ -z "${NODEEXEC_DRY_WORKSPACE:-}" ]]; then
    printf -v NODEEXEC_DRY_WORKSPACE %s /workspace
    export NODEEXEC_DRY_WORKSPACE
  fi
  if [[ -z "${NODEEXEC_LIVE_WORKSPACE:-}" ]]; then
    printf -v NODEEXEC_LIVE_WORKSPACE %s /workspace
    export NODEEXEC_LIVE_WORKSPACE
  fi
  if [[ -z "${NODEEXEC_DRY_RUN_ENABLED:-}" ]]; then
    printf -v NODEEXEC_DRY_RUN_ENABLED %s true
    export NODEEXEC_DRY_RUN_ENABLED
  fi
  if [[ -z "${NODEEXEC_DRY_RUN_DISABLED:-}" ]]; then
    printf -v NODEEXEC_DRY_RUN_DISABLED %s false
    export NODEEXEC_DRY_RUN_DISABLED
  fi
  if [[ -z "${WARDEN_CONFIG:-}" ]]; then
    printf -v WARDEN_CONFIG %s /etc/warden/warden.yaml
    export WARDEN_CONFIG
  fi
  if [[ -z "${WARDEN_LOG_FORMAT:-}" ]]; then
    printf -v WARDEN_LOG_FORMAT %s console
    export WARDEN_LOG_FORMAT
  fi
  if [[ -z "${DEPLOY_COPILOT_HOME:-}" ]]; then
    printf -v DEPLOY_COPILOT_HOME %s /var/lib/copilot
    export DEPLOY_COPILOT_HOME
  fi
  if [[ -z "${DEPLOY_HARNESS_BACKEND:-}" ]]; then
    printf -v DEPLOY_HARNESS_BACKEND %s copilot-cli
    export DEPLOY_HARNESS_BACKEND
  fi
  if [[ -z "${DEPLOY_BIND:-}" ]]; then
    printf -v DEPLOY_BIND %s 0.0.0.0:7780
    export DEPLOY_BIND
  fi
  if [[ -z "${DEPLOY_DATA_DIR:-}" ]]; then
    printf -v DEPLOY_DATA_DIR %s /var/lib/deploy
    export DEPLOY_DATA_DIR
  fi
  if [[ -z "${DEPLOY_WORKSPACE:-}" ]]; then
    printf -v DEPLOY_WORKSPACE %s /workspace
    export DEPLOY_WORKSPACE
  fi
  if [[ -z "${DEPLOY_DATABASE_URL:-}" ]]; then
    export DEPLOY_DATABASE_URL=''
  fi
  if [[ -z "${DEPLOY_WARDEN_URL:-}" ]]; then
    printf -v DEPLOY_WARDEN_URL %s http://127.0.0.1:7717
    export DEPLOY_WARDEN_URL
  fi
  if [[ -z "${NODEEXEC_URL:-}" ]]; then
    export NODEEXEC_URL=''
  fi
  if [[ -z "${NODEEXEC_PORT:-}" ]]; then
    printf -v NODEEXEC_PORT %s 8094
    export NODEEXEC_PORT
  fi
  if [[ -z "${DEPLOY_NODE_LABELS:-}" ]]; then
    printf -v DEPLOY_NODE_LABELS %s '{}'
    export DEPLOY_NODE_LABELS
  fi
  if [[ -z "${DEPLOY_APPROVAL_TIMEOUT:-}" ]]; then
    printf -v DEPLOY_APPROVAL_TIMEOUT %s 15m
    export DEPLOY_APPROVAL_TIMEOUT
  fi
  if [[ -z "${DEPLOY_FLEET_POLL_INTERVAL:-}" ]]; then
    printf -v DEPLOY_FLEET_POLL_INTERVAL %s 2s
    export DEPLOY_FLEET_POLL_INTERVAL
  fi
  if [[ -z "${DEPLOY_COPILOT_CLI:-}" ]]; then
    printf -v DEPLOY_COPILOT_CLI %s /usr/local/bin/copilot
    export DEPLOY_COPILOT_CLI
  fi
  if [[ -z "${DEPLOY_COPILOT_URL:-}" ]]; then
    export DEPLOY_COPILOT_URL=''
  fi
  if [[ -z "${DEPLOY_COPILOT_MODEL:-}" ]]; then
    printf -v DEPLOY_COPILOT_MODEL %s gpt-5.4
    export DEPLOY_COPILOT_MODEL
  fi
  if [[ -z "${DEPLOY_OLLAMA_URL:-}" ]]; then
    export DEPLOY_OLLAMA_URL=''
  fi
  if [[ -z "${DEPLOY_OLLAMA_MODEL:-}" ]]; then
    export DEPLOY_OLLAMA_MODEL=''
  fi
  if [[ -z "${DEPLOY_OLLAMA_MODEL_DIGEST:-}" ]]; then
    export DEPLOY_OLLAMA_MODEL_DIGEST=''
  fi
  if [[ -z "${DEPLOY_OLLAMA_CONTEXT_TOKENS:-}" ]]; then
    printf -v DEPLOY_OLLAMA_CONTEXT_TOKENS %s 16384
    export DEPLOY_OLLAMA_CONTEXT_TOKENS
  fi
  if [[ -z "${DEPLOY_OLLAMA_MAX_TOOL_CALLS:-}" ]]; then
    printf -v DEPLOY_OLLAMA_MAX_TOOL_CALLS %s 16
    export DEPLOY_OLLAMA_MAX_TOOL_CALLS
  fi
  if [[ -z "${DEPLOY_OLLAMA_TURN_TIMEOUT:-}" ]]; then
    printf -v DEPLOY_OLLAMA_TURN_TIMEOUT %s 10m
    export DEPLOY_OLLAMA_TURN_TIMEOUT
  fi
  if [[ -z "${DEPLOY_OPENCODE_URL:-}" ]]; then
    printf -v DEPLOY_OPENCODE_URL %s http://127.0.0.1:4096
    export DEPLOY_OPENCODE_URL
  fi
  if [[ -z "${DEPLOY_OPENCODE_USERNAME:-}" ]]; then
    printf -v DEPLOY_OPENCODE_USERNAME %s opencode
    export DEPLOY_OPENCODE_USERNAME
  fi
  if [[ -z "${DEPLOY_OPENCODE_SESSION_ID:-}" ]]; then
    export DEPLOY_OPENCODE_SESSION_ID=''
  fi
  if [[ -z "${DEPLOY_OPENCODE_REQUEST_TIMEOUT:-}" ]]; then
    printf -v DEPLOY_OPENCODE_REQUEST_TIMEOUT %s 10s
    export DEPLOY_OPENCODE_REQUEST_TIMEOUT
  fi
  if [[ -z "${DEPLOY_OPENCODE_POLL_INTERVAL:-}" ]]; then
    printf -v DEPLOY_OPENCODE_POLL_INTERVAL %s 1s
    export DEPLOY_OPENCODE_POLL_INTERVAL
  fi
  if [[ -z "${DEPLOY_OPENCODE_QUEUE_CAPACITY:-}" ]]; then
    printf -v DEPLOY_OPENCODE_QUEUE_CAPACITY %s 32
    export DEPLOY_OPENCODE_QUEUE_CAPACITY
  fi
  if [[ -z "${DEPLOY_LIVE_CONFIRM_PHRASE:-}" ]]; then
    printf -v DEPLOY_LIVE_CONFIRM_PHRASE %s I_UNDERSTAND_DOCKER_SOCKET_IS_ROOT
    export DEPLOY_LIVE_CONFIRM_PHRASE
  fi
}

deploy_environment_apply_profile() {
  case $1 in
    local)
      printf -v NODEEXEC_REVISION_ROOT %s%s "${DEPLOY_STATE_ROOT:-}" /revisions
      export NODEEXEC_REVISION_ROOT
      printf -v NODEEXEC_LIVE_WORKSPACE %s "${DEPLOY_HOST_WORKSPACE:-}"
      export NODEEXEC_LIVE_WORKSPACE
      printf -v DEPLOY_DATABASE_URL %s%s%s postgres://deploy: "${POSTGRES_PASSWORD:-}" '@postgres:5432/deploy?sslmode=disable'
      export DEPLOY_DATABASE_URL
      printf -v DEPLOY_WARDEN_URL %s http://warden:7717
      export DEPLOY_WARDEN_URL
      printf -v NODEEXEC_URL %s http://nodeexec:8094
      export NODEEXEC_URL
      printf -v DEPLOY_NODE_LABELS %s '{"deploy-demo":{"environment":"prototype","runtime":"compose"}}'
      export DEPLOY_NODE_LABELS
      ;;
    demo)
      printf -v DEPLOY_HARNESS_BACKEND %s demo
      export DEPLOY_HARNESS_BACKEND
      export DEPLOY_COPILOT_URL=''
      export DEPLOY_OPENCODE_URL=''
      ;;
    copilot)
      printf -v DEPLOY_HARNESS_BACKEND %s copilot-cli
      export DEPLOY_HARNESS_BACKEND
      printf -v DEPLOY_COPILOT_URL %s http://copilot:4321
      export DEPLOY_COPILOT_URL
      export DEPLOY_OPENCODE_URL=''
      ;;
    opencode)
      printf -v DEPLOY_HARNESS_BACKEND %s opencode
      export DEPLOY_HARNESS_BACKEND
      export DEPLOY_COPILOT_URL=''
      printf -v DEPLOY_OPENCODE_URL %s http://opencode:4096
      export DEPLOY_OPENCODE_URL
      ;;
    *) printf 'unknown Candacefile environment profile: %s\n' "$1" >&2; return 1 ;;
  esac
}
