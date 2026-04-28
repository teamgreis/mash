# Connection Establishment Behavior

> Precise specification of the complete connection flow from discovery to operational

**Status:** Draft
**Created:** 2025-01-25

---

## 1. Overview

This document specifies the complete connection establishment flow, bridging discovery, commissioning, and operational phases. It addresses the transition points between these phases.

**Scope:**
- mDNS record format (complete)
- IPv6 address resolution
- TLS handshake during commissioning
- PASE to operational session transition
- Certificate validation for operational TLS
- Address change handling

---

## 2. mDNS Record Format

MASH uses two operational service types plus a pairing-request signaling service (see `discovery.md` for full details):

| Service Type | Purpose | When Present |
|--------------|---------|--------------|
| `_mash-comm._tcp` | Commissionable discovery | Commissioning window open |
| `_mash._tcp` | Operational discovery | Device has zone(s) |
| `_mashp._udp` | Pairing request | Controller seeks specific device |

### 2.1 Commissionable Discovery Records (`_mash-comm._tcp`)

During commissioning, device publishes:

**PTR Record:**
```
_mash-comm._tcp.local.  PTR  MASH-1234._mash-comm._tcp.local.
```

**SRV Record:**
```
MASH-1234._mash-comm._tcp.local.  SRV  0 0 8443 evse-001.local.
```

**TXT Record:**
```
MASH-1234._mash-comm._tcp.local.  TXT  "D=1234" "cat=3" "serial=WB-001234" "brand=ChargePoint" "model=Home Flex"
```

**AAAA Record:**
```
evse-001.local.  AAAA  fe80::1234:5678:9abc:def0
evse-001.local.  AAAA  2001:db8::1234:5678:9abc:def0
```

### 2.2 Operational Discovery Records (`_mash._tcp`)

After commissioning, device publishes (one per zone):

**Instance name:** `<zone-id>-<device-id>` where both are fingerprint-derived (64 bits each):
- Zone ID: First 16 hex chars of SHA-256(Zone CA cert DER)
- Device ID: First 16 hex chars of SHA-256(device op cert public key DER)

**PTR Record:**
```
_mash._tcp.local.  PTR  A1B2C3D4E5F6A7B8-F9E8D7C6B5A49382._mash._tcp.local.
```

**SRV Record:**
```
A1B2C3D4E5F6A7B8-F9E8D7C6B5A49382._mash._tcp.local.  SRV  0 0 8443 evse-001.local.
```

**TXT Record:**
```
A1B2C3D4E5F6A7B8-F9E8D7C6B5A49382._mash._tcp.local.  TXT  "ZI=A1B2C3D4E5F6A7B8" "DI=F9E8D7C6B5A49382"
```

| Field | Value | Description |
|-------|-------|-------------|
| Priority | 0 | Single instance, no failover |
| Weight | 0 | No load balancing |
| Port | 8443 | MASH port (commissioning and operational, routed by ALPN) |
| Target | hostname.local | Device hostname |

### 2.3 Address Records

Devices MUST publish at least one AAAA record. MAY publish multiple:
- Link-local address (fe80::/10) - always available
- Global/ULA address - if configured

```
evse-001.local.  AAAA  fe80::1234:5678:9abc:def0
evse-001.local.  AAAA  2001:db8::1234:5678:9abc:def0
```

### 2.4 Address Selection

When multiple AAAA records exist, controller selects:

1. **Prefer global/ULA** over link-local (routable across subnets)
2. **Prefer ULA** (fd00::/8) over global (more stable in home networks)
3. **Fall back to link-local** if no others available

**Link-local connection:**
- Requires interface specification (e.g., `fe80::1%eth0`)
- Controller must determine correct interface
- Works without router/DHCP infrastructure

### 2.5 Record TTL (Time-To-Live)

Uniform 120 s for all record types (DEC-089):

| Record | TTL | Rationale |
|--------|-----|-----------|
| PTR | 120 s (2 min) | IoT churn (zone changes, factory resets); bounded zombie window |
| SRV | 120 s (2 min) | Port changes rare but possible |
| AAAA | 120 s (2 min) | Address may change |
| TXT | 120 s (2 min) | `EP`/`FW`/`VP` (DEC-088) change in normal operation |

Implementations MAY use longer TTLs only with explicit per-deployment justification; the conformant default is 120 s.

The 120 s TTL is the **slow-path** stale-record recovery floor (used when a device cannot send a goodbye). The **fast path** is mandatory client cache invalidation on dial failure — see "Cache invalidation triggers" later in this document.

### 2.6 Record Updates

**When address changes:**
1. Send mDNS "goodbye" (TTL=0) for old AAAA
2. Send mDNS announcement for new AAAA (3 times)
3. Existing connections will break, clients reconnect

**When entering commissioning mode:**
1. Register `_mashc._udp` service instance
2. Send mDNS announcement (3 times)

**When commissioning completes:**
1. Deregister `_mashc._udp` service instance
2. Register `_mash._tcp` service instance for new zone
3. Send mDNS announcements

---

## 3. TLS During Commissioning

### 3.1 The Problem

During commissioning, the device has no operational certificate yet. What certificate does it present for TLS?

### 3.2 Solution: Temporary Self-Signed Certificate

**Device behavior on boot (if no operational certs):**

1. Generate temporary key pair (P-256)
2. Create self-signed certificate:
   ```
   Subject: CN=MASH-<discriminator>
   Issuer: CN=MASH-<discriminator> (self-signed)
   Validity: 1 day
   Key Usage: Digital Signature, Key Encipherment
   ```
3. Use this cert for TLS during commissioning

