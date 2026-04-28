# Pairing and Connection PICS Registry

> PICS codes for transport, commissioning, zones, and connection handling

**Status:** Draft
**Last Updated:** 2025-01-27

---

## 1. Overview

This registry defines PICS codes for the pairing and connection layer of MASH. These codes describe device capabilities for:

- Transport layer (TLS, framing, keep-alive)
- Commissioning (SPAKE2+/PASE, certificates)
- Zone management (multi-zone, priority)
- Connection lifecycle (failsafe, reconnection)
- Subscription handling

---

## 2. Transport Layer (TRANS)

### 2.1 Protocol Support

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.TRANS | Transport layer support | 1 | M |
| MASH.S.TRANS.IPV6 | IPv6 support | 1 | M |
| MASH.S.TRANS.TLS13 | TLS 1.3 support | 1 | M |
| MASH.S.TRANS.PORT | Default listening port | 8443 | M |

### 2.2 TLS Cipher Suites

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.TRANS.TLS_AES_128_GCM | TLS_AES_128_GCM_SHA256 | 0, 1 | M (must be 1) |
| MASH.S.TRANS.TLS_AES_256_GCM | TLS_AES_256_GCM_SHA384 | 0, 1 | O |
| MASH.S.TRANS.TLS_CHACHA20 | TLS_CHACHA20_POLY1305_SHA256 | 0, 1 | O |

### 2.3 Key Exchange

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.TRANS.ECDHE_P256 | ECDHE with P-256 | 0, 1 | M (must be 1) |
| MASH.S.TRANS.ECDHE_X25519 | ECDHE with X25519 | 0, 1 | O |

### 2.4 Framing

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.TRANS.MAX_MSG_SIZE | Maximum message size (bytes) | 8192 | M |
| MASH.S.TRANS.LENGTH_PREFIX | 4-byte length prefix | 1 | M |

### 2.5 Keep-Alive

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.TRANS.PING_INTERVAL | Ping interval (seconds) | 30 | M |
| MASH.S.TRANS.PONG_TIMEOUT | Pong timeout (seconds) | 5 | M |
| MASH.S.TRANS.MAX_MISSED_PONGS | Max missed pongs before disconnect | 3 | M |
| MASH.S.TRANS.DETECTION_DELAY_MAX | Max connection loss detection (seconds) | 95 | M |

---

## 3. Commissioning (COMM)

### 3.1 PASE (Password-Authenticated Session Establishment)

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.COMM | Commissioning support | 1 | M |
| MASH.S.COMM.PASE | SPAKE2+ PASE support | 1 | M |
| MASH.S.COMM.PASE_GROUP | SPAKE2+ group | "P-256" | M |
| MASH.S.COMM.PASE_HASH | SPAKE2+ hash function | "SHA-256" | M |
| MASH.S.COMM.PASE_KDF | SPAKE2+ KDF | "HKDF-SHA256" | M |
| MASH.S.COMM.PASE_MAC | SPAKE2+ MAC | "HMAC-SHA256" | M |

### 3.2 Commissioning Window

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.COMM.WINDOW_DURATION | Commissioning window - user initiated (seconds) | 120 | M |
| MASH.S.COMM.WINDOW_DURATION_PAIRING | Commissioning window - pairing request (seconds) | 10800 | O |
| MASH.S.COMM.SETUP_CODE_BITS | Setup code entropy (bits) | 27 | M |
| MASH.S.COMM.DISCRIMINATOR_BITS | Discriminator bits | 12 | M |

### 3.3 Commissioning Timeouts

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.COMM.PASE_TIMEOUT | PASE phase timeout (seconds) | 30 | M |
| MASH.S.COMM.CSR_TIMEOUT | CSR phase timeout (seconds) | 10 | M |
| MASH.S.COMM.CERT_TIMEOUT | Certificate install timeout (seconds) | 30 | M |

### 3.4 Attestation

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.COMM.ATTESTATION | Device attestation support | 0, 1 | O |
| MASH.S.COMM.ATTESTATION_CERT | Has attestation certificate | 0, 1 | O |

### 3.5 Error Codes

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.COMM.ERR_ZONE_TYPE_EXISTS | Supports error code 10 (zone type already exists) | 0, 1 | M |
| MASH.S.COMM.ERR_BUSY | Supports error code 5 (device busy, retry later) | 0, 1 | M |
| MASH.S.COMM.ERR_BUSY_RETRY_AFTER | Includes RetryAfter hint in busy response | 0, 1 | M |

---

## 4. Certificate Management (CERT)

### 4.1 Certificate Types

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.CERT | Certificate management support | 1 | M |
| MASH.S.CERT.X509V3 | X.509 v3 certificates | 1 | M |
| MASH.S.CERT.ECDSA_P256 | ECDSA P-256 signatures | 1 | M |

