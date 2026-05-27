# Protocol Specifics

## Supported Protocols

| Protocol | Extension | Tag | Credentials | Client |
|----------|-----------|-----|-------------|--------|
| OpenVPN | .ovpn | `openvpn` (or no tag = default) | LOGIN (username + password) | `openvpn` |
| WireGuard | .conf | `wireguard` | N/A (keys in config) | `wg` / `wg-quick` |
| IPsec/IKEv2 | - | `ipsec` | LOGIN (username + password) | `ipsec` (StrongSwan) |
| PPTP | - | `pptp` | LOGIN (username + password) | `pptp` / `pppd` |

---

## 1Password Convention (vault "VPN")

Each profile = items with same title in the vault:
- `PROFILE` (DOCUMENT) = config file (.ovpn, .conf, etc.)
- `PROFILE` (LOGIN) = credentials (optional, not needed for WireGuard)
- Tags on DOCUMENT determine protocol: `openvpn` (default), `wireguard`, `ipsec`, `pptp`

---

## OpenVPN Details

**Config path:** `~/.config/openvpn/client.ovpn`
**Process:** `openvpn` (daemon mode)
**Interface:** `tun0`
**Log:** `/tmp/openvpn.log`

```bash
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
```

**Disconnect:** `sudo killall openvpn 2>/dev/null || true`
**Status check:** `pgrep -x openvpn`, `ip addr show tun0`

---

## WireGuard Details

**Config path:** `~/.config/wireguard/wg0.conf`
**Interface:** `wg0`
**No credentials needed** (keys embedded in config)

```bash
# Connect
sudo wg-quick up ~/.config/wireguard/wg0.conf

# Disconnect
sudo wg-quick down wg0 2>/dev/null || sudo wg-quick down ~/.config/wireguard/wg0.conf 2>/dev/null || true

# Status
ip link show wg0
sudo wg show wg0
```

---

## IPsec/IKEv2 (StrongSwan) Details

**Config path:** `~/.config/strongswan/ipsec.conf`
**Runtime config:** `/etc/ipsec.d/profile.conf`
**Runtime secrets:** `/etc/ipsec.d/profile.secrets`

```bash
# Connect
sudo cp ~/.config/strongswan/ipsec.conf /etc/ipsec.d/profile.conf
[ -f /tmp/vpn-auth.txt ] && sudo cp /tmp/vpn-auth.txt /etc/ipsec.d/profile.secrets
sudo ipsec restart
conn_name=$(grep -oP '(?<=^conn )\S+' ~/.config/strongswan/ipsec.conf | head -1)
sudo ipsec up "$conn_name"

# Disconnect
conn_name=$(grep -oP '(?<=^conn )\S+' /etc/ipsec.d/profile.conf 2>/dev/null | head -1)
[ -n "$conn_name" ] && sudo ipsec down "$conn_name" 2>/dev/null || true
sudo ipsec stop 2>/dev/null || true

# Status
sudo ipsec status
```

---

## PPTP Details

**Config path:** `~/.config/pptp/tunnel.conf`
**Process:** `pppd`
**Interface:** `ppp0`

```bash
# Connect
sudo pppd call tunnel nodetach &

# Disconnect
sudo killall pppd 2>/dev/null || true

# Status
pgrep -x pppd
ip addr show ppp0
```

**WARNING:** PPTP is insecure. Recommend WireGuard or IPsec as alternatives.

---

## SAFEGUARDS (ABSOLUTE)

| Action | Status | Reason |
|--------|--------|--------|
| Multiple VPNs active simultaneously | BLOCKED | Auto-disconnect current before connecting new |
| Log passwords or credentials | FORBIDDEN | Security |
| Commit config files | BLOCKED | Protected by `.gitignore` |
| Store credentials outside `/tmp` | FORBIDDEN | Ephemeral only |
| Skip Phase 1 (Peek) | FORBIDDEN | Must verify prerequisites |
| Connect without confirming profile | FORBIDDEN | Must resolve and display profile name |
| Run without `OP_SERVICE_ACCOUNT_TOKEN` | BLOCKED | Auth required for 1Password |
| Use PPTP without warning | WARNING | PPTP is insecure, recommend alternatives |