**Certificate lifecycle:**
- Generated at boot if no operational certs exist
- Regenerated on factory reset
- Deleted after first operational cert is installed
- Never used for operational connections

### 3.3 Commissioning TLS Handshake

```
Controller                                    Device
    │                                            │
    │──── ClientHello ──────────────────────────►│
    │     - TLS 1.3                               │
    │     - ALPN: "mash/1"                        │
    │     - No client cert (no Certificate msg)  │
    │                                            │
    │◄─── ServerHello ──────────────────────────┤
    │     - TLS 1.3                               │
    │     - Selected cipher                       │
    │                                            │
    │◄─── Certificate (self-signed) ────────────┤
    │     - CN=MASH-<discriminator>              │
    │                                            │
    │◄─── CertificateVerify ────────────────────┤
    │                                            │
    │◄─── Finished ─────────────────────────────┤
    │                                            │
    │──── Finished ─────────────────────────────►│
    │                                            │
    │     [TLS session established]              │
    │     [PASE messages now encrypted]          │
```

### 3.4 Controller Certificate Validation (Commissioning)

During commissioning, controller MUST:

1. **Accept self-signed certificate** - device has no Zone CA cert yet
2. **Verify CN matches discriminator** - CN=MASH-<D> where D is from QR code
3. **Skip chain validation** - no CA to verify against
4. **Proceed to PASE** - real authentication happens via SPAKE2+

**Security note:** The TLS handshake during commissioning does NOT authenticate the device. SPAKE2+ provides authentication via the setup code. TLS only provides encryption.

### 3.5 Why This Is Secure

