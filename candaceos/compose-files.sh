#!/usr/bin/env bash
# Shared one-box deployment file selection. The optional override is operator
# state, outside revision checkouts, and may contain credentials.
candaceos_compose_files() {
  local source_dir=$1 override="$2/compose.override.yaml"
  candaceos_compose_file_args=(
    -f "$source_dir/compose.yaml"
    -f "$source_dir/compose.environment.generated.yaml"
  )
  if [[ -e "$override" || -L "$override" ]]; then
    if [[ ! -f "$override" || -L "$override" || ! -O "$override" || $(stat -c '%a' "$override") != 600 ]]; then
      printf 'candaceos: compose.override.yaml must be an owned 0600 regular file\n' >&2
      return 1
    fi
    candaceos_compose_file_args+=(-f "$override")
  fi
}
