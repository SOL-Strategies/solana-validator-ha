#!/bin/bash
# ⚠️ !!!!WARNING!!!!
# THIS IS AN EXAMPLE SCRIPT - DO NOT USE THIS SCRIPT WITHOUT UNDERSTANDING IT AND MODIFYING IT TO YOUR NEEDS
# USING IT WITHOUT AT LEAST REVIEWING IT CAN POTENTIALLY MESS THINGS UP FOR YOU BIGLY
# ⚠️ !!!!WARNING!!!!
#
# example usage in solana-validator-ha config.yaml:
# ....
# failover:
#  passive: # a.k.a Seppukku
#    command: "/home/solana/solana-validator-ha/scripts/ha-set-role.sh"
#    args: [
#      "--role", "passive",
#      "--client", "agave",
#      "--rpc-url", "http://127.0.0.1:8899",
#      "--identity-keyfile", "{{ .PassiveIdentityKeypairFile }}",
#      "--tower-file", "/mnt/accounts/tower/tower-1_9-{{ .ActiveIdentityPubkey }}.bin",
#    ]
#  active:
#    command: "/home/solana/solana-validator-ha/scripts/ha-set-role.sh"
#    args: [
#      "--role", "active",
#      "--client", "agave",
#      "--rpc-url", "http://127.0.0.1:8899",
#      "--identity-keyfile", "{{ .ActiveIdentityKeypairFile }}",
#      "--authorized-voter-keyfile", "/home/solana/voters/{{ .AuthorizedVoterPubkey }}.json",
#      "--authorized-voter-pubkey", "{{ .AuthorizedVoterPubkey }}",
#      "--tower-file", "/mnt/accounts/tower/tower-1_9-{{ .ActiveIdentityPubkey }}.bin",
#    ]
# ....
set -euo pipefail
declare -A CONFIG=()
CONFIG["client"]=""
CONFIG["identity-keyfile"]=""
CONFIG["role"]=""
CONFIG["rpc-url"]=""
CONFIG["tower-file"]=""
CONFIG["authorized-voter-keyfile"]=""
CONFIG["authorized-voter-pubkey"]=""
# optional - override defaults if your layout differs
CONFIG["user"]="solana"
CONFIG["ledger-dir"]="/mnt/ledger"
CONFIG["firedancer-config"]="/home/solana/config.toml"

