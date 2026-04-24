# MASH Stack Architecture

**Status:** Architecture Review
**Date:** 2026-02-14 (updated)

This document provides a full-depth architecture review of the MASH Go reference implementation. It covers every package in the stack with type-level detail, identifies architectural patterns, and flags areas of concern.

---

## Layer Diagram

```
┌─────────────────────────────────────────────────────────────┐
│  Application         cmd/mash-device, cmd/mash-controller   │
├─────────────────────────────────────────────────────────────┤
│  Service             pkg/service                             │
│                      DeviceService, ControllerService        │
│                      ZoneSession, DeviceSession              │
├─────────────────────────────────────────────────────────────┤
│  Protocol            pkg/interaction                         │
│                      Server (device), Client (controller)    │
├──────────────┬──────────────┬───────────────────────────────┤
│  Features    │  Model       │  Zone / Subscription           │
│  pkg/features│  pkg/model   │  pkg/zone, pkg/subscription    │
├──────────────┴──────────────┴───────────────────────────────┤
│  Wire                pkg/wire                                │
│                      CBOR encoding, message types             │
├─────────────────────────────────────────────────────────────┤
│  Transport           pkg/transport                           │
│                      TLS 1.3, length-prefixed framing        │
├──────────────┬──────────────────────────────────────────────┤
│  Security    │  Discovery                                    │
│  pkg/commis- │  pkg/discovery                                │
│  sioning     │  mDNS (4 service types)                       │
│  pkg/cert    │                                               │
├──────────────┴──────────────────────────────────────────────┤
│  Support             pkg/inspect, pkg/persistence, pkg/log,  │
│                      pkg/zonecontext                          │
└─────────────────────────────────────────────────────────────┘
```

Dependencies flow downward. The service layer is the orchestration hub -- it depends on nearly every other package. Lower layers have no upward dependencies. `LimitResolver` in `pkg/features` imports `pkg/zonecontext` (a thin shared package) to access caller zone identity from context, avoiding any dependency on `pkg/service`.

---

## 1. pkg/transport -- TLS 1.3 + Framing

### Responsibility

Provides TLS 1.3 connections with length-prefixed binary framing and keep-alive monitoring. This is the bottom of the network stack.

### Core Types

| Type | Role |
|------|------|
| `Server` | Accepts incoming TLS connections, dispatches to handlers |
| `ServerConn` | Server-side connection wrapper |
| `Client` | Initiates outgoing TLS connections |
| `ClientConn` | Client-side connection wrapper |
| `Framer` (`FrameReader` + `FrameWriter`) | 4-byte big-endian length-prefixed frame I/O |
| `KeepAlive` | Ping/pong liveness monitor |
| `Connection` | Unified connection handler with state machine |

### Framing Protocol

```
┌──────────────┬─────────────────┐
│ 4-byte length│    CBOR payload  │
│ (big-endian) │                  │
└──────────────┴─────────────────┘
```

Constants:
- `LengthPrefixSize = 4`
- `DefaultMaxMessageSize = 8192` (8 KB, DEC-069)
- `MinMessageSize = 1`

### TLS Configuration

- **Version:** TLS 1.3 only (no fallback)
- **Cipher suites:** TLS_AES_128_GCM_SHA256 (mandatory), TLS_AES_256_GCM_SHA384, TLS_CHACHA20_POLY1305_SHA256
- **Key exchange:** X25519 (recommended), P-256 (mandatory)
- **ALPN:** `mash/1`
- **Session tickets:** Disabled
- **Mutual TLS:** Enforced for operational connections; skipped during commissioning (SPAKE2+ provides auth)

Key functions:
- `NewServerTLSConfig(*TLSConfig) (*tls.Config, error)`
- `NewClientTLSConfig(*TLSConfig) (*tls.Config, error)`
- `NewCommissioningTLSConfig() *tls.Config` -- InsecureSkipVerify for PASE phase
- `NewOperationalClientTLSConfig(*OperationalTLSConfig) (*tls.Config, error)`

### Keep-Alive Protocol

```
Defaults: PingInterval=30s, PongTimeout=5s, MaxMissedPongs=3
Maximum detection delay: 95s (30*3 + 5)
```

`KeepAlive` sends pings at `PingInterval`, expects pongs within `PongTimeout`. After `MaxMissedPongs` missed, calls `onTimeout()` to force-close the connection.

### Control Messages

```go
ControlPing  = 1  // Liveness check
ControlPong  = 2  // Ping response
ControlClose = 3  // Graceful close
```

Encoded/decoded via `EncodePing()`, `EncodePong()`, `EncodeClose()`, `DecodeControlMessage()`.

### Connection State Machine

```
StateDisconnected → StateConnecting → StateConnected → StateClosing → StateDisconnected
```

