---
name: vpn
description: |
  Multi-protocol VPN management with 1Password multi-profile support.
  Supports OpenVPN, WireGuard, IPsec/IKEv2, and PPTP.
  List/connect/disconnect VPN profiles from vault "VPN".
  Use when: managing VPN connections, listing available profiles.
allowed-tools:
  - "Bash(op:*)"
  - "Bash(sudo:*)"
  - "Bash(pgrep:*)"
  - "Bash(ip:*)"
  - "Bash(wg:*)"
  - "Bash(ipsec:*)"
  - "Bash(tail:*)"
  - "Bash(wc:*)"
  - "Bash(jq:*)"
  - "Read(**/*)"
  - "Edit(**/.env)"
  - "Glob(**/*)"
  - "Grep(**/*)"
  - "AskUserQuestion(*)"
  - "Task(*)"
---

# /vpn - Multi-Protocol VPN Management (1Password)

$ARGUMENTS

## Overview

Interactive VPN management via **1Password CLI** (`op`) with multi-profile and multi-protocol support:

- **Peek** - Verify prerequisites (VPN clients, op CLI, vault access, current state)
- **Execute** - List profiles, connect, disconnect, or show status
- **Synthesize** - Display formatted result

**Backend**: 1Password vault "VPN" (configurable via `VPN_VAULT`)
**Protocols**: OpenVPN, WireGuard, IPsec/IKEv2, PPTP
**Convention**: Each profile = items with same title in the vault:
  - `PROFILE` (DOCUMENT) = config file (.ovpn, .conf, etc.)
  - `PROFILE` (LOGIN) = credentials (optional, not needed for WireGuard)
  - Tags on DOCUMENT determine protocol: `openvpn` (default), `wireguard`, `ipsec`, `pptp`

---

## Arguments

| Pattern | Action |
|---------|--------|
| `--list` | List available VPN profiles from 1Password vault (all protocols) |
| `--connect <profile>` | Connect to a named VPN profile |
| `--connect` (no arg) | Connect using default profile from `VPN_CONFIG_REF` in `.env` |
| `--disconnect` | Stop VPN and clean up credentials |
| `--status` | Show connection state, interface IP, and recent logs |
| `--help` | Show usage |

### Examples

```bash
# List available VPN profiles (all protocols)
/vpn --list

# Connect to a specific profile
/vpn --connect HOME

# Connect using default from .env
/vpn --connect

# Check VPN status
/vpn --status

# Disconnect
/vpn --disconnect
```

---

## --help

```
═══════════════════════════════════════════════════════════════════
  /vpn - Multi-Protocol VPN Management (1Password)
═══════════════════════════════════════════════════════════════════

Usage: /vpn <action> [options]

Actions:
  --list                  List VPN profiles in vault (all protocols)
  --connect [profile]     Connect to VPN (default: from .env)
  --disconnect            Stop VPN and clean up
  --status                Show connection state

Options:
  --help                  Show this help

Supported Protocols:
  OpenVPN    (.ovpn)   - tag: "openvpn" (or no tag = default)
  WireGuard  (.conf)   - tag: "wireguard" (no credentials needed)
  IPsec/IKEv2          - tag: "ipsec" (StrongSwan)
  PPTP                 - tag: "pptp"

1Password Convention (vault "VPN"):
  Each profile = items with same title:
    "HOME" (DOCUMENT) -> config file (tagged with protocol)
    "HOME" (LOGIN)    -> username + password (optional)

  Configure default: VPN_CONFIG_REF=op://VPN/HOME
  in .devcontainer/.env

Examples:
  /vpn --list
  /vpn --connect HOME
  /vpn --connect
  /vpn --status
  /vpn --disconnect

═══════════════════════════════════════════════════════════════════
```

---

## Module Reference

| Action | Module |
|--------|--------|
| Prerequisites & profile listing | Read ~/.claude/commands/vpn/profiles.md |
| Connect, disconnect, status | Read ~/.claude/commands/vpn/connect.md |
| Protocol details & safeguards | Read ~/.claude/commands/vpn/protocols.md |

---

## Routing

1. **Always start** with Phase 1.0 Peek from `profiles.md`
2. **--list**: Execute list workflow from `profiles.md`
3. **--connect**: Execute connect workflow from `connect.md`
4. **--disconnect**: Execute disconnect workflow from `connect.md`
5. **--status**: Execute status workflow from `connect.md`
6. **Protocol details**: Refer to `protocols.md` for client-specific commands
