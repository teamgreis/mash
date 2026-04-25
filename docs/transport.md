# MASH Transport Layer

> TCP/TLS transport with length-prefixed framing

**Status:** Draft
**Last Updated:** 2026-01-29

---

## Related Documents

| Document | Description |
|----------|-------------|
| [Protocol Overview](protocol-overview.md) | Vision, architecture, device model |
| [Security](security.md) | TLS certificates, commissioning |
| [Multi-Zone](multi-zone.md) | Zone types, roles, connection model |
| [Interaction Model](interaction-model.md) | Message semantics |

---

## 1. Protocol Stack

```
┌────────────────────────────────┐
│      CBOR Messages             │
├────────────────────────────────┤
│   Length-Prefix Framing (4B)   │
├────────────────────────────────┤
│         TLS 1.3                │
├────────────────────────────────┤
│           TCP                  │
├────────────────────────────────┤
│         IPv6 only              │
└────────────────────────────────┘
```

---

## 2. Network Layer

### 2.1 IPv6-Only

MASH requires IPv6. No IPv4 support.

**Rationale:**
- Simplifies implementation (single code path)
- Every modern device supports IPv6
- Link-local addresses work without infrastructure
- Thread mesh compatibility for future

### 2.2 Address Types

| Address Type | Range | Use |
|--------------|-------|-----|
| Link-local | fe80::/10 | Commissioning, no router needed |
| Multicast | ff02::fb | mDNS discovery |
| Global/ULA | Site-dependent | Operational communication |

### 2.3 Auto-Configuration

- **SLAAC** (Stateless Address Autoconfiguration) for address assignment
- No DHCP required (but supported)
- Works on isolated networks (link-local only)

---

## 3. TLS 1.3

### 3.1 Requirements

- TLS 1.3 only (no fallback to 1.2)
- Mutual authentication with certificates
- See [Security](security.md) for certificate details

### 3.2 Cipher Suites

Required cipher suites (in preference order):

```
TLS_AES_128_GCM_SHA256        (mandatory)
TLS_AES_256_GCM_SHA384        (recommended)
TLS_CHACHA20_POLY1305_SHA256  (optional, good for constrained devices)
```

### 3.3 Key Exchange

- ECDHE with P-256 (secp256r1) - mandatory
- ECDHE with X25519 - recommended

---

## 4. Frame Format

All messages are length-prefixed:

```
┌─────────────────────────────────────────────┐
│ Length (4 bytes, big-endian) │ CBOR Payload │
└─────────────────────────────────────────────┘
```

### 4.1 Length Field

- 4 bytes, unsigned, big-endian
- Represents payload length (not including the 4-byte length field itself)
- Maximum message size: 8192 bytes (8 KB) — see DEC-069

### 4.2 Example

A 256-byte CBOR message:
```
00 00 01 00 [256 bytes of CBOR data]
```

### 4.3 Why Length-Prefix?

| Approach | Pros | Cons |
|----------|------|------|
| Length-prefix | Simple parsing, known size upfront | 4 bytes overhead |
| Delimiter-based | No overhead | Complex parsing, escaping needed |
| Self-describing | Flexible | Must parse to know size |

MASH uses length-prefix for simplicity and efficiency on constrained devices.

---

## 5. Connection Model

### 5.1 Client/Server Roles

- **Client** = Controller (EMS, SMGW, app)
- **Server** = Device (EVSE, inverter, heat pump)

**Client always initiates connection.** This eliminates the EEBUS double-connection race condition.

### 5.2 Connection Persistence

- One persistent connection per controller-device pair
- Connection stays open for the session lifetime
- Automatic reconnection on disconnect

### 5.3 Port and ALPN Routing (DEC-067, DEC-082)

MASH separates commissioning and operational TLS configurations. Two deployment patterns are conformant; both produce identical wire behavior.

**Connection types:**

| Connection Type | ALPN Protocol | TLS Config | Purpose |
|-----------------|---------------|------------|---------|
| Commissioning | `mash-comm/1` | Self-signed server cert, `NoClientCert` | PASE handshake + certificate exchange |
| Operational | `mash/1` | Mutual TLS with operational certs | Zone connections (Read/Write/Subscribe/Invoke) |