`Connection` wraps a `net.Conn` with framing, keep-alive, and state tracking. Implements `ConnectionHandler` interface for message dispatch.

### Architectural Notes

The transport layer is clean and well-isolated. The `Connection` type provides a good abstraction over raw TLS, but the test harness bypasses it and uses lower-level `ClientConn` directly in some places, creating two different connection management patterns.

---

## 2. pkg/wire -- CBOR Message Format

### Responsibility

Defines the on-the-wire CBOR message format for all protocol operations. Integer keys for compactness.

### Message Types

**Request** (controller -> device):
```cbor
{
  1: messageId,    // uint32 (non-zero)
  2: operation,    // uint8: 1=Read, 2=Write, 3=Subscribe, 4=Invoke
  3: endpointId,   // uint8
  4: featureId,    // uint8
  5: payload       // operation-specific (optional)
}
```

**Response** (device -> controller):
```cbor
{
  1: messageId,    // uint32 (matches request)
  2: status,       // uint8: 0=success, 1-13=error
  3: payload       // operation-specific (optional)
}
```

**Notification** (device -> controller, unsolicited):
```cbor
{
  1: 0,                // messageId 0 = notification
  2: subscriptionId,   // uint32
  3: endpointId,       // uint8
  4: featureId,        // uint8
  5: changes           // map[attrId]value
}
```

### Operations

```go
OpRead      = 1  // Get attribute values
OpWrite     = 2  // Set attribute values
OpSubscribe = 3  // Register for notifications (or unsubscribe)
OpInvoke    = 4  // Execute command
```

### Status Codes

| Code | Name | Description |
|------|------|-------------|
| 0 | Success | |
| 1 | InvalidEndpoint | Endpoint not found |
| 2 | InvalidFeature | Feature not found |
| 3 | InvalidAttribute | Attribute not found |
| 4 | InvalidCommand | Command not found |
| 5 | InvalidParameter | Bad request payload |
| 6 | ReadOnly | Attribute is read-only |
| 7 | WriteOnly | Attribute is write-only |
| 8 | NotAuthorized | |
| 9 | Busy | Retry later |
| 10 | Unsupported | Operation not supported |
| 11 | ConstraintError | Value failed constraint |
| 12 | Timeout | |
| 13 | ResourceExhausted | |

### Payload Types

| Operation | Request Payload | Response Payload |
|-----------|----------------|-----------------|
| Read | `{1: [attrId...]}` or omitted (all) | `map[uint16]any` (attr values) |
| Write | `map[uint16]any` (new values) | `map[uint16]any` (actual values) |
| Subscribe | `{1: [attrId...], 2: minInterval, 3: maxInterval}` | `{1: subscriptionId, 2: currentValues}` |
| Invoke | `{1: commandId, 2: parameters}` | command-specific |
| Unsubscribe | `{1: subscriptionId}` (with EP=0, FID=0) | |

### Message Type Detection

`PeekMessageType(data) MessageType` -- lightweight detection without full decode:
- messageId=0 -> Notification
- key 1 is 1-3 with no EP/FID keys -> Control
- has EP + FID as integers -> Request
- otherwise -> Response

### State Enums (defined in wire, used across stack)

**ControlState:** Autonomous(0), Controlled(1), Limited(2), Failsafe(3), Override(4)

**ProcessState:** None(0), Available(1), Scheduled(2), Running(3), Paused(4), Completed(5), Aborted(6)

**OperatingState:** Normal(0), Standby(1), Error(2), Maintenance(3), Offline(4)

**ZoneType:** Grid(1), Local(2), Test(3) -- with Priority() method

### CBOR Quirks

After CBOR round-trip, maps become `map[any]any` with `uint64` keys. Helper functions handle this:
- `ExtractReadAttributeIDs(payload) []uint16`
- `ExtractWritePayload(payload) WritePayload`
- `ExtractSubscribePayload(payload) *SubscribePayload`
- `ToInt64()`, `ToUint32()`, `ToUint8Public()` -- safe type coercion

### Architectural Notes

The wire package is clean. One concern: `CommandError` type (returned by feature command handlers) couples wire-level status codes into the feature layer. This is intentional for ergonomics but means features import wire.

---

## 3. pkg/model -- Device Model

### Responsibility

Defines the 3-level device hierarchy: Device > Endpoint > Feature > Attribute/Command.

### Core Types

**Device:**
- Fields: `deviceID`, `vendorID`, `productID`, `serialNumber`, `firmwareVersion`, `endpoints map[uint8]*Endpoint`
- Endpoint 0 always exists (DEVICE_ROOT with DeviceInfo)
- Methods: `AddEndpoint()`, `GetEndpoint()`, `GetFeature()`, `ReadAttribute()`, `WriteAttribute()`, `InvokeCommand()`, `FindEndpointsByType()`, `FindEndpointsWithFeature()`

