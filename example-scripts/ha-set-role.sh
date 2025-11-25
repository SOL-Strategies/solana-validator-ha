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
CONFIG["user"]="solana"

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
        for ((i=0; i<${#args[@]}; i+=2)); do
            if ((i+1 < ${#args[@]})); then
                # Pair current arg with next arg
                log_kv_pairs+=("${args[i]}=\"${args[i+1]}\"")
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
print_usage () {
    local exit_code="${1:-0}"
    cat <<EOF >&2
Set the role of the validator

$0 [flags]

Flags:
    --client                   <client> (required) client (one of: agave, jito|jito-solana, bam-client, firedancer)
    --identity-keyfile         <file>   (required) identity keyfile
    --role                     <role>   (required) role to transition to (one of: active, passive)
    --rpc-url                  <url>    (required) local validator rpc url
    --tower-file               <file>   (required) tower file
    --user                     <user>   (optional) user to run set identity commands as (default: solana)
    -h,  --help
EOF
    exit "$exit_code"
}

# parse args
parse_args () {
    # if no args are provided, print usage
    [ $# -eq 0 ] && print_usage 1

    # parse args
    while [ $# -gt 0 ]; do
        case $1 in
            --role|--client|--identity-keyfile|--rpc-url|--tower-file)
                CONFIG["${1#--}"]="$2"
                shift 2
                ;;
            --role=*|--client=*|--identity-keyfile=*|--rpc-url=*|--tower-file=*)
                local key=$(echo "${1#--}" | cut -d= -f1)
                local value=$(echo "${1#--}" | cut -d= -f2)
                CONFIG["${key}"]="${value}"
                shift 1
                ;;
            --help|-h)
                print_usage 0
                ;;
            *)
                logger fatal "Unknown argument: $1"
                ;;
        esac
    done

    # ensure config has all fields non-empty
    for key in "${!CONFIG[@]}"; do
        if [ -z "${CONFIG[$key]}" ]; then
            logger fatal "Config field ${key} is empty"
        fi
    done

    # ensure role is one of: active, passive
    if [ "${CONFIG["role"]}" != "active" ] && [ "${CONFIG["role"]}" != "passive" ]; then
        logger fatal "Role must be one of: active, passive"
    fi

    # add to config array - ensure identity-keyfile is a valid file and can get pubkey for it
    local keyfile="${CONFIG["identity-keyfile"]}"
    if [ -z "${keyfile}" ]; then
        logger fatal "Identity keyfile is empty"
    fi

    CONFIG["identity-pubkey"]="$(solana-keygen pubkey "${keyfile}")"
    if [ -z "${CONFIG["identity-pubkey"]}" ]; then
        logger fatal "Unable to get pubkey for identity keyfile" keyfile "${keyfile}"
    fi
    logger info "retrieved identity from file" keyfile "${keyfile}" pubkey "${CONFIG["identity-pubkey"]}"

    # ensure client is one of: agave, jito|jito-solana, bam-client, firedancer and set service and set-identity command accordingly
    case "${CONFIG["client"]}" in
        "firedancer")
            CONFIG["service"]="firedancer.service"
            CONFIG["set-identity-command"]="fdctl set-identity --config /home/solana/config.toml --force ${CONFIG["identity-keyfile"]}"
            ;;
        agave|jito|jito-solana|bam-client)
            CONFIG["service"]="sol.service"
            CONFIG["set-identity-command"]="agave-validator --ledger /mnt/ledger set-identity ${CONFIG["identity-keyfile"]}"
            ;;
        *)
            logger fatal "unknown client: ${CONFIG["client"]}" got "${CONFIG["client"]}" want "agave|jito|jito-solana|bam-client|firedancer"
            ;;
    esac
}

has_requested_identity () {
    get_rpc_identity
    if [ "${CONFIG["rpc-identity"]}" = "${CONFIG["identity-pubkey"]}" ]; then
        logger info "local rpc reports desired identity - nothing to do" want "${CONFIG["identity-pubkey"]}" got "${CONFIG["rpc-identity"]}"
        return 0 # success
    fi
    logger warn "local rpc reports different identity to requested" want "${CONFIG["identity-pubkey"]}" got "${CONFIG["rpc-identity"]}"
    return 1 # failure
}

get_rpc_identity () {
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

require_rpc_healthy () {
    local rpc_url="${CONFIG["rpc-url"]}"
    local health="$(curl -sSf "${rpc_url}/health" 2>&1)"
    if [ "${health}" != "ok" ]; then
        return 1
    fi
}

# wait for local rpc healthy - no timeout
wait_for_rpc_healthy () {
    local rpc_url="${CONFIG["rpc-url"]}"
    logger info "waiting for local rpc to become healthy" rpc_url "${rpc_url}"
    while ! require_rpc_healthy; do
        sleep 1
    done
    logger info "local rpc is healthy" rpc_url "${rpc_url}"
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

remove_tower_file () {
    local tower_file="${CONFIG["tower-file"]}"
    if [ -f "${tower_file}" ]; then
        logger info "removing" file "${tower_file}"
        rm -f "${tower_file}"
        logger info "removed" file "${tower_file}"
    fi
}

# attempt to set active - if we fail here solana-validator-ha will just attempt again on the next iteration
set_active () {
    local cmd="${CONFIG["set-identity-command"]}"
    # a validator ready to be active must be healthy and have the correct identity
    # must respond with its identity and have health ok
    require_rpc_healthy || logger fatal "local rpc is unhealthy" rpc_url "${CONFIG["rpc-url"]}"

    # already has requested identity, we are good
    if has_requested_identity; then
        return
    fi

    # remove tower file if it exists to ensure we don't accidentally use an outdated one
    remove_tower_file

    logger info "setting active" pubkey "${CONFIG["identity-pubkey"]}" command "${cmd}"
    # run the command to set the identity as active
    if ! output=$(run_as_user "${cmd}"); then
        logger fatal "failed" command "${cmd}" error "${output}"
    fi
    logger info "successfully set active" command "${cmd}" output "${output}"
}

# if for whatever reason we cannot set the identity to passive, we restart the service to ensure it comes up as passive (we always configure them so)
# we prefer having no leader (being hard down) over potentially having multiple leaders
set_passive () {
    local service="${CONFIG["service"]}"
    # if the service is not running, we are already passive because we always start with a passive identity
    if ! systemctl is-active "${service}" >/dev/null 2>&1; then
        logger info "service is not running - already passive, waiting for it to start" service "${service}"
    fi

    # wait for service RPC to respond so that we can check its loaded identity
    wait_for_rpc_healthy

    # if already has requested identity, we are good
    if has_requested_identity; then
        return
    fi

    # doesn't have requested identity, so we need to set it
    local cmd="${CONFIG["set-identity-command"]}"
    logger info "running command to set passive" command "${cmd}"

    # if we fail to set the identity to passive, force it by restarting the service
    # to ensure it comes backup as passive (we always configure them so they start as passive)
    if ! output=$(run_as_user "${cmd}"); then
        logger error "failed" command "${cmd}" error "${output}"
        logger warn "ensuring service is restarted" service "${service}"
        # make goddamn sure we take the service down - try restart, then stop, then disable
        local systemctl_error
        if systemctl restart "${service}" 2>&1; then
            logger info "service restarted successfully" service "${service}"
        elif systemctl_error=$(systemctl stop "${service}" 2>&1); then
            logger warn "service restart failed, stopped service" service "${service}" error "${systemctl_error}"
        elif systemctl_error=$(systemctl disable "${service}" 2>&1); then
            logger error "service stop failed, disabled service" service "${service}" error "${systemctl_error}"
        else
            # All three operations failed - capture the last error
            systemctl_error=$(systemctl disable "${service}" 2>&1 || echo "unknown error")
            logger fatal "failed to restart, stop, or disable service" service "${service}" error "${systemctl_error}"
        fi
    else
        logger info "done" command "${cmd}" result "${output}"
    fi
    # remove the tower file to ensure if this comes active again it doesn't try to use an outdated file
    remove_tower_file
}

main () {
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
