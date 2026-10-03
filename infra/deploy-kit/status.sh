#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
environment_projection="$script_dir/environment.generated.sh"
[[ -r "$environment_projection" ]] || { printf 'generated environment projection is missing\n' >&2; exit 1; }
# shellcheck source=environment.generated.sh
source "$environment_projection"
source "$script_dir/compose-files.sh"
state_root=${!deploy_env_state_root-}
state_root=${state_root:-$script_dir}
[[ "$state_root" == /* ]] || { printf '%s must be absolute\n' "$deploy_env_state_root" >&2; exit 1; }
[[ -f "$state_root/.env" ]] || { printf 'Deploy is not installed; run ./install.sh\n' >&2; exit 1; }
printf -v "$deploy_env_state_root" '%s' "$state_root"
export "$deploy_env_state_root"

deploy_compose_files "$script_dir" "$state_root"
docker compose --project-directory "$script_dir" --env-file "$state_root/.env" \
  "${deploy_compose_file_args[@]}" \
  --profile dry-run --profile live --profile copilot \
  --profile opencode ps