**Endpoint:**
- Fields: `ID uint8`, `Type EndpointType`, `Label string`, `features map[FeatureType]*Feature`
- Methods: `AddFeature()`, `GetFeature()`, `HasFeature()`

**Feature:**
- Fields: `featureType`, `revision`, `featureMap` (capability bitmap), `attributes map[uint16]*Attribute`, `commands map[uint8]*Command`
- Global attributes (always present): `featureMap` (0xFFFC), `attributeList` (0xFFFD), `commandList` (0xFFFE)
- `ReadHook func(ctx, attrID) (any, bool)` -- context-aware read interception (used by LimitResolver for per-zone values)
- `FeatureSubscriber` interface for change notifications
- Methods: `ReadAttribute()`, `ReadAttributeWithContext()`, `WriteAttribute()`, `InvokeCommand()`, `Subscribe()`

**Attribute:**
- Fields: `ID uint16`, `Name`, `Type DataType`, `Access` (Read/Write/Subscribe flags), `Default`, `Unit`, `Nullable`, `Constraint`
- Dirty tracking for subscription notifications

**Command:**
- Fields: `ID uint8`, `Name`, `Handler func(ctx, params) (any, error)`

### Generated Types (from YAML)

Endpoint types: DeviceRoot(0), EVCharger(1), Inverter(2), Battery(3), PVString(4), HeatPump(5), Meter(6), GridConnection(7)

Feature types: Electrical(0x01), Measurement(0x02), EnergyControl(0x03), Status(0x05), DeviceInfo(0x06), ChargingSession(0x06), Tariff(0x07), Signals(0x08), Plan(0x09), TestControl(0xFE)

### Architectural Notes

The `ReadHook` mechanism is a pragmatic solution to the "per-zone attribute values" problem -- a single feature instance serves multiple zones, but `myConsumptionLimit` must return different values depending on which zone is reading. The hook intercepts reads and returns zone-specific values via context. This avoids duplicating features per zone but makes the data flow less obvious.

---

## 4. pkg/features -- Feature Implementations

### Responsibility

Implements feature business logic. Mix of generated code (from YAML definitions) and hand-written code.

### Generated vs Hand-Written

**Generated (`*_gen.go`):**
- Feature factory functions (`NewEnergyControl()`, etc.)
- Attribute constant definitions (`EnergyControlAttrConsumptionLimit = 0x0011`)
- Command constant definitions (`EnergyControlCmdSetLimit = 0x01`)
- Setter/getter methods
- Request/response structs (`SetLimitRequest`, `SetLimitResponse`)
- Enum types and constants
- Total: ~11,600 LOC across all generated files

**Hand-written:**
- Feature-specific logic (state machines, limit resolution)
- Command handler implementations
- Integration helpers

### EnergyControl (0x03) -- Most Complex Feature

**Control State Machine:**
```
AUTONOMOUS ──SetLimit/Setpoint──> CONTROLLED/LIMITED
     ↑                                  │
     │ ClearAll                         │ Disconnect+timeout
     │                                  ↓
     └──────────────────────── FAILSAFE
                                        │
                                Manual override
                                        ↓
                                   OVERRIDE
```

**LimitResolver** (`limit_resolver.go`):
- Tracks per-zone consumption/production limits via `zone.MultiZoneValue`
- Resolution: "most restrictive wins" (smallest limit is effective)
- Duration timers per zone via `duration.Manager` with zone index mapping
- Imports `pkg/zonecontext` directly to extract caller zone ID and zone type from context
- `OnZoneMyChange` callback for subscription notifications on per-zone attribute changes
- `Register()` wires handlers into EnergyControl and sets up ReadHook for `myConsumptionLimit`/`myProductionLimit`

**Key commands:** SetLimit, ClearLimit, SetCurrentLimits, ClearCurrentLimits, SetSetpoint, ClearSetpoint, Pause, Resume, Stop

### TestControl (0xFE) -- Test-Only Feature

Commands for test harness manipulation:
- `TriggerEnterCommissioningMode` -- open commissioning window
- `TriggerExitCommissioningMode`
- `TriggerRemoveZone(zoneID)`
- `SetFailsafeLimit(limitWatts)`
- Various state manipulation commands

Only available when `DeviceConfig.TestMode = true` and `TestEnableKey` matches.

### Architectural Notes

The generated/hand-written split works well. The code generation pipeline (`docs/features/<feature>/1.0.yaml` -> `mash-featgen` -> `*_gen.go`) is clean and the generated code is readable. LimitResolver's dependency on caller zone identity is cleanly resolved via the `pkg/zonecontext` shared package, avoiding any circular import issues.