**Pattern A — Single-port ALPN routing (DEC-067).** Recommended for implementations whose TLS library exposes a `GetConfigForClient`-equivalent primitive (e.g., Go).

- Single TCP port (default **8443**) serves both ALPN protocols.
- The server inspects the ALPN list in `ClientHello.SupportedProtos` *before* the handshake completes and returns the appropriate TLS config (different cert and `ClientAuth` per ALPN). This prevents operational certificates from being exposed to unauthenticated commissioning peers.
- Both `_mash._tcp` and `_mash-comm._tcp` mDNS records advertise port 8443 (same SRV port).
- When the commissioning window is closed, `mash-comm/1` connections are rejected at the TLS layer.
- The listener starts when first needed (commissioning or zones) and stops when both are inactive.

**Pattern B — Two-port deployment (DEC-082).** Recommended for implementations whose TLS library lacks a `GetConfigForClient`-equivalent primitive (e.g., Python stdlib `ssl`, C with mbedTLS, Zig).

- Two TCP listeners on distinct ports, each advertising one ALPN exclusively:
  - **Operational listener** — default **8443**, ALPN `mash/1` only, mutual TLS with operational certs.
  - **Commissioning listener** — implementation-chosen free port (no canonical default), ALPN `mash-comm/1` only, self-signed server cert, `NoClientCert`.
- mDNS SRV records advertise the actual port for each service: `_mash._tcp` SRV → operational port; `_mash-comm._tcp` SRV → commissioning port.
- Commissioning window closure is enforced by stopping the commissioning listener (or rejecting at accept time). Equivalent in effect to single-port's TLS-layer rejection.
- The operational listener is stopped when the device has zero zones; the commissioning listener is stopped when the commissioning window is closed.

**Conformance equivalence.** Once the TLS handshake completes, all subsequent CBOR/PASE/cert/operational traffic is byte-identical regardless of deployment pattern. Peers and conformance tests (`mash-test`) discover the actual ports via mDNS SRV and connect accordingly; no peer or test logic depends on the deployment shape.

**Authoritative port:** the mDNS SRV `port` field. Implementations MUST consult it rather than assume a default. The default port 8443 applies only to the operational service in pattern A and to the operational listener in pattern B.

### 5.4 Connection Limits (DEC-047)

Connection limits are derived from zone capacity to ensure predictable resource usage.

**Zone-Based Connection Model:**

| Connection Type | Maximum | Derivation |
|-----------------|---------|------------|
| Operational | max_zones | One per paired zone |
| Commissioning | 1 | Single concurrent (ALPN: `mash-comm/1`) |
| **Total** | max_zones + 1 | Maximum simultaneous |

Connection caps are enforced per-ALPN: `mash-comm/1` allows 1 concurrent connection, `mash/1` allows up to max_zones. The cap is per-device, not per-listener — two-port deployments (DEC-082) enforce it across both listeners with shared counters.

**Example (typical device, max_zones=2):**
- 2 operational connections (GRID zone + LOCAL zone)
- 1 commissioning connection (when slot available)
- Total: 3 connections maximum

**Commissioning Rejection (DEC-063):**

When a commissioning connection is rejected after the PASERequest message has been received, the device MUST send a `CommissioningError` message with error code 5 (`DEVICE_BUSY`) and an appropriate `RetryAfter` hint before closing the connection.

| Condition | RetryAfter | Rationale |
|-----------|------------|-----------|
| Commissioning in progress | HandshakeTimeout ms | Wait for current handshake to complete |
| Cooldown period active | Remaining cooldown ms | Wait for cooldown to expire |
| All zone slots filled | 0 | No point retrying until decommission |

The `CommissioningError` message format (CBOR):
```
{ 1: 255, 2: 5, 3: "reason string", 4: retryAfterMs }
```

Key 4 (`RetryAfter`) is a uint32 in milliseconds with `omitempty` -- it is not serialized when 0. This is backward compatible: older implementations ignore unknown CBOR keys.

**Behavior:**
1. Device MUST track operational and commissioning connections separately
2. When commissioning rejected after PASERequest, device MUST send `CommissioningError(DEVICE_BUSY)` with `RetryAfter` hint (DEC-063)
3. Device MUST NOT reveal current connection count or zone capacity
4. Device MAY implement per-IP tracking for diagnostics
5. Device MUST enforce the total connection cap at the transport level (DEC-062)

