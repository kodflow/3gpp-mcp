#!/bin/bash
# ============================================================================
# ops-peek.sh - Collect ops context (secrets + VPN) in a single JSON call
# Usage: ops-peek.sh [project_dir]
# Exit 0 = always (fail-open)
#
# Replaces ~7-9 sequential tool calls with 1 script call.
# Used by: /secret (Phase 1), /vpn (Phase 1)
# ============================================================================

set +e

PROJECT_DIR="${1:-${CLAUDE_PROJECT_DIR:-/workspace}}"
cd "$PROJECT_DIR" 2>/dev/null || exit 0

# --- 1Password ---
OP_TOKEN_SET=false
OP_VAULT_OK=false
OP_ITEMS="[]"

if [ -n "$OP_SERVICE_ACCOUNT_TOKEN" ]; then
    OP_TOKEN_SET=true
    if command -v op >/dev/null 2>&1; then
        if op vault list --format=json >/dev/null 2>&1; then
            OP_VAULT_OK=true

            REMOTE_URL=$(git remote get-url origin 2>/dev/null || echo "")
            if [ -n "$REMOTE_URL" ]; then
                ORG_REPO=$(echo "$REMOTE_URL" | sed -E 's|.*[:/]([^/]+)/([^/.]+)(\.git)?$|\1/\2|')
                OP_ITEMS=$(op item list --vault CI --format=json 2>/dev/null | jq -r --arg prefix "$ORG_REPO" \
                    '[.[] | select(.title | startswith($prefix)) | {title: .title, category: .category}]' 2>/dev/null || echo "[]")
            fi
        fi
    fi
fi

# --- VPN clients ---
OPENVPN=$(command -v openvpn >/dev/null 2>&1 && echo "true" || echo "false")
WIREGUARD=$(command -v wg >/dev/null 2>&1 && echo "true" || echo "false")
STRONGSWAN=$(command -v ipsec >/dev/null 2>&1 || command -v charon >/dev/null 2>&1 && echo "true" || echo "false")
PPTP=$(command -v pptpd >/dev/null 2>&1 || command -v pptp >/dev/null 2>&1 && echo "true" || echo "false")

# --- VPN profiles from 1Password ---
VPN_PROFILES="[]"
if $OP_VAULT_OK; then
    VPN_PROFILES=$(op item list --vault VPN --format=json 2>/dev/null | jq '[.[] | {
        title: .title,
        category: .category,
        protocol: (
            if (.tags // [] | map(ascii_downcase) | any(. == "openvpn")) then "openvpn"
            elif (.tags // [] | map(ascii_downcase) | any(. == "wireguard")) then "wireguard"
            elif (.tags // [] | map(ascii_downcase) | any(. == "ipsec" or . == "ikev2")) then "ipsec"
            elif (.tags // [] | map(ascii_downcase) | any(. == "pptp")) then "pptp"
            else "unknown"
            end
        )
    }]' 2>/dev/null || echo "[]")
fi

# --- VPN connection state ---
VPN_CONNECTED=false
ACTIVE_IFACE=""
RUNNING_PROCESS=""

if pgrep -x openvpn >/dev/null 2>&1; then
    VPN_CONNECTED=true
    RUNNING_PROCESS="openvpn"
    ACTIVE_IFACE="tun0"
elif ip link show wg0 >/dev/null 2>&1; then
    VPN_CONNECTED=true
    RUNNING_PROCESS="wireguard"
    ACTIVE_IFACE="wg0"
elif pgrep -x charon >/dev/null 2>&1; then
    VPN_CONNECTED=true
    RUNNING_PROCESS="strongswan"
    ACTIVE_IFACE="ipsec0"
elif pgrep -x pppd >/dev/null 2>&1; then
    VPN_CONNECTED=true
    RUNNING_PROCESS="pptp"
    ACTIVE_IFACE="ppp0"
fi

# --- Output JSON ---
jq -n \
    --argjson token_set "$OP_TOKEN_SET" \
    --argjson vault_ok "$OP_VAULT_OK" \
    --argjson items "$OP_ITEMS" \
    --argjson openvpn "$OPENVPN" --argjson wireguard "$WIREGUARD" \
    --argjson strongswan "$STRONGSWAN" --argjson pptp "$PPTP" \
    --argjson profiles "$VPN_PROFILES" \
    --argjson connected "$VPN_CONNECTED" \
    --arg active_iface "$ACTIVE_IFACE" --arg running_process "$RUNNING_PROCESS" \
    '{
        onepassword: {token_set: $token_set, vault_accessible: $vault_ok, items: $items},
        vpn: {
            clients: {openvpn: $openvpn, wireguard: $wireguard, strongswan: $strongswan, pptp: $pptp},
            profiles: $profiles,
            state: {connected: $connected, active_interface: (if $active_iface == "" then null else $active_iface end), running_process: (if $running_process == "" then null else $running_process end)}
        }
    }'