---

## 5. pkg/zone -- Multi-Zone Management

### Responsibility

Tracks zone memberships and resolves multi-zone values (limits, setpoints).

### Core Types

**Zone:**
- Fields: `ID`, `Type` (GRID/LOCAL/TEST), `Connected`, `LastSeen`, `CommissionedAt`

**ZoneValue:**
- Fields: `ZoneID`, `ZoneType`, `Value int64`, `Duration`, `SetAt`, `ExpiresAt`
- Methods: `IsExpired()`, `Priority()`

**MultiZoneValue:**
- Fields: `Values map[zoneID]*ZoneValue`, `EffectiveValue`, `WinningZoneID`
- Resolution rules:
  - **Limits:** Most restrictive (smallest) wins
  - **Setpoints:** Highest priority (lowest ZoneType number) wins
  - **TEST zones:** Excluded from resolution (DEC-060, observer-only)

**Manager:**
- Max zones: 3 (GRID + LOCAL + TEST, per DEC-043/DEC-060)
- Operations: `AddZone()`, `RemoveZone()`, `GetZone()`, `GetZones()`

### Architectural Notes

Clean, focused package. The priority model (GRID > LOCAL > TEST) is well-defined. The exclusion of TEST zones from resolution is handled cleanly via zone type checks.

---

## 6. pkg/subscription -- Subscription Management

### Responsibility

Manages attribute subscriptions with coalescing, heartbeat, and bounce-back suppression.

### Core Types

**Manager:**
- Subscriptions by ID: `map[uint32]*Subscription`
- Feature index: `map[featureKey][]*Subscription` for efficient notification dispatch
- `OnNotification` callback to send to peer

**Subscription:**
- Fields: `SubscriptionID`, `EndpointID`, `FeatureID`, `AttributeIDs`
- `MinInterval` (coalescing window), `MaxInterval` (heartbeat)
- Accumulated changes, last notification time

### Behavior

- **Coalescing:** Deduplicates rapid changes within MinInterval window
- **Bounce-back suppression:** If value changes then returns to original within window, no notification sent (configurable, default off)
- **Priming:** On subscribe, immediately sends current values for all subscribed attributes
- **Heartbeat:** Sends current values every MaxInterval if no changes
- **Not persistent:** Subscriptions cleared on disconnect

### Service-Level Integration

The `pkg/service` package also has a `SessionSubscriptionTracker` that tracks inbound/outbound subscriptions per session:
- Inbound: remote subscribes to our features (controller -> device)
- Outbound: we subscribe to remote features (device -> controller)
- Separate ID spaces (inbound IDs start at 1, outbound at 1)

### Architectural Notes

Two subscription types exist with distinct roles:
- `pkg/subscription.Manager` -- coalescing/heartbeat logic (implements `service.SubscriptionTracker` interface)
- `pkg/service.SessionSubscriptionTracker` -- per-session inbound/outbound tracking (bidirectional ID management)

---

## 7. pkg/interaction -- Request/Response

### Responsibility

Provides the four-operation protocol interface. Server handles incoming requests (device-side), Client sends requests (controller-side).

### Client (controller-side)

```go
type Client struct {
    sender    RequestSender
    timeout   time.Duration        // Default: 10s (DEC-044)
    nextMsgID uint32               // Skips 0 (reserved for notifications)
    pending   map[uint32]chan *wire.Response
    notifyHandler func(*wire.Notification)
}
```

Four operations:
- `Read(ctx, endpointID, featureID, attrIDs) (map[uint16]any, error)`
- `Write(ctx, endpointID, featureID, attrs) (map[uint16]any, error)`
- `Subscribe(ctx, endpointID, featureID, opts) (subscriptionID, currentValues, error)`
- `Invoke(ctx, endpointID, featureID, commandID, params) (any, error)`

Plus `Unsubscribe(ctx, subscriptionID)` and `HandleResponse()`/`HandleNotification()` for message routing.

### Message ID Management

- Starts at 1, increments atomically
- Skips 0 on wraparound (0 reserved for notifications)
- Pending requests tracked in map with response channels
- Timeout: whichever expires first (context deadline or client timeout)

### Architectural Notes

Clean, minimal package. The `RequestSender` interface (`Send([]byte) error`) is the only coupling to transport. The client correctly handles concurrent requests via per-message-ID response channels.

---

## 8. pkg/commissioning -- PASE + Certificate Renewal

### Responsibility

Implements SPAKE2+ (PASE) handshake, certificate exchange, and in-session certificate renewal.

### SPAKE2+ Implementation