**Transport-Level Enforcement (DEC-062):**

The total connection cap (max_zones + 1) MUST be enforced at TCP accept, before the TLS handshake begins. This is the earliest rejection point and protects constrained devices from resource exhaustion by connections that never send application-layer messages.

| Requirement | Description |
|-------------|-------------|
| Counter increment | MUST occur after TCP accept, before spawning connection handler |
| Cap check | If active connections >= max_zones + 1, MUST close raw TCP connection immediately |
| Counter decrement | MUST occur when connection handler returns (any exit path) |
| Atomicity | Check and increment MUST be free of TOCTOU races |
| Scope | Counter covers ALL connection types (commissioning and operational) |

The transport-level cap operates independently of the PASE-level commissioning lock (DEC-047) and the message-gated locking (DEC-061). A connection that is rejected at the transport level never reaches the TLS handshake or PASE exchange.

**Stale Connection Reaper (DEC-064):**

A device MUST implement a background reaper that periodically force-closes pre-operational connections exceeding a staleness threshold. This catches connections that pass the transport-level cap but stall before becoming operational.

| Parameter | Default | Description |
|-----------|---------|-------------|
| StaleConnectionTimeout | 90s | Maximum age for pre-operational connections. Set 0 to disable. |
| ReaperInterval | 10s | How often the reaper scans for stale connections |

**Rules:**
1. Connections are tracked from TCP accept until they enter the operational message loop
2. Connections MUST be deregistered from the tracker before entering the operational message loop
3. The reaper MUST NOT close operational connections
4. The default timeout (90s) is intentionally greater than HandshakeTimeout (85s) -- per-phase timeouts fire first for well-behaved connections; the reaper is a safety net for edge cases
5. The reaper MUST exit cleanly when the service stops

**Rationale:**
- Limits derived from zone capacity are logically defensible
- Prevents resource exhaustion on 256KB MCUs
- Ensures operational connections are never blocked by commissioning
- Single commissioning connection minimizes attack surface

---

## 6. Keep-Alive

### 6.1 Mechanism

Keep-alive uses application-layer ping/pong (not TCP keep-alive):

| Parameter | Value |
|-----------|-------|
| Ping interval | 30 seconds (if no other activity) |
| Pong timeout | 5 seconds |
| Max missed pongs | 3 |

### 6.2 Ping Message

Per DEC-083, every control message frame is wrapped in **CBOR tag 77** (`D8 4D`).
The tag prefix is the unique CONTROL marker; any frame that does not begin
with `D8 4D` is application-layer (Request / Response / Notification) and
MUST be classified accordingly.

```cbor
77({
  1: 1,         // type: 1 = ping
  2: 12345      // sequence (uint32; omitted when zero)
})
```

Wire bytes for a ping with sequence 12345:

```
D8 4D A2 01 01 02 19 30 39
│  │  │  │  │  │  │  └────── sequence value (12345 in 2-byte form)
│  │  │  │  │  │  └────────── key 2 (sequence)
│  │  │  │  │  └───────────── value 1 (ping)
│  │  │  │  └──────────────── key 1 (type)
│  │  │  └─────────────────── 2-element map
│  │  └────────────────────── tag 77 ('M' for MASH)
│  └───────────────────────── tag with 1-byte argument
└──────────────────────────── (start of frame)
```

### 6.3 Pong Message

```cbor
77({
  1: 2,         // type: 2 = pong
  2: 12345      // sequence (echoed from the matching ping)
})
```

### 6.4 Close Message

```cbor
77({
  1: 3          // type: 3 = close
})
```

The sequence field is omitted on close (no sequence semantics).

### 6.5 Connection Loss Detection

**Maximum detection delay: 95 seconds**

Calculation:
```
Ping every 30s → 3 cycles × 30s = 90s
Plus final timeout → 90s + 5s = 95s maximum
```

This is the worst-case scenario when network partition occurs immediately after a successful ping. Typical detection is much faster:

| Scenario | Detection Time |
|----------|---------------|
| Clean disconnect (TCP RST) | < 1 second |
| TLS error | < 1 second |
| Network partition after ping | 5 seconds |
| Network partition mid-cycle | Up to 95 seconds |

### 6.6 Connection Closure