| Attack | Mitigation |
|--------|------------|
| MITM during TLS | SPAKE2+ will fail (attacker doesn't know setup code) |
| Rogue device | SPAKE2+ will fail (different setup code) |
| Replay | SPAKE2+ uses random X/Y, fresh each session |
| Eavesdropping | TLS encrypts PASE messages |

---

## 4. PASE to Operational Transition

### 4.1 The Transition Problem

After PASE completes and operational cert is installed:
- Current TLS session used self-signed cert (not authenticated)
- Need to establish mutually authenticated session
- How do we transition?

### 4.2 Solution: Close and Reconnect

**After CERT_INSTALL succeeds:**

```
Controller                                    Device
    │                                            │
    │◄─── CERT_ACK {status: 0} ─────────────────┤
    │                                            │
    │──── Close {reason: "commissioning_complete"}►│
    │                                            │
    │◄─── Close_ACK ────────────────────────────┤
    │                                            │
    │     [TCP connection closed]                │
    │                                            │
    │     [Controller waits 1 second]            │
    │                                            │
    │──── New TCP Connect ──────────────────────►│
    │                                            │
    │──── TLS with operational certs ───────────►│
    │     (mutual authentication)                │
    │                                            │
    │     [OPERATIONAL session established]      │
```

### 4.3 Why Reconnect (Not Upgrade)?

| Approach | Pros | Cons |
|----------|------|------|
| **Reconnect** (chosen) | Clean state, simple implementation | Brief disconnect |
| TLS renegotiation | No disconnect | TLS 1.3 doesn't support renegotiation |
| Session upgrade | Continuous | Complex state management |

**Reconnect is cleanest:** The commissioning connection was unauthenticated. Starting fresh with mutual TLS is clearer and simpler.

### 4.4 Device Behavior During Transition

1. Receive CERT_ACK with success
2. Store operational cert and Zone CA cert
3. Receive Close message
4. Send Close_ACK
5. Close TCP connection
6. Update mDNS:
   - Deregister `_mashc._udp` service instance
   - Extract device ID from operational cert CommonName (assigned by controller)
   - Register `_mash._tcp` service instance with `<zone-id>-<device-id>` name
   - Update TXT records for operational mode (ZI, DI)
7. Listen for new connection
8. On new TLS connection, require mutual cert auth

### 4.5 Controller Behavior During Transition

1. Send Close after receiving CERT_ACK
2. Wait for Close_ACK
3. Close TCP connection
4. Wait 1 second (allow device to update state)
5. Browse mDNS for new instance name (or use same address)
6. Connect with operational TLS (present Zone CA-signed cert)
7. Verify device cert is signed by same Zone CA

---

## 5. Operational TLS Handshake

### 5.1 Mutual Authentication

```
Controller                                    Device
    │                                            │
    │──── ClientHello ──────────────────────────►│
    │     - TLS 1.3                               │
    │     - ALPN: "mash/1"                        │
    │     - SNI: <device-id> (optional)          │
    │                                            │
    │◄─── ServerHello ──────────────────────────┤
    │                                            │
    │◄─── CertificateRequest ───────────────────┤
    │     (device requests client cert)          │
    │                                            │
    │◄─── Certificate (device op cert) ─────────┤
    │                                            │
    │◄─── CertificateVerify ────────────────────┤
    │                                            │
    │◄─── Finished ─────────────────────────────┤
    │                                            │
    │──── Certificate (controller op cert) ─────►│
    │                                            │
    │──── CertificateVerify ────────────────────►│
    │                                            │
    │──── Finished ─────────────────────────────►│
    │                                            │
    │     [Mutual TLS established]               │
    │     [Both certs verified against Zone CA]  │
```

### 5.2 Certificate Validation Algorithm

**Controller validates device certificate:**

```python
def validate_device_cert(device_cert: X509, zone_ca_cert: X509) -> bool:
    """Validate device's operational certificate."""

    # 1. Check certificate is not expired
    if device_cert.not_before > now() or device_cert.not_after < now():
        return False  # Certificate expired or not yet valid

    # 2. Verify signature by Zone CA
    if not device_cert.verify_signature(zone_ca_cert.public_key):
        return False  # Not signed by our Zone CA

    # 3. Check issuer matches Zone CA subject
    if device_cert.issuer != zone_ca_cert.subject:
        return False  # Issuer mismatch

    # 4. Check key usage
    if not device_cert.has_key_usage(KEY_USAGE_DIGITAL_SIGNATURE):
        return False  # Invalid key usage

    # 5. Check extended key usage (if present)
    if device_cert.has_ext_key_usage():
        if not device_cert.has_ext_key_usage(EXT_KEY_USAGE_SERVER_AUTH):
            return False  # Wrong extended key usage

    return True
```

**Device validates controller certificate:**

```python
def validate_controller_cert(controller_cert: X509, zone_ca_cert: X509) -> bool:
    """Validate controller's operational certificate."""

    # 1. Check certificate is not expired
    if controller_cert.not_before > now() or controller_cert.not_after < now():
        return False

    # 2. Verify signature by Zone CA
    if not controller_cert.verify_signature(zone_ca_cert.public_key):
        return False  # Controller not in our zone

    # 3. Check issuer matches Zone CA subject
    if controller_cert.issuer != zone_ca_cert.subject:
        return False

    # 4. Check key usage
    if not controller_cert.has_key_usage(KEY_USAGE_DIGITAL_SIGNATURE):
        return False

    # 5. Check extended key usage (if present)
    if controller_cert.has_ext_key_usage():
        if not controller_cert.has_ext_key_usage(EXT_KEY_USAGE_CLIENT_AUTH):
            return False

    return True
```

### 5.3 Zone Membership Verification

Both parties verify they're in the same zone:

```
Zone membership = cert.issuer == local_zone_ca.subject
                  AND cert.verify_signature(local_zone_ca.public_key)
```

If verification fails:
- TLS handshake fails with certificate_unknown alert
- Connection closed
- No MASH messages exchanged

### 5.4 Clock Skew Handling

Certificate validation requires synchronized clocks.

**Tolerance:** Accept certificates within +/- 300 seconds of stated validity times.

```python
CLOCK_SKEW_TOLERANCE = 300  # seconds

def is_cert_valid(cert: X509) -> bool:
    now = current_time()
    return (cert.not_before - CLOCK_SKEW_TOLERANCE <= now <=
            cert.not_after + CLOCK_SKEW_TOLERANCE)
```

---

## 6. IPv6 Address Handling

### 6.1 Address Types

| Address Type | Range | Use Case |
|--------------|-------|----------|
| Link-local | fe80::/10 | Commissioning, same subnet |
| ULA | fd00::/8 | Home networks, stable |
| Global | 2000::/3 | Routable, may change |

### 6.2 Address Selection Priority

Controller should prefer addresses in this order:

1. **ULA (fd00::/8)** - Most stable in residential
2. **Global unicast** - Routable but may change
3. **Link-local (fe80::/10)** - Always available, same subnet only

### 6.3 Address Change Handling

**Device behavior when address changes:**

1. Update AAAA record in mDNS
2. Keep existing connections (TCP doesn't break immediately)
3. If connection breaks, wait for controller to reconnect

**Controller behavior when connection breaks:**

1. Detect connection loss (keep-alive failure)
2. Enter RECONNECTING state
3. Re-resolve device via mDNS (get new AAAA)
4. Connect to new address
5. Re-authenticate with mutual TLS
6. Re-establish subscriptions

### 6.4 Link-Local Interface Binding

For link-local addresses, controller must specify interface:

```
Connect to: fe80::1234:5678:9abc:def0%eth0
                                    ^^^^^^
                                    Interface identifier
```

**Controller determines interface:**
1. mDNS response arrives on specific interface
2. Use that interface for link-local connection
3. Store interface with device record

### 6.5 Multi-Interface Behavior

Devices may have multiple network interfaces (e.g., Ethernet + WiFi). This section specifies behavior when interfaces change.

**Note:** Matter has known limitations with multi-interface handling (see [connectedhomeip #32512](https://github.com/project-chip/connectedhomeip/issues/32512)). MASH takes a simpler approach: publish all addresses, try all addresses.

#### 6.5.1 Device Behavior (mDNS)

**Rule:** Device MUST publish AAAA records for ALL active interfaces.

```
evse-001.local.  AAAA  fe80::1111:1111:1111:1111  ; eth0 link-local
evse-001.local.  AAAA  fd00::1111:1111:1111:1111  ; eth0 ULA
evse-001.local.  AAAA  fe80::2222:2222:2222:2222  ; wlan0 link-local
evse-001.local.  AAAA  fd00::2222:2222:2222:2222  ; wlan0 ULA
```

**When interface goes DOWN:**
1. Send mDNS goodbye (TTL=0) for all addresses on that interface
2. Keep AAAA records for remaining interfaces
3. Existing connections on that interface will break (TCP timeout)

**When interface comes UP:**
1. Wait for address assignment (SLAAC/DHCPv6)
2. Wait debounce period: **2 seconds** (avoid flapping)
3. Send mDNS announcement for new addresses (3 times)

**Debouncing:** If interface goes down and up within 2 seconds, suppress mDNS goodbye/announcement. This prevents mDNS storms during brief network glitches.

#### 6.5.2 Controller Behavior (Connection)

**Rule:** Controller MUST collect all AAAA records and try addresses until one works.

**Address collection during mDNS resolution:**
```python
def resolve_device(instance_name: str) -> list[Address]:
    """Resolve device to list of addresses."""
    addresses = []

    # Collect ALL AAAA records for the hostname
    for record in mdns_query(instance_name):
        if record.type == "AAAA":
            addresses.append(Address(
                ip=record.ip,
                interface=record.receiving_interface  # For link-local
            ))

    # Sort by preference (ULA > Global > Link-local)
    return sort_by_preference(addresses)
```

**Connection attempt sequence:**
```python
def connect_to_device(addresses: list[Address], timeout_per_addr: float = 5.0) -> Connection:
    """Try all addresses until one succeeds."""
    last_error = None

    for addr in addresses:
        try:
            # For link-local, include interface
            target = f"{addr.ip}%{addr.interface}" if addr.is_link_local else addr.ip

            conn = tcp_connect(target, port=8443, timeout=timeout_per_addr)
            return conn  # Success - use this address

        except (ConnectionRefused, Timeout, NetworkUnreachable) as e:
            last_error = e
            continue  # Try next address

    raise ConnectionFailed(f"All {len(addresses)} addresses failed", last_error)
```

**Timeout per address:** 5 seconds (allows time for TCP handshake but doesn't block too long)

#### 6.5.3 Reconnection on Interface Change

When connection breaks due to interface change:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│  Interface Change Reconnection Flow                                          │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Controller                          Device (eth0 + wlan0)                   │
│      │                                      │                                │
│      │◄═══ Connected via fd00::1111 (eth0) ═│                                │
│      │                                      │                                │
│      │                          [eth0 cable unplugged]                       │
│      │                                      │                                │
│      │    [TCP timeout / keep-alive fails]  │──► mDNS goodbye for eth0 addrs │
│      │                                      │                                │
│      │──► Enter RECONNECTING state          │                                │
│      │                                      │                                │
│      │─── mDNS re-resolve ─────────────────►│                                │
│      │                                      │                                │
│      │◄── AAAA: fd00::2222 (wlan0 only) ────│                                │
│      │                                      │                                │
│      │─── TCP connect fd00::2222 ──────────►│                                │
│      │                                      │                                │
│      │─── TLS mutual auth ─────────────────►│                                │
│      │                                      │                                │
│      │◄═══ Connected via fd00::2222 (wlan0) │                                │
│      │                                      │                                │
│      │─── Re-subscribe ────────────────────►│                                │
│      │                                      │                                │
│      │◄── Priming report ──────────────────│                                │
│      │                                      │                                │
│      │    [OPERATIONAL - via wlan0]         │                                │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

#### 6.5.4 Address Preference Within Multi-Interface

When device has multiple interfaces, controller sorts addresses:

| Priority | Criteria | Rationale |
|----------|----------|-----------|
| 1 | ULA (fd00::/8) | Most stable, home network local |
| 2 | Global unicast (2000::/3) | Routable, may work cross-subnet |
| 3 | Link-local (fe80::/10) | Last resort, same-subnet only |

**Within same priority:** No preference between interfaces. First working address wins.

**No LAN vs WiFi preference:** Controller cannot reliably determine which interface is "better" (wired vs wireless). Network topology varies. Simply try all and use first working.

#### 6.5.5 Edge Cases

**All addresses fail:**
- Report error to user: "Device unreachable"
- Continue periodic retry (exponential backoff)
- Device may be powered off, network isolated, or crashed

**Address works but TLS fails:**
- Different device at that address (rare, IP reuse)
- Certificate mismatch → try next address
- If all addresses have TLS failure → report "Device authentication failed"

**mDNS returns no addresses:**
- Device may have lost all network connectivity
- Device may have deregistered (factory reset, zone removal)
- Continue periodic mDNS browse with backoff

#### 6.5.6 Implementation Notes

**Caching:** Controller MAY cache last-known-good address and try it first before full mDNS resolution. But MUST fall back to mDNS if cached address fails.

**Parallel connection attempts:** Controller MAY attempt connections to multiple addresses in parallel (e.g., start next attempt after 1 second if first hasn't succeeded). This reduces total connection time but increases network load.

**Interface tracking:** For link-local addresses, controller MUST track which interface the mDNS response arrived on. This interface must be used for the connection (zone ID in socket address).

---

## 7. Complete Connection Sequence

### 7.1 First-Time Commissioning (End-to-End)

```
┌────────────────────────────────────────────────────────────────────────────┐
│                    COMPLETE FIRST-TIME CONNECTION SEQUENCE                  │
├────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  Controller                          Network                      Device   │
│      │                                  │                            │     │
│  1.  │ ─── Scan QR code ─────────────────────────────────────────────────  │
│      │     Parse: D=1234, setupcode, VP                              │     │
│      │                                  │                            │     │
│  2.  │ ─── mDNS Query ─────────────────►│                            │     │
│      │     _mashc._udp.local PTR?       │                            │     │
│      │                                  │                            │     │
│      │ ◄── mDNS Response ───────────────┤◄──────────────────────────┤     │
│      │     PTR MASH-1234._mashc._udp    │                            │     │
│      │     SRV 0 0 8443 evse.local      │                            │     │
│      │     AAAA fe80::1234              │                            │     │
│      │     TXT D=1234 cat=3 serial=...  │                            │     │
│      │                                  │                            │     │
│  3.  │     [Verify: D matches QR discriminator]                       │     │
│      │                                  │                            │     │
│  4.  │ ═══ TCP Connect ════════════════════════════════════════════►│     │
│      │     [fe80::1234]:8443            │                            │     │
│      │                                  │                            │     │
│  5.  │ ─── TLS ClientHello ────────────────────────────────────────►│     │
│      │     ALPN: mash/1, no client cert │                            │     │
│      │                                  │                            │     │
│      │ ◄── TLS ServerHello + Cert ─────────────────────────────────┤     │
│      │     Cert: CN=MASH-1234 (self-signed)                         │     │
│      │                                  │                            │     │
│      │     [Accept self-signed, verify CN=MASH-<D>]                 │     │
│      │                                  │                            │     │
│      │ ─── TLS Finished ───────────────────────────────────────────►│     │
│      │                                  │                            │     │
│      │     [TLS ESTABLISHED - encrypted but not authenticated]      │     │
│      │                                  │                            │     │
│  6.  │ ─── PASE_X {X, context} ────────────────────────────────────►│     │
│      │                                  │                            │     │
│      │ ◄── PASE_Y {Y, verifier_device} ────────────────────────────┤     │
│      │                                  │                            │     │
│      │ ─── PASE_VERIFY {verifier_ctrl} ────────────────────────────►│     │
│      │                                  │                            │     │
│      │ ◄── PASE_CONFIRM {status: 0} ───────────────────────────────┤     │
│      │                                  │                            │     │
│      │     [PASE VERIFIED - device authenticated via setup code]    │     │
│      │                                  │                            │     │
│  7.  │ ─── ATTESTATION_REQ ────────────────────────────────────────►│     │
│      │                                  │                            │     │
│      │ ◄── ATTESTATION_RSP {device_cert, chain} ───────────────────┤     │
│      │                                  │  (optional)                │     │
│      │                                  │                            │     │
│  8.  │ ─── CSR_REQ {nonce} ────────────────────────────────────────►│     │
│      │                                  │                            │     │
│      │ ◄── CSR_RSP {csr} ──────────────────────────────────────────┤     │
│      │                                  │                            │     │
│      │     [Sign CSR with Zone CA]      │                            │     │
│      │                                  │                            │     │
│  9.  │ ─── CERT_INSTALL {op_cert, zone_ca} ────────────────────────►│     │
│      │                                  │                            │     │
│      │ ◄── CERT_ACK {status: 0} ───────────────────────────────────┤     │
│      │                                  │                            │     │
│      │     [COMMISSIONING COMPLETE]     │                            │     │
│      │                                  │                            │     │
│ 10.  │ ─── Close {reason: commissioning_complete} ─────────────────►│     │
│      │                                  │                            │     │
│      │ ◄── Close_ACK ──────────────────────────────────────────────┤     │
│      │                                  │                            │     │
│      │     [TCP CLOSED]                 │                            │     │
│      │                                  │                            │     │
│      │     [Wait 1 second]              │      [Update mDNS]         │     │
│      │                                  │      [_mashc gone,         │     │
│      │                                  │       _mash._tcp added]    │     │
│      │                                  │                            │     │
│ 11.  │ ═══ TCP Connect (new) ══════════════════════════════════════►│     │
│      │                                  │                            │     │
│ 12.  │ ─── TLS ClientHello ────────────────────────────────────────►│     │
│      │     ALPN: mash/1                 │                            │     │
│      │                                  │                            │     │
│      │ ◄── CertificateRequest ─────────────────────────────────────┤     │
│      │                                  │                            │     │
│      │ ◄── Certificate (device op cert) ───────────────────────────┤     │
│      │                                  │                            │     │
│      │     [Validate: signed by Zone CA, not expired]               │     │
│      │                                  │                            │     │
│      │ ─── Certificate (controller op cert) ───────────────────────►│     │
│      │                                  │                            │     │
│      │     [Device validates: signed by Zone CA]                    │     │
│      │                                  │                            │     │
│      │ ─── TLS Finished ───────────────────────────────────────────►│     │
│      │                                  │                            │     │
│      │     [MUTUAL TLS ESTABLISHED - both authenticated]            │     │
│      │                                  │                            │     │
│ 13.  │ ─── Read {DeviceInfo} ──────────────────────────────────────►│     │
│      │                                  │                            │     │
│      │ ◄── Response {attributes} ──────────────────────────────────┤     │
│      │                                  │                            │     │
│      │     [OPERATIONAL - ready for commands]                       │     │
│      │                                  │                            │     │
└────────────────────────────────────────────────────────────────────────────┘
```

### 7.2 Reconnection (After Disconnect)

```
Controller                                    Device
    │                                            │
    │     [Detect connection loss]               │
    │     [Enter RECONNECTING state]             │
    │                                            │
    │──── mDNS re-resolve (if needed) ──────────►│
    │                                            │
    │◄─── AAAA response ────────────────────────┤
    │                                            │
    │═══ TCP Connect ═══════════════════════════►│
    │                                            │
    │──── TLS with mutual certs ────────────────►│
    │     [No PASE needed - already commissioned]│
    │                                            │
    │     [MUTUAL TLS ESTABLISHED]               │
    │                                            │
    │──── Re-subscribe ─────────────────────────►│
    │                                            │
    │◄─── Priming report ───────────────────────┤
    │                                            │
    │     [OPERATIONAL]                          │
```

### 7.3 Initial Operational Reads After Commissioning

After commissioning completes and operational TLS is established, controller SHOULD read device attributes in a specific order to understand device capabilities before sending commands.

**Recommended read sequence:**

| Order | Feature | Attributes | Purpose |
|-------|---------|------------|---------|
| 1 | DeviceInfo | all | Device identity, capabilities, firmware version |
| 2 | Electrical | phaseCount, connectionType | Understand electrical configuration |
| 3 | EnergyControl | controlState, acceptsLimits, isPausable | Understand current state and capabilities |
| 4 | EnergyControl | effectiveLimits, zoneLimits | Understand current constraints |
| 5 | Measurement | activePower, current (if needed) | Current operating point |
| 6 | Status | operationalState | Device operational status |

**Read algorithm:**

```python
def perform_initial_reads(conn: Connection, device: Device) -> DeviceState:
    """Read initial device state after commissioning."""

    # 1. DeviceInfo - mandatory, always read first
    device_info = conn.read_all_attributes(endpoint=0, feature="DeviceInfo")
    device.device_type = device_info.device_type
    device.firmware = device_info.firmware_version
    device.serial = device_info.serial_number
    device.supported_features = device_info.feature_map

    # 2. Electrical - if supported
    if device.supports_feature("Electrical"):
        electrical = conn.read_all_attributes(endpoint=0, feature="Electrical")
        device.phase_count = electrical.phase_count
        device.connection_type = electrical.connection_type

    # 3. EnergyControl - if supported
    if device.supports_feature("EnergyControl"):
        control = conn.read_all_attributes(endpoint=0, feature="EnergyControl")
        device.control_state = control.control_state
        device.accepts_limits = control.accepts_limits
        device.is_pausable = control.is_pausable
        device.effective_limits = {
            "consumption": control.effective_consumption_limit,
            "production": control.effective_production_limit
        }

    # 4. Measurement - if supported, for initial values
    if device.supports_feature("Measurement"):
        measurement = conn.read_all_attributes(endpoint=0, feature="Measurement")
        device.current_power = measurement.active_power

    # 5. Subscribe for ongoing updates
    setup_subscriptions(conn, device)

    return device.state
```

**Why read before subscribe:**
- Understand capabilities before establishing subscriptions
- Avoid subscribing to unsupported attributes
- Single read is faster than subscription priming for initial sync
- Controller can make decisions based on capabilities

**DeviceInfo attributes (mandatory read):**

```cbor
{
  "deviceType": "EVSE",           // What kind of device
  "vendorName": "ChargePoint",    // Manufacturer
  "productName": "Home Flex",     // Model
  "serialNumber": "WB-001234",    // Unique identifier
  "firmwareVersion": "1.2.3",     // Software version
  "featureMap": 0x001B,           // Supported features bitmap
  "endpointList": [0, 1]          // Available endpoints
}
```

**Handling read errors:**

| Error | Action |
|-------|--------|
| Attribute not found | Feature not supported, skip |
| Timeout | Retry once, then mark device degraded |
| Permission denied | Log warning, continue with other reads |
| Connection lost | Re-enter reconnection flow |

### 7.4 mDNS Resolution Strategy

**Problem:** When to use cached mDNS results vs perform fresh resolution?

**Resolution decision tree:**

```python
def get_device_address(device_id: str, context: str) -> list[Address]:
    """Decide whether to use cache or fresh resolution."""

    if context == "first_connection":
        # Always fresh resolution for new connections
        return mdns_resolve_fresh(device_id)

    if context == "reconnection":
        cached = address_cache.get(device_id)
        if cached and cached.age < timedelta(seconds=30):
            # Try cached first, but have fallback ready
            return [cached.address] + mdns_resolve_fresh(device_id)
        else:
            # Cache too old, resolve fresh
            return mdns_resolve_fresh(device_id)

    if context == "address_failed":
        # Previous address didn't work, must resolve fresh
        address_cache.invalidate(device_id)
        return mdns_resolve_fresh(device_id)
```

**Cache policy:**

| Scenario | Cache Behavior |
|----------|----------------|
| First commissioning | Always fresh mDNS browse |
| Reconnection (< 30s disconnect) | Try cached address first |
| Reconnection (> 30s disconnect) | Fresh mDNS resolution |
| Connection refused | Invalidate cache, fresh resolution |
| TLS handshake failure | Invalidate cache, fresh resolution |
| After device reboot | Fresh resolution (address may have changed) |

**Cache invalidation triggers (MUST, per DEC-089).**

Clients MUST invalidate any cached mDNS entry on each of the following triggers. "Invalidate" means: drop the cached SRV/A/AAAA/TXT entry for the failing instance and re-issue a fresh PTR query before the next dial. Clients MUST NOT retry the same cached SRV target after one of these triggers without first re-resolving.

1. **TCP `connection-refused`** to the SRV-advertised address:port — device has moved or shut down.
2. **TLS certificate mismatch** — the cert is not signed by the expected Zone CA, or the CommonName does not match the expected device-id. Indicates wrong device at that address.
3. **mDNS goodbye received** (TTL=0) — device announcing departure.
4. **Cache age > 120 s** — standard TTL expiry (DEC-089).
5. **Explicit invalidation** — after device removal or factory reset.

The MUST-level requirement is the fast-path complement to the 120 s TTL (§2.5). Together they bound the worst-case stale-record window to a single failed dial in the common case, and to 120 s in the degenerate case (device crash without goodbye, no client tries to dial in the interim).

**mDNS resolution timeout:**

| Phase | Timeout | Rationale |
|-------|---------|-----------|
| PTR query | 5 seconds | Discovery of instance names |
| SRV query | 3 seconds | Get hostname and port |
| AAAA query | 3 seconds | Get addresses |
| **Total resolution** | **10 seconds** | Sum with parallelization |

**Continuous browse mode:**

For active monitoring (EMS watching for devices), controller MAY use continuous mDNS browse:

```python
def start_continuous_browse() -> None:
    """Monitor for device availability changes."""

    def on_service_added(service):
        # New device available
        if service.type == "_mash._tcp":
            notify_device_available(service)

    def on_service_removed(service):
        # Device no longer advertising
        if service.type == "_mash._tcp":
            notify_device_unavailable(service)
            address_cache.invalidate(service.device_id)

    mdns.browse("_mash._tcp.local", on_service_added, on_service_removed)
```

### 7.5 Device Not Found Handling

**Scenario:** Controller scans QR code or attempts to connect but cannot find device via mDNS.

**Browse result classification:**

| Result | Meaning | User Message |
|--------|---------|--------------|
| No PTR responses | No devices in commissioning mode | "No devices found. Ensure device is in pairing mode." |
| PTR but no matching D | Devices found, but wrong discriminator | "Device not found. Check QR code matches device." |
| SRV but no AAAA | DNS misconfiguration | "Device found but address unavailable. Check network." |
| AAAA but connection refused | Device reachable but not listening | "Device not responding. Try rebooting device." |

**Error handling flow:**

```python
def handle_device_not_found(qr_data: QRData, browse_results: list) -> UserError:
    """Provide actionable error message based on failure mode."""

    if not browse_results:
        # No mDNS responses at all
        return UserError(
            code="NO_DEVICES_FOUND",
            message="No devices found in pairing mode",
            suggestions=[
                "Press the pairing button on your device",
                "Ensure device is powered on",
                "Check device is on same network",
                "Wait for device to finish booting"
            ],
            retry_action="browse"
        )

    # Check if any device has matching discriminator
    matching = [r for r in browse_results if r.discriminator == qr_data.discriminator]
    if not matching:
        # Found devices, but not the right one
        found_discriminators = [r.discriminator for r in browse_results]
        return UserError(
            code="DISCRIMINATOR_MISMATCH",
            message=f"Device with discriminator {qr_data.discriminator} not found",
            suggestions=[
                "Verify QR code is for the device you want to pair",
                f"Found devices with discriminators: {found_discriminators}",
                "Device may have been reset (new QR code)"
            ],
            retry_action="scan_qr"
        )

    # Device found but address resolution failed
    device = matching[0]
    if not device.addresses:
        return UserError(
            code="ADDRESS_RESOLUTION_FAILED",
            message="Device found but network address unavailable",
            suggestions=[
                "Device may have network issues",
                "Check router/DHCP configuration",
                "Try restarting device"
            ],
            retry_action="browse"
        )

    # Address available but connection failed
    return UserError(
        code="CONNECTION_FAILED",
        message="Cannot connect to device",
        suggestions=[
            "Device may be busy or unresponsive",
            "Firewall may be blocking connection",
            "Try power-cycling the device"
        ],
        retry_action="connect"
    )
```

**Retry policy for device not found:**

| Attempt | Wait | Action |
|---------|------|--------|
| 1 | 0s | Initial browse |
| 2 | 2s | Retry browse |
| 3 | 5s | Retry browse |
| 4 | 10s | Final retry, then show error |

**Device commissioning window considerations:**

- Commissioning window is 120 seconds
- If browse takes > 30 seconds, warn user window may expire
- After 120 seconds, suggest user re-trigger commissioning mode

**Test cases for device not found:**

| ID | Description | Setup | Expected |
|----|-------------|-------|----------|
| TC-NOTFOUND-1 | No devices | No devices in commissioning | "No devices found" error |
| TC-NOTFOUND-2 | Wrong discriminator | Device D=1111, QR D=2222 | "Discriminator mismatch" error |
| TC-NOTFOUND-3 | Address unavailable | SRV present, no AAAA | "Address unavailable" error |
| TC-NOTFOUND-4 | Connection refused | Address valid, port closed | "Connection failed" error |
| TC-NOTFOUND-5 | Retry success | No device, then device appears | Browse retries, connection succeeds |
| TC-NOTFOUND-6 | Window expired | Browse during last 30s of window | Warning about window expiry |

---

## 8. PICS Items

```
# mDNS records
MASH.S.MDNS.SRV_RECORD=1              # Publishes SRV record
MASH.S.MDNS.AAAA_RECORD=1             # Publishes AAAA record
MASH.S.MDNS.LINK_LOCAL=1              # Supports link-local
MASH.S.MDNS.GLOBAL_ADDR=1             # Supports global address

# TLS commissioning
MASH.S.TLS.SELF_SIGNED_COMM=1         # Uses self-signed for commissioning
MASH.S.TLS.ALPN=mash/1                # ALPN protocol identifier
MASH.S.TLS.MUTUAL_AUTH=1              # Requires mutual auth for operational

# Transition
MASH.S.TRANS.RECONNECT_AFTER_COMM=1   # Reconnects after commissioning
MASH.S.TRANS.WAIT_TIME=1              # Wait time before reconnect (seconds)

# Certificate validation
MASH.S.CERT.CLOCK_SKEW_TOLERANCE=300  # Clock skew tolerance (seconds)
MASH.S.CERT.VALIDATES_CHAIN=1         # Validates certificate chain
MASH.S.CERT.CHECKS_KEY_USAGE=1        # Checks key usage extension

# IPv6
MASH.S.IPV6.LINK_LOCAL_INTERFACE=1    # Handles link-local interface binding
MASH.S.IPV6.ADDRESS_CHANGE=1          # Handles address changes

# Multi-interface (device)
MASH.S.MULTIIF.SUPPORTED=1            # Supports multiple network interfaces
MASH.S.MULTIIF.PUBLISH_ALL=1          # Publishes AAAA for all interfaces
MASH.S.MULTIIF.DEBOUNCE_MS=2000       # Interface change debounce (ms)

# Multi-interface (controller)
MASH.C.MULTIIF.TRY_ALL=1              # Tries all addresses until success
MASH.C.MULTIIF.TIMEOUT_PER_ADDR=5000  # Timeout per address attempt (ms)
MASH.C.MULTIIF.PARALLEL_CONNECT=0     # Parallel connection attempts (0=sequential)
MASH.C.MULTIIF.CACHE_LAST_GOOD=1      # Caches last working address
```

---

## 9. Test Cases

### TC-MDNS-REC-*: mDNS Records

| ID | Description | Expected |
|----|-------------|----------|
| TC-MDNS-REC-1 | SRV record present | Port 8443, valid hostname |
| TC-MDNS-REC-2 | AAAA record present | Valid IPv6 address |
| TC-MDNS-REC-3 | Multiple AAAA | ULA and link-local |
| TC-MDNS-REC-4 | Address change | New AAAA announced |

### TC-TLS-COMM-*: TLS During Commissioning

| ID | Description | Expected |
|----|-------------|----------|
| TC-TLS-COMM-1 | Self-signed cert | CN=MASH-<D> accepted |
| TC-TLS-COMM-2 | Wrong CN | CN doesn't match D, warning |
| TC-TLS-COMM-3 | No client cert | Connection accepted |
| TC-TLS-COMM-4 | ALPN mash/1 | Protocol selected |

### TC-TRANS-*: PASE to Operational Transition

| ID | Description | Expected |
|----|-------------|----------|
| TC-TRANS-1 | Normal transition | Close, reconnect, mutual TLS |
| TC-TRANS-2 | mDNS update | `_mashc._udp` removed, `_mash._tcp` added |
| TC-TRANS-3 | Service type switch | `_mash._tcp` instance name: `<zone-id>-<device-id>` |
| TC-TRANS-4 | Reconnect timeout | 10 seconds max |

### TC-CERT-VAL-*: Certificate Validation

| ID | Description | Expected |
|----|-------------|----------|
| TC-CERT-VAL-1 | Valid cert | Verification succeeds |
| TC-CERT-VAL-2 | Expired cert | Verification fails |
| TC-CERT-VAL-3 | Wrong Zone CA | Verification fails |
| TC-CERT-VAL-4 | Clock skew 200s | Verification succeeds |
| TC-CERT-VAL-5 | Clock skew 400s | Verification fails |

### TC-CERT-VAL-CTRL-*: Controller Certificate Validation

| ID | Description | Expected |
|----|-------------|----------|
| TC-CERT-VAL-CTRL-1 | Valid controller cert | Device accepts, session established |
| TC-CERT-VAL-CTRL-2 | Expired controller cert | Device rejects connection |
| TC-CERT-VAL-CTRL-3 | Wrong Zone CA issuer | Device rejects connection |
| TC-CERT-VAL-CTRL-4 | Self-signed controller cert | Device rejects (must chain to Zone CA) |
| TC-CERT-VAL-CTRL-5 | Clock skew 200s | Device accepts (within tolerance) |
| TC-CERT-VAL-CTRL-6 | Clock skew 400s | Device rejects (exceeds tolerance) |

### TC-IPV6-*: IPv6 Address Handling

| ID | Description | Expected |
|----|-------------|----------|
| TC-IPV6-1 | Link-local connect | Connection succeeds |
| TC-IPV6-2 | Global address | Preferred over link-local |
| TC-IPV6-3 | Address change | Reconnect to new address |
| TC-IPV6-4 | Interface binding | Correct interface used |

### TC-MULTIIF-*: Multi-Interface Behavior

| ID | Description | Setup | Expected |
|----|-------------|-------|----------|
| TC-MULTIIF-1 | Publish all interfaces | Device has eth0 + wlan0 | AAAA records for both interfaces |
| TC-MULTIIF-2 | Interface down | Unplug eth0 while connected via eth0 | Goodbye for eth0 addrs, reconnect via wlan0 |
| TC-MULTIIF-3 | Interface up | Enable wlan0 on device | New AAAA announced after 2s debounce |
| TC-MULTIIF-4 | Try all addresses | First address unreachable | Controller tries next, connects |
| TC-MULTIIF-5 | All addresses fail | All interfaces down | "Device unreachable" error |
| TC-MULTIIF-6 | Debounce flapping | Interface down then up within 2s | No mDNS goodbye/announcement |
| TC-MULTIIF-7 | ULA preferred | Device has ULA + link-local | ULA tried first |
| TC-MULTIIF-8 | Cached address | Reconnect after brief disconnect | Cached address tried first |
| TC-MULTIIF-9 | Cache miss fallback | Cached address no longer valid | mDNS re-resolve, new address works |
| TC-MULTIIF-10 | TLS fail on one address | First address: wrong device | Try next address, TLS succeeds |

### TC-E2E-*: End-to-End Flow

| ID | Description | Expected |
|----|-------------|----------|
| TC-E2E-1 | First commissioning | Full flow succeeds |
| TC-E2E-2 | Reconnection | Mutual TLS, no PASE |
| TC-E2E-3 | Second zone | Additional commissioning |
| TC-E2E-4 | Device reboot | Reconnection works |

---

## 10. Security Considerations

### 10.1 Commissioning TLS Security

The self-signed certificate during commissioning provides:
- **Encryption:** PASE messages are encrypted
- **NOT authentication:** Device identity not verified

Authentication comes from SPAKE2+:
- Setup code proves device possession
- Verifier exchange proves both sides know code

### 10.2 Certificate Pinning

After commissioning, controller MAY pin:
- Device operational certificate
- Zone CA certificate

This prevents:
- Certificate substitution attacks
- Compromised Zone CA (if pinning op cert directly)

### 10.3 Address Spoofing

mDNS responses can be spoofed. Mitigations:
- SPAKE2+ authenticates during commissioning
- Mutual TLS authenticates during operation
- Address spoofing only causes DoS, not impersonation
