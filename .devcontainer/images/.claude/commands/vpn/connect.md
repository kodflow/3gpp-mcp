# Connection Workflow

## Action: --connect

**Connect to a VPN profile (protocol-aware):**

```yaml
connect_workflow:
  1_auto_disconnect:
    action: "Disconnect any active VPN first (only one VPN at a time)"
    rule: "ONLY ONE VPN CONNECTION ALLOWED AT ANY TIME"
    logic: |
      If any VPN is active (openvpn, wg0, charon, pppd):
        1. Inform user: "Disconnecting active VPN (only one allowed)..."
        2. Run disconnect for the detected protocol
        3. Clean up credentials
        4. Proceed with new connection
    commands:
      openvpn_active: "pgrep -x openvpn → sudo killall openvpn"
      wireguard_active: "ip link show wg0 → sudo wg-quick down wg0"
      ipsec_active: "pgrep -x charon → sudo ipsec stop"
      pptp_active: "pgrep -x pppd → sudo killall pppd"
    cleanup: "rm -f /tmp/vpn-auth.txt"
    note: "Auto-disconnect is silent and fast. User sees brief info message."

  2_resolve_profile:
    action: "Determine profile name"
    logic: |
      if argument provided:
        profile = argument
      else:
        ref="${VPN_CONFIG_REF:-${OPENVPN_CONFIG_REF:-}}"
        if [ -z "$ref" ]; then
          ABORT "No profile specified and VPN_CONFIG_REF not set.
                 Use: /vpn --connect <profile>
                 Or set VPN_CONFIG_REF=op://VPN/PROFILE in .devcontainer/.env"
        fi
        ref="${ref#op://}"
        profile=$(echo "$ref" | cut -d'/' -f2)

  3_detect_protocol:
    action: "Determine protocol from 1Password item tags"
    command: |
      vault="${VPN_VAULT:-VPN}"
      doc_item=$(op item list --vault "$vault" --categories DOCUMENT --format json \
        | jq -r --arg t "$profile" '.[] | select(.title==$t)')
      doc_uuid=$(echo "$doc_item" | jq -r '.id')
      # Detect protocol from tags (default: openvpn) - zsh-compatible
      protocol="openvpn"
      echo "$doc_item" | jq -r '.tags // [] | .[]' | while IFS= read -r tag; do
        case "$tag" in
          wireguard|ipsec|pptp) protocol="$tag"; break ;;
        esac
      done

  4_resolve_credentials:
    action: "Get LOGIN item (if applicable)"
    condition: "protocol != wireguard"
    command: |
      login_uuid=$(op item list --vault "$vault" --categories LOGIN --format json \
        | jq -r --arg t "$profile" '.[] | select(.title==$t) | .id')
      if [ -n "$login_uuid" ]; then
        vpn_user=$(op read "op://$vault/$login_uuid/username")
        vpn_pass=$(op read "op://$vault/$login_uuid/password")
        printf '%s\n%s\n' "$vpn_user" "$vpn_pass" > /tmp/vpn-auth.txt
        chmod 600 /tmp/vpn-auth.txt
      fi
    note: "NEVER log passwords"

  5_download_config:
    action: "Download config by UUID"
    command: |
      case "$protocol" in
        openvpn)
          mkdir -p ~/.config/openvpn
          op document get "$doc_uuid" --vault "$vault" > ~/.config/openvpn/client.ovpn
          chmod 600 ~/.config/openvpn/client.ovpn
          ;;
        wireguard)
          mkdir -p ~/.config/wireguard
          op document get "$doc_uuid" --vault "$vault" > ~/.config/wireguard/wg0.conf
          chmod 600 ~/.config/wireguard/wg0.conf
          ;;
        ipsec)
          mkdir -p ~/.config/strongswan
          op document get "$doc_uuid" --vault "$vault" > ~/.config/strongswan/ipsec.conf
          chmod 600 ~/.config/strongswan/ipsec.conf
          ;;
        pptp)
          mkdir -p ~/.config/pptp
          op document get "$doc_uuid" --vault "$vault" > ~/.config/pptp/tunnel.conf
          chmod 600 ~/.config/pptp/tunnel.conf
          ;;
      esac

  6_connect:
    action: "Start VPN (protocol-specific)"
    commands:
      openvpn: |
        sudo openvpn \
          --config ~/.config/openvpn/client.ovpn \
          --daemon ovpn-client \
          --log /tmp/openvpn.log \
          --script-security 2 \
          --up /etc/openvpn/update-dns \
          --down /etc/openvpn/update-dns \
          --keepalive 10 60 \
          --connect-retry 5 \
          --connect-retry-max 0 \
          --persist-tun \
          --persist-key \
          --resolv-retry infinite \
          --auth-user-pass /tmp/vpn-auth.txt
      wireguard: |
        sudo wg-quick up ~/.config/wireguard/wg0.conf
      ipsec: |
        sudo cp ~/.config/strongswan/ipsec.conf /etc/ipsec.d/profile.conf
        [ -f /tmp/vpn-auth.txt ] && sudo cp /tmp/vpn-auth.txt /etc/ipsec.d/profile.secrets
        sudo ipsec restart
        # Extract connection name from config
        conn_name=$(grep -oP '(?<=^conn )\S+' ~/.config/strongswan/ipsec.conf | head -1)
        sudo ipsec up "$conn_name"
      pptp: |
        # Extract server from config and connect
        sudo pppd call tunnel nodetach &
        # Or: sudo pptp <server> file ~/.config/pptp/tunnel.conf

  7_verify:
    action: "Wait for interface (protocol-specific)"
    commands:
      openvpn: "Wait for tun0 (up to 15s)"
      wireguard: "Wait for wg0 (up to 10s)"
      ipsec: "Wait for ipsec status ESTABLISHED (up to 15s)"
      pptp: "Wait for ppp0 (up to 15s)"
    timeout: "15 seconds"
```