After 3 missed pongs:
1. Close TCP connection
2. Notify application layer (triggers FAILSAFE on device)
3. Attempt reconnection (client side)

---

## 7. Reconnection

### 7.1 Client Reconnection Strategy

When connection is lost, client should:

1. Wait initial backoff (1 second)
2. Attempt reconnection
3. On failure, exponential backoff: 2s, 4s, 8s, 16s, 32s, 60s (max)
4. Continue attempting at 60s intervals
5. Reset backoff on successful connection

### 7.2 Reconnection Jitter

To prevent thundering herd when multiple clients reconnect simultaneously (e.g., after device restart), clients SHOULD add random jitter:

```
actual_delay = base_delay + random(0, base_delay * 0.25)
```

| Base Delay | With Jitter |
|------------|-------------|
| 1s | 1.0 - 1.25s |
| 2s | 2.0 - 2.5s |
| 60s | 60 - 75s |

### 7.3 Reconnection Success Criteria

**A reconnection is successful when the TLS handshake completes and both sides authenticate.**

Specifically:
1. TCP connection established
2. TLS 1.3 handshake completed
3. Both certificates validated (mutual authentication)
4. Device not in commissioning mode (operational state)

At this point:
- Backoff timer resets to 1 second
- Client should re-establish subscriptions
- Client should re-read device state

**Note:** A successful TLS handshake followed by immediate application-layer rejection (e.g., zone limit exceeded) does NOT count as successful reconnection - backoff should continue.

### 7.4 State Preservation

After reconnection:
- Subscriptions must be re-established
- Device state should be re-read
- No assumption that state is unchanged

---

## 8. Message Ordering

### 8.1 Delivery Order Guarantee

**TCP guarantees in-order delivery.** MASH inherits this guarantee:

- Messages from a single sender arrive in send order
- No explicit sequence numbers needed at MASH layer
- TLS maintains ordering within the encrypted stream

### 8.2 Multi-Zone Ordering

When multiple zones send commands simultaneously:

- Device processes messages in receipt order (FIFO)
- No priority queuing based on zone type
- Each command is processed and responded to independently

**Example:**
```
T+0.000s: Zone 1 sends SetLimit(5000)
T+0.001s: Zone 2 sends SetLimit(3000)

Device receives in order: Zone 1, then Zone 2
Device processes Zone 1 SetLimit → responds
Device processes Zone 2 SetLimit → responds

Final effectiveLimit = min(5000, 3000) = 3000
```

### 8.3 Subscription Notification Ordering

- Notifications are sent in the order changes occur
- Coalescing may combine multiple changes (see subscription semantics)
- Notifications from different subscriptions have no ordering guarantee relative to each other

### 8.4 Write-Then-Notify Ordering (DEC-071)

When a write from **one** connection causes a value change that a subscriber on **another** connection observes, the device MUST deliver the triggered notification(s) on the subscriber's stream **before** sending the write response on the writer's stream. That is, from the device's perspective:

```
writer stream:     ... Request(write) ─────────────────► [Response] ─►
subscriber stream: .................... Notification ────────────────►
```

`Notification → Response` is the required order on the wire.

**Why this matters:**

Controller A writes `SetLimit(3000)` on its connection. Controller B is subscribed to `effectiveConsumptionLimit` on its own connection.
- **Allowed:** B sees the notification, then A sees the write succeed.
- **Forbidden:** A sees the write succeed, then B eventually sees the notification. Because A may now issue a follow-on command assuming the whole fleet has observed the change, a late-arriving notification on B's stream leaves B transiently out of sync with A's reality. Making the notification precede the response eliminates the race at the protocol level and lets both sides reason about causality without additional handshakes.

**Scope and non-guarantees:**

- This ordering applies **only** to notifications *caused by* the acknowledged write. Unrelated notifications on the subscriber's stream follow §8.3 rules.
- Ordering across multiple subscribers on different connections is per-subscriber: each subscriber sees its notification before the writer's response is sent to the writer. There is no cross-subscriber ordering guarantee (none of the subscribers observe each other's streams).
- If delivering the notification fails (e.g., subscriber connection is broken), the device still completes the write and sends the response on the writer's stream. Subscription semantics (minInterval batching, delta notifications) continue to apply. Failure to deliver a notification MUST NOT block the write response.