- Curve: P-256 (secp256r1), SHA-256 hashing, HKDF-SHA256 for KDF
- Setup codes: 8-digit decimal (27-bit entropy, 00000000-99999999)
- `Verifier` type stores `W0` and `L` (no password stored on device)

**PASEClientSession** handshake flow:
1. Send `PASERequest` (public value pA, client identity)
2. Receive `PASEResponse` (server public value pB)
3. Process server value, send `PASEConfirm`
4. Receive `PASEComplete`
5. Verify server confirmation
6. Return shared secret

**PASEServerSession** with message-gated locking (DEC-061):
1. `WaitForPASERequest(ctx, conn)` -- reads first message **without holding lock**
2. `CompleteHandshake(ctx, conn, req)` -- acquires lock, completes remaining steps
3. Prevents slow/malicious clients from blocking other zones

### Certificate Exchange Messages

| MsgType | Name | Direction |
|---------|------|-----------|
| 1-4 | PASE (Request/Response/Confirm/Complete) | Bidirectional |
| 10 | CSRRequest | Controller -> Device |
| 11 | CSRResponse | Device -> Controller |
| 12 | CertInstall | Controller -> Device |
| 13 | CertInstallResponse | Device -> Controller |
| 20 | CommissioningComplete | Controller -> Device |
| 255 | CommissioningError | Either direction |

### In-Session Certificate Renewal (DEC-047)

4-message protocol without TLS reconnection:

| MsgType | Name | Direction | Key Fields |
|---------|------|-----------|------------|
| 30 | CertRenewalRequest | Controller -> Device | 32-byte nonce, optional ZoneCA |
| 31 | CertRenewalCSR | Device -> Controller | PKCS#10 CSR, NonceHash (SHA256[0:16]) |
| 32 | CertRenewalInstall | Controller -> Device | New cert, sequence number |
| 33 | CertRenewalAck | Device -> Controller | Status, active sequence |

**Nonce binding (DEC-047):** `ComputeNonceHash(nonce) = SHA256(nonce)[0:16]`. Device includes hash in CSR response. Controller validates before signing. Prevents replay attacks where attacker captures CSR from one session and uses it in another.

### Error Codes

| Code | Name | Notes |
|------|------|-------|
| 0 | Success | |
| 1 | AuthFailed | Generic (DEC-047: replaces InvalidPublicKey/ConfirmFailed) |
| 3 | CSRFailed | |
| 4 | CertInstallFailed | |
| 5 | Busy | DEC-063: device busy, RetryAfter in ms |
| 10 | ZoneTypeExists | Device already has this zone type |

### Architectural Notes

The message-gated locking pattern (DEC-061) is well-designed -- it prevents a slow client from monopolizing the commissioning lock. The generic error code (DEC-047) prevents information leakage about why authentication failed. The nonce binding for renewal is a solid anti-replay mechanism.

---

## 9. pkg/cert -- Certificate Management

### Responsibility

Certificate generation, storage, and lifecycle management.

### Certificate Hierarchy

```
Zone CA (self-signed, 20-year validity)
  └── Operational Cert (signed by Zone CA, 1-year validity)
       ├── Renewal window: 30 days before expiry
       └── Grace period: 7 days after expiry
```

### Core Types

**ZoneCA:**
- `Certificate *x509.Certificate`, `PrivateKey *ecdsa.PrivateKey`, `ZoneID string`, `ZoneType ZoneType`

**OperationalCert:**
- `Certificate`, `PrivateKey`, `ZoneID`, `ZoneType`, `ZoneCACert`
- Methods: `SKI()`, `ExpiresAt()`, `NeedsRenewal()`, `IsExpired()`, `IsInGracePeriod()`, `TLSCertificate()`

**DeviceIdentity:**
- `DeviceID`, `VendorID`, `ProductID`, `SerialNumber`

### Store Interface

```go
type Store interface {
    GetOperationalCert(zoneID) (*OperationalCert, error)
    SetOperationalCert(*OperationalCert) error
    RemoveOperationalCert(zoneID) error
    ListZones() []string
    ZoneCount() int
    GetZoneCACert(zoneID) (*x509.Certificate, error)
    SetZoneCACert(zoneID, *x509.Certificate) error
    GetAllZoneCAs() []*x509.Certificate
    Save() error
    Load() error
}
```

Implementations: `MemoryStore` (testing), `FileStore` (PEM files), `FileControllerStore` (holds Zone CA private key).

### Fingerprinting

`Fingerprint(cert) string` -- first 64 bits (16 hex chars) of certificate SHA-256. Used for Zone ID and Device ID derivation from SKI (Subject Key Identifier).

### Architectural Notes

Clean separation between cert generation and storage. The Store interface allows easy testing with MemoryStore while FileStore provides persistence. The ControllerStore extension for Zone CA private key management is a reasonable specialization.

