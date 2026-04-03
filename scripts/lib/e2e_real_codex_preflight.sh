#!/usr/bin/env bash

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

shell_split_words() {
  local input="$1"
  local current=""
  local char=""
  local escaped=0
  local in_single=0
  local in_double=0
  local i

  E2E_SHELL_WORDS=()

  for ((i = 0; i < ${#input}; i++)); do
    char="${input:i:1}"
    if [[ "$escaped" -eq 1 ]]; then
      current+="$char"
      escaped=0
      continue
    fi
    if [[ "$char" == "\\" && "$in_single" -eq 0 ]]; then
      escaped=1
      continue
    fi
    if [[ "$char" == "'" && "$in_double" -eq 0 ]]; then
      if [[ "$in_single" -eq 1 ]]; then
        in_single=0
      else
        in_single=1
      fi
      continue
    fi
    if [[ "$char" == "\"" && "$in_single" -eq 0 ]]; then
      if [[ "$in_double" -eq 1 ]]; then
        in_double=0
      else
        in_double=1
      fi
      continue
    fi
    if [[ "$in_single" -eq 0 && "$in_double" -eq 0 && "$char" =~ [[:space:]] ]]; then
      if [[ -n "$current" ]]; then
        E2E_SHELL_WORDS+=("$current")
        current=""
      fi
      continue
    fi
    current+="$char"
  done

  if [[ "$escaped" -eq 1 ]]; then
    current+="\\"
  fi
  if [[ -n "$current" ]]; then
    E2E_SHELL_WORDS+=("$current")
  fi
}

is_shell_assignment() {
  [[ "$1" =~ ^[A-Za-z_][A-Za-z0-9_]*=.*$ ]]
}

command_executable_from_shell_command() {
  local raw_command="$1"
  local token=""
  local env_prefix=0

  shell_split_words "$raw_command"
  for token in "${E2E_SHELL_WORDS[@]}"; do
    if [[ "$env_prefix" -eq 0 && "$token" == "env" ]]; then
      env_prefix=1
      continue
    fi
    if [[ "$env_prefix" -eq 1 ]]; then
      if [[ "$token" == "--" ]]; then
        env_prefix=2
        continue
      fi
      if is_shell_assignment "$token"; then
        continue
      fi
      if [[ "$token" == -* ]]; then
        continue
      fi
      printf '%s\n' "$token"
      return 0
    fi
    if is_shell_assignment "$token"; then
      continue
    fi
    printf '%s\n' "$token"
    return 0
  done
  return 1
}

require_command_from_shell_command() {
  local label="$1"
  local raw_command="$2"
  local executable=""

  if ! executable="$(command_executable_from_shell_command "$raw_command")"; then
    echo "unable to determine executable from $label: $raw_command" >&2
    exit 1
  fi
  require_cmd "$executable"
}