### 4.2 Certificate Storage

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.CERT.MAX_ZONES | Maximum zone certificates stored | 2 | M |
| MASH.S.CERT.PERSISTENT | Certificates persist across reboot | 0, 1 | M |

### 4.3 Certificate Lifecycle

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.CERT.OP_VALIDITY | Operational cert validity (days) | 365 | M |
| MASH.S.CERT.RENEWAL_WINDOW | Renewal window before expiry (days) | 30 | M |
| MASH.S.CERT.IN_SESSION_RENEWAL | In-session certificate renewal | 0, 1 | M |
| MASH.S.CERT.GRACE_PERIOD | Grace period after expiry (days) | 0, 7 | O |

---

## 5. Zone Management (ZONE)

### 5.1 Multi-Zone Support

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.ZONE | Zone management support | 1 | M |
| MASH.S.ZONE.MAX_ZONES | Maximum concurrent zones (one per type) | 2 | M |
| MASH.S.ZONE.CURRENT_COUNT | Current zone count (runtime) | 0-2 | - |

### 5.2 Zone Types Accepted

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.ZONE.GRID | Accepts GRID zones (external/regulatory) | 0, 1 | M |
| MASH.S.ZONE.LOCAL | Accepts LOCAL zones (local energy management) | 0, 1 | M |

### 5.3 Zone Operations

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.ZONE.ADD | AddZone command support | 1 | M |
| MASH.S.ZONE.REMOVE | RemoveZone command support (self-removal) | 1 | M |
| MASH.S.ZONE.LIST | ListZones command support | 1 | M |

### 5.4 Admin Delegation

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.ZONE.ADMIN_DELEGATION | Admin delegation support | 0, 1 | O |
| MASH.S.ZONE.TEMP_TOKEN_DURATION | Temporary token validity (seconds) | 300 | O |

---

## 6. Connection Lifecycle (CONN)

### 6.1 Connection States

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.CONN | Connection lifecycle support | 1 | M |
| MASH.S.CONN.GRACEFUL_CLOSE | Graceful close message support | 1 | M |
| MASH.S.CONN.CLOSE_TIMEOUT | Close acknowledgment timeout (seconds) | 5 | M |
| MASH.S.CONN.STALE_REAPER | Stale connection reaper enabled | 0, 1 | M |
| MASH.S.CONN.STALE_TIMEOUT | Stale connection reaper timeout (seconds) | 90 | M |

### 6.2 Reconnection (Client-side)

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.C.CONN.BACKOFF_INITIAL | Initial backoff (seconds) | 1 | M |
| MASH.C.CONN.BACKOFF_MAX | Maximum backoff (seconds) | 60 | M |
| MASH.C.CONN.BACKOFF_MULTIPLIER | Backoff multiplier | 2 | M |
| MASH.C.CONN.JITTER | Jitter enabled | 0, 1 | O |
| MASH.C.CONN.JITTER_FACTOR | Jitter factor (0.0-1.0) | 0.25 | O |

---

## 7. Failsafe Behavior (FAILSAFE)

### 7.1 Failsafe Configuration

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.FAILSAFE | Failsafe support | 1 | M |
| MASH.S.FAILSAFE.DURATION_MIN | Minimum failsafe duration (seconds) | 7200 | M |
| MASH.S.FAILSAFE.DURATION_MAX | Maximum failsafe duration (seconds) | 86400 | M |
| MASH.S.FAILSAFE.DURATION_DEFAULT | Default failsafe duration (seconds) | 14400 | M |

### 7.2 Failsafe Limits

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.FAILSAFE.CONSUMPTION_LIMIT | Has failsafe consumption limit | 1 | M |
| MASH.S.FAILSAFE.PRODUCTION_LIMIT | Has failsafe production limit | 0, 1 | [BIDIRECTIONAL] |
| MASH.S.FAILSAFE.LIMIT_CONFIGURABLE | Failsafe limits configurable | 0, 1 | O |

### 7.3 Failsafe Behavior

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.FAILSAFE.TIMER_PERSISTENT | Timer persists across reboot | 0, 1 | O |
| MASH.S.FAILSAFE.TIMER_ACCURACY | Timer accuracy | "1%" | M |
| MASH.S.FAILSAFE.GRACE_PERIOD | Grace period after failsafe | 0, 1 | O |
| MASH.S.FAILSAFE.GRACE_DURATION | Grace period duration (seconds) | 0-3600 | O |
| MASH.S.FAILSAFE.RECONNECT_WINS | Reconnection prevents AUTONOMOUS | 1 | M |

---

## 8. Subscription Handling (SUB)

### 8.1 Subscription Capabilities

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.SUB | Subscription support | 1 | M |
| MASH.S.SUB.MAX_SUBSCRIPTIONS | Max subscriptions per connection | 10-255 | M |
| MASH.S.SUB.MAX_ATTRS_PER_SUB | Max attributes per subscription | 20-255 | M |

