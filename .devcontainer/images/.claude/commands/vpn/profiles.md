# 1Password Profile Management

## Phase 1.0: Peek (MANDATORY)

**Verify prerequisites BEFORE any action:**

```yaml
peek_workflow:
  1_check_vpn_clients:
    action: "Check installed VPN clients"
    commands:
      - "command -v openvpn"
      - "command -v wg"
      - "command -v ipsec"
      - "command -v pptp"
    note: "At least one must be present. List all found."

  2_check_op:
    action: "Verify op CLI is available"
    command: "command -v op"
    on_failure: |
      ABORT with message:
      "op CLI not found. Install 1Password CLI or run inside DevContainer."

  3_check_token:
    action: "Verify OP_SERVICE_ACCOUNT_TOKEN"
    command: "test -n \"$OP_SERVICE_ACCOUNT_TOKEN\""
    on_failure: |
      ABORT with message:
      "OP_SERVICE_ACCOUNT_TOKEN not set. Configure in .devcontainer/.env"

  4_check_vault:
    action: "Verify access to VPN vault"
    command: "op vault get \"${VPN_VAULT:-VPN}\" --format json 2>/dev/null | jq -r '.id'"
    store: "VAULT_NAME"
    on_failure: |
      ABORT with message:
      "Cannot access vault '${VPN_VAULT:-VPN}'. Check 1Password configuration."

  5_check_state:
    action: "Check if any VPN is already connected"
    commands:
      - "pgrep -x openvpn"
      - "ip link show wg0 2>/dev/null"
      - "pgrep -x charon"
      - "pgrep -x pppd"
    store: "VPN_RUNNING (type + boolean)"
    note: "Informational - does not abort"
```

**Output Phase 1:**

```
═══════════════════════════════════════════════════════════════════
  /vpn - Connection Check
═══════════════════════════════════════════════════════════════════

  VPN Clients:
    OpenVPN    : /usr/sbin/openvpn
    WireGuard  : /usr/bin/wg
    StrongSwan : /usr/sbin/ipsec
    PPTP       : /usr/sbin/pptp

  1Password CLI: op
  Service Token: OP_SERVICE_ACCOUNT_TOKEN (set)
  Vault Access : VPN
  VPN State    : DISCONNECTED

═══════════════════════════════════════════════════════════════════
```

---

## Phase 1.5: OS Agent Dispatch (Parallel)

**After Peek completes, dispatch to the appropriate OS specialist for client validation:**

```yaml
os_dispatch:
  trigger: "After Phase 1.0 Peek completes"

  1_detect_os:
    linux:
      command: "cat /etc/os-release 2>/dev/null | grep -E '^ID=' | cut -d= -f2 | tr -d '\"'"
      routing_table:
        debian: os-specialist-debian
        ubuntu: os-specialist-ubuntu
        fedora: os-specialist-fedora
        rhel|centos|rocky|almalinux: os-specialist-rhel
        arch|manjaro: os-specialist-arch
        alpine: os-specialist-alpine
        opensuse-leap|opensuse-tumbleweed: os-specialist-opensuse
        void: os-specialist-void
        devuan: os-specialist-devuan
        artix: os-specialist-artix
        gentoo: os-specialist-gentoo
        nixos: os-specialist-nixos
        kali: os-specialist-kali
        slackware: os-specialist-slackware
    bsd:
      command: "uname -s"
      routing_table:
        FreeBSD: os-specialist-freebsd
        OpenBSD: os-specialist-openbsd
        NetBSD: os-specialist-netbsd
        DragonFly: os-specialist-dragonflybsd
    darwin: os-specialist-macos
    fallback: "devops-executor-linux (generic)"

  2_dispatch:
    mode: "single Task call"
    prompt: |
      Validate VPN client installation and configuration for {protocol}:
      - Is {vpn_client} installed? If not, provide install command.
      - Check firewall rules for VPN traffic (UDP 1194, UDP 51820, UDP 500/4500).
      - Verify TUN/TAP device availability.
      - Check DNS resolver configuration.
      Return condensed JSON with install commands and config recommendations.

  3_use_result:
    action: "Integrate OS-specific commands into connect/disconnect workflows"
    example: |
      # Agent returns:
      {"commands": [{"description": "Install WireGuard", "command": "apk add wireguard-tools", "sudo": true}]}
      # Skill uses the exact command for the detected OS
```

---

## Action: --list

**List available VPN profiles from 1Password vault (all protocols):**

```yaml
list_workflow:
  1_list_documents:
    action: "List DOCUMENT items in VPN vault with tags"
    command: |
      vault="${VPN_VAULT:-VPN}"
      op item list --vault "$vault" --categories DOCUMENT --format json \
        | jq -r '.[] | {title: .title, tags: (.tags // [])}'
    store: "PROFILES with tags"

  2_detect_protocol:
    action: "Determine protocol from tags"
    logic: |
      for each profile:
        tags = item.tags
        if "wireguard" in tags: protocol = "wireguard"
        elif "ipsec" in tags: protocol = "ipsec"
        elif "pptp" in tags: protocol = "pptp"
        else: protocol = "openvpn"  # default

  3_list_logins:
    action: "Cross-reference LOGIN items"
    command: |
      op item list --vault "$vault" --categories LOGIN --format json \
        | jq -r '.[].title'
    store: "LOGIN_TITLES"

  4_display:
    action: "Display table with protocol, config, and credentials"
```

**Output --list:**

```
═══════════════════════════════════════════════════════════════════
  /vpn --list
═══════════════════════════════════════════════════════════════════

  Vault: VPN

  | Profile    | Protocol  | Config     | Credentials |
  |------------|-----------|------------|-------------|
  | HOME       | openvpn   | DOCUMENT   | LOGIN       |
  | OFFICE     | wireguard | DOCUMENT   | N/A         |
  | DATACENTER | ipsec     | DOCUMENT   | LOGIN       |

  Total: 3 profiles

  Default: HOME (from VPN_CONFIG_REF)

═══════════════════════════════════════════════════════════════════
```