# convenience poor man's logger with echo to be quick
logger() {
  local level="$1"
  local message="$2"
  shift 2
  local level_short=$(echo "$level" | cut -c1-4 | tr '[:lower:]' '[:upper:]')
  local caller_name="${FUNCNAME[1]:-main}"
  local script_name="${0##*/}"

  # anything after the message is considered a key-value pair
  # loop through and add to suffix of the message
  declare log_kv_pairs=()
  local args=("$@")
  if [ ${#args[@]} -gt 0 ]; then
    for ((i = 0; i < ${#args[@]}; i += 2)); do
      if ((i + 1 < ${#args[@]})); then
        # Pair current arg with next arg
        log_kv_pairs+=("${args[i]}=\"${args[i + 1]}\"")
      else
        # Odd number of args, last one gets =?
        log_kv_pairs+=("${args[i]}=\"\"")
      fi
    done
  fi

  echo "[$level_short ${script_name}::${caller_name}]: ${message} ${log_kv_pairs[@]}" >&2
  if [ "$level" == "fatal" ]; then
    exit 1
  fi
}

# print usage
print_usage() {
  local exit_code="${1:-0}"
  cat <<EOF >&2
Set the role of the validator

$0 [flags]

Flags:
    --client                   <client> (required) client (one of: agave, jito-solana, firedancer)
    --identity-keyfile         <file>   (required) identity keyfile
    --role                     <role>   (required) role to transition to (one of: active, passive)
    --rpc-url                  <url>    (required) local validator rpc url
    --tower-file               <file>   (required) tower file
    --authorized-voter-keyfile <file>   (optional) authorized voter keypair to set before active identity
    --authorized-voter-pubkey  <pubkey> (optional) expected pubkey for --authorized-voter-keyfile
    --firedancer-config        <file>   (optional) firedancer config file (default: /home/solana/config.toml)
    --ledger-dir               <dir>    (optional) ledger directory (default: /mnt/ledger)
    --user                     <user>   (optional) user to run set identity commands as (default: solana)
    -h,  --help
EOF
  exit "$exit_code"
}

# parse args
parse_args() {
  # if no args are provided, print usage
  [ $# -eq 0 ] && print_usage 1

  # required fields - validated non-empty after parsing
  local required_fields=("client" "identity-keyfile" "role" "rpc-url" "tower-file")

  # parse args
  while [ $# -gt 0 ]; do
    case $1 in
    --role | --client | --identity-keyfile | --rpc-url | --tower-file | --authorized-voter-keyfile | --authorized-voter-pubkey | --user | --ledger-dir | --firedancer-config)
      CONFIG["${1#--}"]="$2"
      shift 2
      ;;
    --role=* | --client=* | --identity-keyfile=* | --rpc-url=* | --tower-file=* | --authorized-voter-keyfile=* | --authorized-voter-pubkey=* | --user=* | --ledger-dir=* | --firedancer-config=*)
      local key="${1#--}"
      key="${key%%=*}"
      local value="${1#*=}"
      CONFIG["${key}"]="${value}"
      shift 1
      ;;
    --help | -h)
      print_usage 0
      ;;
    *)
      logger fatal "Unknown argument: $1"
      ;;
    esac
  done

  # ensure required fields are non-empty
  for key in "${required_fields[@]}"; do
    if [ -z "${CONFIG[$key]}" ]; then
      logger fatal "Required flag --${key} is missing or empty"
    fi
  done

  # ensure role is one of: active, passive
  if [ "${CONFIG["role"]}" != "active" ] && [ "${CONFIG["role"]}" != "passive" ]; then
    logger fatal "Role must be one of: active, passive"
  fi

  # add to config array - ensure identity-keyfile is a valid file and can get pubkey for it
  local keyfile="${CONFIG["identity-keyfile"]}"
  CONFIG["identity-pubkey"]="$(solana-keygen pubkey "${keyfile}")"
  if [ -z "${CONFIG["identity-pubkey"]}" ]; then
    logger fatal "Unable to get pubkey for identity keyfile" keyfile "${keyfile}"
  fi
  logger info "retrieved identity from file" keyfile "${keyfile}" pubkey "${CONFIG["identity-pubkey"]}"

  if [ -n "${CONFIG["authorized-voter-keyfile"]}" ]; then
    local voter_keyfile="${CONFIG["authorized-voter-keyfile"]}"
    CONFIG["authorized-voter-keyfile-pubkey"]="$(solana-keygen pubkey "${voter_keyfile}")"
    if [ -z "${CONFIG["authorized-voter-keyfile-pubkey"]}" ]; then
      logger fatal "Unable to get pubkey for authorized voter keyfile" keyfile "${voter_keyfile}"
    fi
    if [ -n "${CONFIG["authorized-voter-pubkey"]}" ] && [ "${CONFIG["authorized-voter-pubkey"]}" != "${CONFIG["authorized-voter-keyfile-pubkey"]}" ]; then
      logger fatal "authorized voter keyfile does not match expected pubkey" keyfile "${voter_keyfile}" want "${CONFIG["authorized-voter-pubkey"]}" got "${CONFIG["authorized-voter-keyfile-pubkey"]}"
    fi
    CONFIG["authorized-voter-pubkey"]="${CONFIG["authorized-voter-keyfile-pubkey"]}"
    logger info "retrieved authorized voter from file" keyfile "${voter_keyfile}" pubkey "${CONFIG["authorized-voter-pubkey"]}"
  elif [ "${CONFIG["role"]}" = "active" ] && [ -n "${CONFIG["authorized-voter-pubkey"]}" ]; then
    logger fatal "active role requires --authorized-voter-keyfile when --authorized-voter-pubkey is set" pubkey "${CONFIG["authorized-voter-pubkey"]}"
  fi

  # ensure client is one of: agave, jito-solana, firedancer and set service and set-identity command accordingly
  case "${CONFIG["client"]}" in
  "firedancer")
    CONFIG["service"]="firedancer.service"
    CONFIG["set-identity-command"]="fdctl set-identity --config ${CONFIG["firedancer-config"]} --force ${CONFIG["identity-keyfile"]}"
    ;;
  agave | jito-solana)
    CONFIG["service"]="sol.service"
    CONFIG["set-identity-command"]="agave-validator --ledger ${CONFIG["ledger-dir"]} set-identity ${CONFIG["identity-keyfile"]}"
    ;;
  *)
    logger fatal "unknown client: ${CONFIG["client"]}" got "${CONFIG["client"]}" want "agave|jito-solana|firedancer"
    ;;
  esac
}

set_authorized_voter() {
  local voter_keyfile="${CONFIG["authorized-voter-keyfile"]}"
  if [ -z "${voter_keyfile}" ]; then
    logger warn "no authorized voter keyfile supplied - skipping authorized voter update"
    return
  fi

  case "${CONFIG["client"]}" in
  agave | jito-solana)
    local remove_cmd="agave-validator --ledger ${CONFIG["ledger-dir"]} authorized-voter remove-all"
    local add_cmd="agave-validator --ledger ${CONFIG["ledger-dir"]} authorized-voter add ${voter_keyfile}"
    logger info "clearing existing authorized voters" command "${remove_cmd}"
    run_as_user "${remove_cmd}" >/dev/null
    logger info "adding authorized voter" pubkey "${CONFIG["authorized-voter-pubkey"]}" command "${add_cmd}"
    run_as_user "${add_cmd}" >/dev/null
    ;;
  firedancer)
    logger fatal "authorized voter runtime update is not implemented for firedancer in this example script"
    ;;
  *)
    logger fatal "unknown client for authorized voter update" client "${CONFIG["client"]}"
    ;;
  esac
}