---

## 10. pkg/discovery -- mDNS

### Responsibility

mDNS-based service discovery with 4 service types.

### Service Types

| Type | mDNS Name | Purpose | Transport |
|------|-----------|---------|-----------|
| Commissionable | `_mashc._udp` | Devices in commissioning mode | UDP |
| Operational | `_mash._tcp` | Commissioned devices | TCP |
| Pairing Request | `_mashp._udp` | Controller-initiated pairing signal | UDP |

### TXT Records

**Commissionable:** D (discriminator), cat (categories), serial, brand, model, DN (device name)

**Operational:** ZI (zone ID, 16 hex), DI (device ID, 16 hex), VP (vendor:product), FW, FM (feature map), EP (endpoint count)

**Commissioner:** ZN (zone name), ZI, VP, DN, DC (device count)

**Pairing Request:** D (discriminator), ZI, ZN

### Device Categories

| Code | Category |
|------|----------|
| 1 | GCPH (Grid Connection Point Hub) |
| 2 | EMS (Energy Management System) |
| 3 | E-Mobility (EVSE) |
| 4 | HVAC |
| 5 | Inverter |
| 6 | Domestic Appliance |
| 7 | Metering |

### DiscoveryManager

State machine: `Unregistered -> Unconnected -> CommissioningOpen -> Operational -> OperationalCommissioning`

Coordinates advertising and browsing. Manages commissioning window timer (default 15 min, DEC-048: re-triggerable, max 3 hours).

### QR Code Format

`MASH:<version>:<discriminator>:<setupcode>:<vendorid>:<productid>`

### Architectural Notes

Well-structured with clean separation between advertising and browsing. The `DiscoveryManager` state machine correctly handles the commissioning window lifecycle. The quiet mode for testing (no network operations) is a good testing pattern.

---

## 11. pkg/service -- Service Orchestration

This is the largest and most complex package. It orchestrates all other packages into working device and controller services.

### DeviceService

**Lifecycle:** Idle -> Starting -> Running -> Stopping -> Stopped

**Key fields:**
- `device *model.Device` -- the device model
- `discoveryManager` -- mDNS advertising
- `tlsListener` / `tlsConfig` / `tlsCert` -- TLS server
- `verifier *commissioning.Verifier` -- SPAKE2+ verifier
- `failsafeTimers map[zoneID]*failsafe.Timer` -- per-zone failsafe
- `durationManager *duration.Manager` -- timer-based command durations
- `subscriptionManager SubscriptionTracker` -- subscription coalescing (interface)
- `zoneSessions map[zoneID]*ZoneSession` -- active operational connections
- `connTracker` -- stale connection reaper (DEC-064)
- `paseTracker` -- PASE attempt tracking for backoff (DEC-047)
- `activeConns atomic.Int32` -- transport connection cap (DEC-062)

**Connection lifecycle:**
```
1. Device advertises _mashc._udp (commissioning)
2. Controller connects via TLS (InsecureSkipVerify)
3. PASE handshake (SPAKE2+) authenticates both sides
4. Certificate exchange (CSR -> sign -> install)
5. CommissioningComplete
6. [Connection may be reused or closed]
7. Controller reconnects via operational TLS (mutual cert auth)
8. ZoneSession created, message loop starts
9. On disconnect: failsafe timer starts
```

### ControllerService

**Key fields:**
- `zoneID`, `zoneName` -- controller's zone identity
- `browser`, `advertiser`, `discoveryManager` -- mDNS
- `connectedDevices map[deviceID]*ConnectedDevice`
- `deviceSessions map[deviceID]*DeviceSession`
- `activePairingRequests map[discriminator]context.CancelFunc`

**Commission flow:**
1. Browse for `_mashc._udp` services
2. Match by discriminator
3. Connect via commissioning TLS
4. PASE handshake
5. Certificate exchange (controller signs device CSR with Zone CA)
6. CommissioningComplete
7. Disconnect, reconnect via operational TLS
8. Create DeviceSession

### ZoneSession (device-side, wraps controller connection)

```go
type ZoneSession struct {
    zoneID  string
    conn    Sendable               // Abstract connection
    handler *ProtocolHandler       // Handles incoming requests
    client  *interaction.Client    // Sends requests to controller
}
```