### 8.2 Subscription Intervals

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.SUB.MIN_INTERVAL_MIN | Minimum minInterval allowed | 0 | M |
| MASH.S.SUB.MIN_INTERVAL_MAX | Maximum minInterval allowed | 3600 | M |
| MASH.S.SUB.MAX_INTERVAL_MIN | Minimum maxInterval allowed | 1 | M |
| MASH.S.SUB.MAX_INTERVAL_MAX | Maximum maxInterval allowed | 86400 | M |

### 8.3 Subscription Behavior

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.SUB.COALESCE | Coalescing strategy | "last_value" | M |
| MASH.S.SUB.BOUNCE_BACK | Suppress bounce-back notifications | 0, 1 | O |
| MASH.S.SUB.HEARTBEAT_CONTENT | Heartbeat notification content | "empty", "full" | M |
| MASH.S.SUB.PRIMING | Priming notification on subscribe | 1 | M |
| MASH.S.SUB.DELTA_ONLY | Delta-only change notifications | 1 | M |
| MASH.S.SUB.INTERVAL_AUTOCORRECT | Auto-correct invalid intervals | 0, 1 | O |

---

## 9. Duration Timers (DURATION)

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.DURATION | Duration parameter support | 1 | M |
| MASH.S.DURATION.MAX | Maximum duration (seconds) | 86400 | M |
| MASH.S.DURATION.START_ON | Timer starts on | "receipt" | M |
| MASH.S.DURATION.EXPIRY_ACTION | Action on expiry | "clear" | M |
| MASH.S.DURATION.PERSIST_RECONNECT | Persist across reconnection | 0 | M |
| MASH.S.DURATION.ACCURACY | Timer accuracy | "1%" | M |

---

## 10. Discovery (DISC)

### 10.1 mDNS Support

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.DISC | Discovery support | 1 | M |
| MASH.S.DISC.MDNS | mDNS/DNS-SD support | 1 | M |
| MASH.S.DISC.SERVICE_TYPE | Service type | "_mash._tcp" | M |

### 10.2 Pre-Commissioning Discovery

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.DISC.PRE_COMM | Pre-commissioning mDNS | 1 | M |
| MASH.S.DISC.TXT_D | Discriminator TXT record | 1 | M |
| MASH.S.DISC.TXT_VP | Vendor:Product TXT record | 1 | M |
| MASH.S.DISC.TXT_CM | Commissioning mode TXT record | 1 | M |
| MASH.S.DISC.TXT_DT | Device type TXT record | 1 | M |

### 10.3 Post-Commissioning Discovery

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.DISC.POST_COMM | Post-commissioning mDNS | 1 | M |
| MASH.S.DISC.TXT_ZI | Zone ID TXT record | 1 | M |
| MASH.S.DISC.TXT_DI | Device ID TXT record | 1 | M |
| MASH.S.DISC.TXT_FW | Firmware version TXT record | 0, 1 | O |
| MASH.S.DISC.TXT_EP | Endpoint count TXT record | 0, 1 | O |
<!-- DEC-074 / DEC-088: FM (feature map) is forbidden in operational TXT. -->
| MASH.S.DISC.TXT_VP | Vendor:Product TXT record | 0, 1 | O |

### 10.4 QR Code

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.DISC.QR | QR code support | 0, 1 | O |
| MASH.S.DISC.QR_FORMAT | QR code format version | 1 | O |
| MASH.S.DISC.MANUAL_ENTRY | Manual code entry fallback | 1 | M |

### 10.5 Pairing Request Discovery (DEC-042)

| PICS Code | Description | Values | Conformance |
|-----------|-------------|--------|-------------|
| MASH.S.DISC.PAIRING_REQUEST | Device supports pairing request listening (_mashp._udp) | 0, 1 | O |
| MASH.C.DISC.PAIRING_REQUEST | Controller supports pairing request announcing (_mashp._udp) | 0, 1 | O |
| MASH.S.DISC.TXT_D_PAIRING | Discriminator in pairing request TXT record | 0, 1 | [PAIRING_REQUEST] |
| MASH.S.DISC.TXT_ZI | Zone ID in pairing request TXT record | 0, 1 | [PAIRING_REQUEST] |
| MASH.S.DISC.TXT_ZN | Zone name in pairing request TXT record (optional) | 0, 1 | O |

---

## Related Documents

| Document | Description |
|----------|-------------|
| [PICS Format](../pics-format.md) | PICS file format specification |
| [Transport](../../transport.md) | Transport layer specification |
| [Security](../../security.md) | Commissioning and certificates |
| [Discovery](../../discovery.md) | mDNS and capability discovery |
