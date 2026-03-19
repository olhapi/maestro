#!/usr/bin/env bash

set -euo pipefail

sanitize_npm_env_args() {
  local name
  while IFS='=' read -r name _; do
    case "$name" in
      npm_config_*|NPM_CONFIG_*)
        case "$name" in
          npm_config_cache|NPM_CONFIG_CACHE|npm_config_userconfig|NPM_CONFIG_USERCONFIG)
            ;;
          *)
            printf '%s\0%s\0' "-u" "$name"
            ;;
        esac
        ;;
    esac
  done < <(env)
}

run_clean_npm() {
  local -a env_cmd=(env)
  while IFS= read -r -d '' arg; do
    env_cmd+=("$arg")
  done < <(sanitize_npm_env_args)

  "${env_cmd[@]}" npm "$@"
}

run_clean_npx() {
  local -a env_cmd=(env)
  while IFS= read -r -d '' arg; do
    env_cmd+=("$arg")
  done < <(sanitize_npm_env_args)

  "${env_cmd[@]}" npx "$@"
}