Message routing in `OnMessage()`:
1. Renewal messages (MsgType 30-33) -> renewal handler
2. `wire.MessageTypeRequest` -> `handler.HandleRequest()` (controller reading device)
3. `wire.MessageTypeResponse` -> `client.HandleResponse()` (response to device's request)
4. `wire.MessageTypeNotification` -> `client.HandleNotification()`

### DeviceSession (controller-side, wraps device connection)

```go
type DeviceSession struct {
    deviceID string
    conn     Sendable
    client   *interaction.Client    // Sends requests to device
    sender   *TransportRequestSender
    handler  *ProtocolHandler       // Optional: for device -> controller requests
}
```

### ProtocolHandler

Processes incoming requests against the device model. Uses the `DeviceModel` interface (not concrete `*model.Device`) for testability:

```go
HandleRequest(req *wire.Request) *wire.Response
  -> handleRead()    // Read attributes with context
  -> handleWrite()   // Write with constraint checking
  -> handleSubscribe() // Add/remove subscriptions
  -> handleInvoke()  // Execute commands
```

**Context propagation:** Each request gets context with caller's zone ID and zone type (via `pkg/zonecontext`), enabling per-zone authorization and attribute interception.

### Service-Layer Interfaces (`interfaces.go`)

Two interfaces decouple the service layer from concrete types:
- `SubscriptionTracker` -- 8-method interface wrapping `*subscription.Manager` for subscription coalescing
- `DeviceModel` -- 16-method interface wrapping `*model.Device` for device model operations

### Security Hardening

| DEC | Feature | Implementation |
|-----|---------|----------------|
| DEC-047 | PASE backoff | `PASEAttemptTracker` with 4 tiers: [0ms, 1s, 3s, 10s] based on failure count |
| DEC-047 | Generic errors | Single `ErrCodeAuthFailed` instead of specific error codes |
| DEC-047 | Nonce binding | SHA256(nonce)[0:16] in renewal CSR |
| DEC-061 | Message-gated locking | Read first PASE message without lock, then acquire |
| DEC-062 | Connection cap | `MaxZones + 1` pre-operational connections |
| DEC-064 | Stale reaper | Closes connections older than 90s that haven't completed commissioning |
| DEC-065 | Connection cooldown | 500ms minimum between connection attempts |

### Event System

```go
type EventType uint8
// EventConnected, EventDisconnected, EventCommissioned, EventDecommissioned,
// EventValueChanged, EventFailsafeStarted/Triggered/Cleared,
// EventCommissioningOpened/Closed, EventDeviceDiscovered/Gone,
// EventZoneRemoved/Restored, EventDeviceReconnected, EventCertificateRenewed, ...
```

Services expose `OnEvent(handler EventHandler)` for application-level callbacks.

### Failsafe Timer Management

One timer per zone:
- Created when zone is commissioned
- Restarted when zone reconnects
- On expiry: `handleFailsafe(zoneID)` marks zone disconnected, emits event, EnergyControl enters FAILSAFE state
- Default timeout: 2 hours

### DeviceConfig

Key fields (beyond basics):
```go
MaxZones                  int           // Default: 2 (GRID + LOCAL)
FailsafeTimeout           time.Duration // Default: 2h
CommissioningWindowDuration time.Duration // Default: 15m
ConnectionCooldown         time.Duration // Default: 500ms (DEC-065)
PASEFirstMessageTimeout    time.Duration // Default: 5s (DEC-061)
HandshakeTimeout           time.Duration // Default: 85s
PASEBackoffEnabled         bool          // Default: true (DEC-047)
PASEBackoffTiers           [4]time.Duration // [0, 1s, 3s, 10s]
GenericErrors              bool          // Default: true (DEC-047)
StaleConnectionTimeout     time.Duration // Default: 90s (DEC-064)
TestMode                   bool
TestEnableKey              string
```

### ControllerConfig

```go
ZoneType              cert.ZoneType
ZoneName              string
SubscriptionMinInterval time.Duration // Default: 1s
SubscriptionMaxInterval time.Duration // Default: 60s
EnableAutoReconnect     bool          // Default: true
EnableBounceBackSuppression bool      // Default: true
PairingRequestTimeout   time.Duration // Default: 1h
```

### Sendable / TransportRequestSender

```go
type Sendable interface { Send(data []byte) error }
type framedConnection struct { conn io.ReadWriteCloser; framer *transport.Framer }
type TransportRequestSender struct { conn Sendable } // Adapts Sendable to RequestSender
```

### Architectural Notes

The service package is the most complex part of the stack. DeviceService is large but its responsibilities are inherently complex (TLS server, commissioning, zone management, failsafe timers, subscriptions, discovery, security hardening). The bidirectional session model (ZoneSession/DeviceSession both have handler + client) is clean but adds complexity.

Key dependencies are abstracted behind interfaces (`SubscriptionTracker`, `DeviceModel`) defined in `interfaces.go`, enabling focused unit tests without full service wiring.

The context-based zone ID propagation is clean -- a Read request flows through ProtocolHandler -> Feature -> ReadHook -> LimitResolver, with zone identity passing through context (via `pkg/zonecontext`) at each step.

---

## 12. pkg/inspect -- Name Resolution

### Responsibility

Maps human-readable names to numeric IDs for features, attributes, commands, and endpoints.

### Name Resolution

```go
ResolveEndpointName("evcharger")           -> 1
ResolveFeatureName("energycontrol")        -> 0x03
ResolveAttributeName(0x03, "consumptionLimit") -> 0x0011
ResolveCommandName(0x03, "setLimit")       -> 0x01
```

All lookups are case-insensitive (lowercase internally). Tables are generated from YAML feature definitions by `mash-featgen` into `names_gen.go`. The resolver functions remain in `names.go`.

### Inspector

Wraps a `model.Device` for inspection:
- `InspectDevice() *DeviceTree` -- full tree
- `ReadAttribute(path) (value, metadata, error)`
- `WriteAttribute(path, value) error`

### Architectural Notes

The name tables are now generated from the same YAML definitions as features (`mash-featgen`), ensuring they stay in sync automatically when attributes or commands are added.

---

## 13. pkg/persistence -- State Storage

### Responsibility

JSON-based persistence for device and controller runtime state.

### DeviceState

```go
type DeviceState struct {
    Version       int
    SavedAt       time.Time
    Zones         []ZoneMembership         // Zone ID, type, controller, join time
    FailsafeState map[string]FailsafeSnapshot // Timer state per zone
    ZoneIndexMap  map[string]uint8          // Consistent zone-to-index mapping
}
```

### ControllerState

```go
type ControllerState struct {
    Version int
    SavedAt time.Time
    ZoneID  string
    Devices []DeviceMembership           // Device ID, type, join/last-seen
}
```

Both use `*StateStore` with `Save()`, `Load()`, `Clear()` backed by JSON files.

### What Survives Restart

- Zone memberships (which zones the device belongs to)
- Failsafe timer state (can resume countdown)
- Zone index mapping (consistent endpoint assignments)
- Controller's device list

### What Does NOT Survive

- Active connections (must reconnect)
- Subscriptions (must re-subscribe)
- In-flight requests
- PASE session state

---

## 14. Cross-Cutting Concerns

### Error Handling

- Wire-level: `wire.Status` codes in responses (0-13)
- Command-level: `wire.CommandError{Status, Message}` returned by feature handlers
- Transport-level: Go errors (`ErrNotConnected`, `ErrConnectionClosed`, etc.)
- Commissioning: Error codes (0-10, 255) in CBOR messages
- No unified error type across the stack -- each layer has its own

### Context Propagation

- Zone ID and zone type injected into context for per-zone authorization
- `pkg/zonecontext` package provides `ContextWithCallerZoneID()` / `CallerZoneIDFromContext()` and zone type equivalents
- Features receive context during Read/Write/Invoke
- LimitResolver imports `pkg/zonecontext` directly to extract zone identity from context

### Logging

- Application logging: `*slog.Logger` (structured, per-service)
- Protocol logging: `log.Logger` interface (CBOR format, per-connection)
- Capability snapshots: Periodic device structure capture for protocol analysis
- Separate concerns: application logs for debugging, protocol logs for analysis

### Code Generation Boundary

| What's Generated | Source | Tool |
|-----------------|--------|------|
| Feature types, attributes, commands, enums | `docs/features/<feature>/1.0.yaml` | `mash-featgen` |
| Endpoint types, feature types (model) | `docs/features/protocol-versions.yaml` | `mash-featgen` |
| Name resolution tables (inspect) | `docs/features/<feature>/1.0.yaml` | `mash-featgen` |
| Use case definitions | `docs/usecases/1.0/*.yaml` | `mash-ucgen` |

Generated files: `*_gen.go`. Never edit directly. Hand-written code coexists in same packages.

### Concurrency Model

- Goroutine-per-connection for TLS accept loop
- Goroutine-per-session for message loops (ZoneSession, DeviceSession)
- Mutex-protected shared state (zone manager, subscription manager, etc.)
- Atomic operations for connection caps and message IDs
- Context-based cancellation for graceful shutdown

---

## 15. Dependency Graph (Simplified)

```
cmd/mash-device ──→ pkg/service ──→ pkg/interaction
cmd/mash-controller ─┘    │        pkg/model
                           │        pkg/features
                           │        pkg/zone
                           │        pkg/subscription
                           │        pkg/commissioning
                           │        pkg/cert
                           │        pkg/discovery
                           │        pkg/transport
                           │        pkg/wire
                           │        pkg/persistence
                           │        pkg/inspect
                           │        pkg/log
                           │
internal/testharness ──────┘ (creates DeviceService + ControllerService)
```

The test harness depends on the same service layer as the real applications, which is good for test fidelity but means the harness inherits all the complexity of the service layer.