**Implementation guidance:**

The reference implementation's dispatcher submits notification frames to all subscriber sessions synchronously as part of the write's commit step, before returning control to the write handler to emit the response. See `pkg/service/session_subscription_tracker.go` in the Go reference.

---

## 9. Message Size Limits

| Limit | Value | Rationale |
|-------|-------|-----------|
| Max message size | 8 KB | Fits in 256 KB MCU memory budget (DEC-003, DEC-069) |
| Typical message | < 2 KB | Most operations are small |
| Max subscription batch | 4 KB | Bounded under max message size; prevents flooding |

---

## 10. Error Handling

### 10.1 Transport Errors

| Error | Action |
|-------|--------|
| TLS handshake failure | Close connection, log error |
| Invalid length prefix | Close connection |
| Message too large | Close connection |
| CBOR parse error | Send error response, keep connection |
| Timeout | Close connection, reconnect |

### 10.2 Graceful Shutdown

To close connection gracefully:

```cbor
{
  "type": "close",
  "reason": "shutdown"
}
```

Wait for acknowledgment or 5-second timeout, then close TCP.

---

## 11. Comparison with EEBUS SHIP

| Aspect | EEBUS SHIP | MASH Transport |
|--------|------------|----------------|
| Protocol | WebSocket over TLS | Raw TLS over TCP |
| Framing | WebSocket frames | Length-prefix |
| Handshake | Complex SHIP state machine | TLS + simple hello |
| Connection initiation | Either side (race condition) | Client only |
| Keep-alive | SHIP-level + WebSocket | Application ping/pong |
| Message format | JSON | CBOR |

**Key improvements:**
- No WebSocket overhead
- No SHIP state machine complexity
- Deterministic connection ownership
- Binary format for efficiency

---

## 12. Commissioning Window Timing (DEC-048)

### 12.1 Duration Parameters

Commissioning window timing is aligned with Matter specification 5.4.2.3.1:

| Parameter | Value | Matter Spec |
|-----------|-------|-------------|
| Default duration | 15 minutes | 15 minutes max |
| Minimum configurable | 3 minutes | 3 minutes min |
| Maximum configurable | 3 hours | 15 minutes max |

**Note:** MASH allows longer maximum (3 hours vs Matter's 15 min) for professional
installer scenarios. The pairing request mechanism provides re-triggering when needed.

### 12.2 Window Lifecycle

```
┌─────────────────────────────────────────────────────────────────┐
│  Power On / Factory Reset                                        │
│     │                                                            │
│     ▼                                                            │
│  ┌────────────────────────┐                                      │
│  │  Commissioning Mode    │◄─── Pairing Request (_mashp._udp)    │
│  │  (15 min default)      │                                      │
│  └────────────┬───────────┘                                      │
│               │                                                  │
│    ┌──────────┼──────────┐                                       │
│    ▼          ▼          ▼                                       │
│  Success   Timeout    Explicit Close                             │
│    │          │          │                                       │
│    ▼          ▼          ▼                                       │
│  ┌────────────────────────┐                                      │
│  │  Operational Mode      │                                      │
│  │  (no mDNS _mash-comm)  │                                      │
│  └────────────────────────┘                                      │
└─────────────────────────────────────────────────────────────────┘
```

### 12.3 Re-triggering via Pairing Request

When the commissioning window expires, controllers can re-trigger it using the
pairing request mechanism (`_mashp._udp`):

1. Controller advertises `_mashp._udp.local` with matching discriminator
2. Device detects pairing request (browsing `_mashp._udp`)
3. Device opens new commissioning window (15 min default)
4. Device advertises `_mash-comm._tcp`

**Security:** Pairing request requires knowledge of the device discriminator
(from QR code or manual entry). This prevents unauthorized re-triggering.

### 12.4 Rationale

**Why 15 minutes default (not 3 hours):**
- Sufficient for typical commissioning workflow
- Reduces mDNS advertisement pollution on shared spectrum
- Smaller attack window for PASE brute-force attempts
- Pairing request provides on-demand re-triggering

**Why 3 hours maximum (not Matter's 15 min):**
- Professional installer scenarios may require longer setup
- No harm in allowing longer windows when explicitly configured
- Pairing request mechanism eliminates need for very long defaults