**Output --connect (OpenVPN):**

```
═══════════════════════════════════════════════════════════════════
  /vpn --connect HOME
═══════════════════════════════════════════════════════════════════

  Profile  : HOME
  Protocol : OpenVPN
  Vault    : VPN
  Config   : Downloaded (.ovpn)
  Creds    : Resolved (username + password)
  Status   : CONNECTED
  Interface: tun0 (10.8.0.2)

═══════════════════════════════════════════════════════════════════
```

**Output --connect (WireGuard):**

```
═══════════════════════════════════════════════════════════════════
  /vpn --connect OFFICE
═══════════════════════════════════════════════════════════════════

  Profile  : OFFICE
  Protocol : WireGuard
  Vault    : VPN
  Config   : Downloaded (.conf)
  Creds    : N/A (keys in config)
  Status   : CONNECTED
  Interface: wg0 (10.0.0.2)

═══════════════════════════════════════════════════════════════════
```

---

## Action: --disconnect

**Stop VPN and clean up (auto-detects active protocol):**

```yaml
disconnect_workflow:
  1_detect_running:
    action: "Detect which VPN protocol is active"
    checks:
      - "pgrep -x openvpn → OpenVPN"
      - "ip link show wg0 → WireGuard"
      - "pgrep -x charon → IPsec"
      - "pgrep -x pppd → PPTP"
    on_none: |
      INFO "No VPN is running. Nothing to disconnect."
      return

  2_stop:
    action: "Stop detected VPN"
    commands:
      openvpn: "sudo killall openvpn 2>/dev/null || true"
      wireguard: "sudo wg-quick down wg0 2>/dev/null || sudo wg-quick down ~/.config/wireguard/wg0.conf 2>/dev/null || true"
      ipsec: |
        conn_name=$(grep -oP '(?<=^conn )\S+' /etc/ipsec.d/profile.conf 2>/dev/null | head -1)
        [ -n "$conn_name" ] && sudo ipsec down "$conn_name" 2>/dev/null || true
        sudo ipsec stop 2>/dev/null || true
      pptp: "sudo killall pppd 2>/dev/null || true"

  3_cleanup:
    action: "Remove credentials and temp files"
    command: |
      rm -f /tmp/vpn-auth.txt
      sudo rm -f /etc/ipsec.d/profile.conf /etc/ipsec.d/profile.secrets 2>/dev/null || true
    note: "Ephemeral credentials cleaned up"

  4_verify:
    action: "Confirm disconnection"
    command: |
      ! pgrep -x openvpn && ! ip link show wg0 2>/dev/null && \
      ! pgrep -x charon && ! pgrep -x pppd
```

**Output --disconnect:**

```
═══════════════════════════════════════════════════════════════════
  /vpn --disconnect
═══════════════════════════════════════════════════════════════════

  Protocol : OpenVPN (detected)
  Action   : Stopped openvpn daemon
  Cleanup  : /tmp/vpn-auth.txt removed
  Status   : DISCONNECTED

═══════════════════════════════════════════════════════════════════
```

---

## Action: --status

**Check current VPN state (all protocols):**

```yaml
status_workflow:
  1_check_openvpn:
    action: "Check OpenVPN"
    commands:
      - "pgrep -x openvpn → PID"
      - "ip addr show tun0 → IP"
      - "sudo tail -5 /tmp/openvpn.log → logs"

  2_check_wireguard:
    action: "Check WireGuard"
    commands:
      - "ip link show wg0 → interface"
      - "sudo wg show wg0 → stats"

  3_check_ipsec:
    action: "Check IPsec"
    commands:
      - "sudo ipsec status → connections"

  4_check_pptp:
    action: "Check PPTP"
    commands:
      - "pgrep -x pppd → PID"
      - "ip addr show ppp0 → IP"
```

**Output --status (connected, OpenVPN):**

```
═══════════════════════════════════════════════════════════════════
  /vpn --status
═══════════════════════════════════════════════════════════════════

  Protocol : OpenVPN
  Process  : openvpn (PID: 1234)
  Interface: tun0 (10.8.0.2)
  Uptime   : Running

  Recent logs:
    [timestamp] Initialization Sequence Completed
    [timestamp] Data Channel: cipher 'AES-256-GCM'

═══════════════════════════════════════════════════════════════════
```

**Output --status (disconnected):**

```
═══════════════════════════════════════════════════════════════════
  /vpn --status
═══════════════════════════════════════════════════════════════════

  OpenVPN  : not running
  WireGuard: wg0 not found
  IPsec    : no connections
  PPTP     : not running
  Status   : DISCONNECTED

  Hint: Use /vpn --connect to start VPN

═══════════════════════════════════════════════════════════════════
```