has_requested_identity() {
  get_rpc_identity
  if [ "${CONFIG["rpc-identity"]}" = "${CONFIG["identity-pubkey"]}" ]; then
    logger info "local rpc reports desired identity - nothing to do" want "${CONFIG["identity-pubkey"]}" got "${CONFIG["rpc-identity"]}"
    return 0 # success
  fi
  logger warn "local rpc reports different identity to requested" want "${CONFIG["identity-pubkey"]}" got "${CONFIG["rpc-identity"]}"
  return 1 # failure
}

get_rpc_identity() {
  local rpc_url="${CONFIG["rpc-url"]}"

  # Capture both stdout and stderr from the entire pipeline
  local curl_output
  local jq_output
  local error_msg=""

  # First, capture curl output (including errors)
  if ! curl_output=$(curl -sSf -X POST -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","id":1,"method":"getIdentity"}' \
    "${rpc_url}" 2>&1); then
    error_msg="curl failed: ${curl_output}"
    CONFIG["rpc-identity"]="unknown"
    logger error "failed getting identity from local rpc" rpc_url "${rpc_url}" error "${error_msg}" pubkey "${CONFIG["rpc-identity"]}"
    return
  fi

  # If curl succeeded, try to parse JSON
  if ! jq_output=$(echo "${curl_output}" | jq -er '.result.identity' 2>&1); then
    error_msg="jq parse failed: ${jq_output}"
    CONFIG["rpc-identity"]="unknown"
    logger error "failed getting identity from local rpc" rpc_url "${rpc_url}" error "${error_msg}" response "${curl_output}" pubkey "${CONFIG["rpc-identity"]}"
    return
  fi

  # Success
  CONFIG["rpc-identity"]="${jq_output}"
  logger info "retrieved identity from local rpc" rpc_url "${rpc_url}" pubkey "${CONFIG["rpc-identity"]}"
}

require_rpc_healthy() {
  local rpc_url="${CONFIG["rpc-url"]}"
  local health="$(curl -sSf "${rpc_url}/health" 2>&1)"
  if [ "${health}" != "ok" ]; then
    return 1
  fi
}

# wait for local rpc to be healthy - blocks until healthy (service may be starting or restarting)
wait_for_rpc_healthy() {
  local rpc_url="${CONFIG["rpc-url"]}"
  logger info "waiting for local rpc to become healthy" rpc_url "${rpc_url}"
  while ! require_rpc_healthy; do
    sleep 1
  done
  logger info "local rpc is healthy" rpc_url "${rpc_url}"
}

# wait for rpc to report the requested identity, up to <timeout> seconds (default: 30)
# returns 1 if the timeout is reached without the identity matching
wait_for_identity() {
  local timeout="${1:-30}"
  local elapsed=0
  logger info "waiting for rpc to reflect new identity" pubkey "${CONFIG["identity-pubkey"]}" timeout "${timeout}"
  while ! has_requested_identity; do
    if [ "${elapsed}" -ge "${timeout}" ]; then
      logger warn "timed out waiting for rpc to reflect new identity" pubkey "${CONFIG["identity-pubkey"]}" timeout "${timeout}"
      return 1
    fi
    sleep 2
    elapsed=$((elapsed + 2))
  done
}

run_as_user() {
  local command="$1"
  local user="${CONFIG["user"]}"
  logger info "running" user "${user}" command "${command}"
  if ! output=$(su - "${user}" -c "${command}" 2>&1); then
    logger error "failed" user "${user}" command "${command}" error "${output}"
    echo "${output}"
    return 1
  fi
  logger info "done" user "${user}" command "${command}" output "${output}"
  echo "${output}"
}

remove_tower_file() {
  local tower_file="${CONFIG["tower-file"]}"
  if [ -f "${tower_file}" ]; then
    logger info "removing" file "${tower_file}"
    rm -f "${tower_file}"
    logger info "removed" file "${tower_file}"
  fi
}

# called when set-identity fails: try to restart so it comes back up passive, then escalate
# to stop/disable if restart fails - we prefer a brief outage over two active validators
restart_or_stop_service() {
  local service="${CONFIG["service"]}"
  local systemctl_error
  if systemctl restart "${service}" 2>&1; then
    logger info "service restarted successfully" service "${service}"
  elif systemctl_error=$(systemctl stop "${service}" 2>&1); then
    logger warn "service restart failed, stopped service" service "${service}" error "${systemctl_error}"
  elif systemctl_error=$(systemctl disable "${service}" 2>&1); then
    logger error "service stop failed, disabled service" service "${service}" error "${systemctl_error}"
  else
    logger fatal "failed to restart, stop, or disable service" service "${service}"
  fi
}

# attempt to set active - if we fail here solana-validator-ha will just attempt again on the next iteration
set_active() {
  local cmd="${CONFIG["set-identity-command"]}"

  # a validator ready to be active must be healthy and have the correct identity -
  # single-shot check: if unhealthy, fail immediately and let the HA manager try again
  # on the next cycle rather than blocking here
  require_rpc_healthy || logger fatal "local rpc is unhealthy" rpc_url "${CONFIG["rpc-url"]}"

  # already has requested identity, we are good
  if has_requested_identity; then
    set_authorized_voter
    return
  fi

  # active promotion must install the profile's authorized voter before identity switch.
  set_authorized_voter

  # remove the stale local tower file so the validator fetches a fresh one from the network
  # on its next restart, rather than loading a potentially outdated local copy
  remove_tower_file

  logger info "setting active" pubkey "${CONFIG["identity-pubkey"]}" command "${cmd}"
  if ! output=$(run_as_user "${cmd}"); then
    logger fatal "failed" command "${cmd}" error "${output}"
  fi
  logger info "set-identity command succeeded" command "${cmd}" output "${output}"

  # verify the rpc reflects the new identity before declaring success
  if ! wait_for_identity 20; then
    logger fatal "rpc did not reflect active identity after set-identity" pubkey "${CONFIG["identity-pubkey"]}"
  fi
  logger info "successfully set active" pubkey "${CONFIG["identity-pubkey"]}"
}

# if for whatever reason we cannot set the identity to passive, we restart the service to ensure it comes up as passive (we always configure them so)
# we prefer having no leader (being hard down) over potentially having multiple leaders
set_passive() {
  local service="${CONFIG["service"]}"

  if ! systemctl is-active "${service}" >/dev/null 2>&1; then
    # service is already down - it will come up passive since we always configure it that way
    logger info "service is not running - already passive" service "${service}"
  else
    # wait for rpc to respond (service may be mid-restart or starting up)
    wait_for_rpc_healthy

    if ! has_requested_identity; then
      local cmd="${CONFIG["set-identity-command"]}"
      logger info "running command to set passive" command "${cmd}"
      if ! output=$(run_as_user "${cmd}"); then
        logger error "set-identity failed, escalating to service restart" command "${cmd}" error "${output}"
        restart_or_stop_service
      else
        logger info "set-identity command succeeded" command "${cmd}" result "${output}"
        # fatal if unconfirmed - do not remove the tower file from under a potentially still-active validator
        wait_for_identity 20 || logger fatal "rpc did not reflect passive identity after set-identity" pubkey "${CONFIG["identity-pubkey"]}"
      fi
    fi
  fi

  # always remove the stale tower file so the validator fetches a fresh one from the
  # network on its next startup rather than loading a potentially outdated local copy
  remove_tower_file
}

main() {
  logger info "🐒💥💩 $0 $*"
  parse_args "$@"
  case "${CONFIG["role"]}" in
  active)
    set_active
    ;;
  passive)
    set_passive
    ;;
  *)
    logger fatal "unknown role ${CONFIG["role"]} must be one of: active|passive"
    ;;
  esac
}

main "$@"
