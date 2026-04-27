# MASH Protocol Overview

> **M**inimal **A**pplication-layer **S**mart **H**ome Protocol
>
> A lightweight, streamlined replacement for EEBUS SHIP/SPINE

**Status:** Active
**Created:** 2025-01-24
**Last Updated:** 2026-01-31

---

## Related Documents

| Document | Description |
|----------|-------------|
| [Transport](transport.md) | TCP/TLS, framing, keep-alive, connection model |
| [Security](security.md) | Certificates, commissioning, zones, pairing |
| [Discovery](discovery.md) | mDNS, capability discovery, QR codes |
| [Interaction Model](interaction-model.md) | Read/Write/Subscribe/Invoke semantics |
| [Multi-Zone](multi-zone.md) | Zone types, roles, priority resolution |
| [Features](features/README.md) | Feature definitions and registry |
| [Decision Log](decision-log.md) | Design decisions and rationale |
| [Protocol Comparison](protocol-comparison.md) | MASH vs Matter 1.5 vs EEBUS |
| [Matter Comparison](matter-comparison.md) | PKI and certificate model deep-dive |

---

## 1. Vision & Goals

### 1.1 Problem Statement

EEBUS (SHIP + SPINE) has fundamental design flaws that make it:
- **Complex**: 50+ specification ambiguities, 7,000+ implementation variations
- **Hard to implement**: Race conditions, undefined behaviors, no test specs
- **Resource-heavy**: Designed for flexibility over efficiency
- **Lacking orchestration**: Communication without coordination

### 1.2 Design Goals

| Priority | Goal | Description |
|----------|------|-------------|
| P0 | **Simple** | Implementable in days, not months |
| P0 | **Deterministic** | No ambiguities, no race conditions |
| P0 | **Lightweight** | Runs on 256KB RAM MCUs |
| P1 | **Secure** | Modern security without complexity |
| P1 | **Testable** | Reference implementation + test suite from day one |
| P2 | **Extensible** | New use cases without breaking changes |

### 1.3 Non-Goals

- Full backwards compatibility with EEBUS
- Support for every conceivable future use case
- Complex trust hierarchies
- Cloud-first architecture

---

## 2. Architecture Overview

### 2.1 Layer Model (Inspired by Matter)

```
┌─────────────────────────────────────────────┐
│           Application / Use Cases           │  <- EV Charging, Load Control, etc.
├─────────────────────────────────────────────┤
│              Data Model Layer               │  <- Features, Attributes, Commands
│              (CBOR serialization)           │
├─────────────────────────────────────────────┤
│           Interaction Model Layer           │  <- Read, Write, Subscribe, Invoke
├─────────────────────────────────────────────┤
│         Transport Layer (TCP/TLS)           │  <- Length-prefixed framing
├─────────────────────────────────────────────┤
│            Discovery Layer                  │  <- mDNS/DNS-SD + QR commissioning
└─────────────────────────────────────────────┘
```

### 2.2 Key Simplifications vs EEBUS

| EEBUS Complexity | MASH Simplification |
|------------------|---------------------|
| SHIP + SPINE as separate specs | Single unified specification |
| 7 RFE operation modes | 4 operations: Read, Write, Subscribe, Invoke |
| Features with Functions | Features with Attributes + Commands |
| Bindings + Subscriptions (separate) | Unified subscription model |
| Double connection race condition | Single connection per device pair |
| Trust levels 0-100 | Binary trust: paired or not |
| 250+ data structures | Small set of core features |

---

## 3. Devices & Controllers

MASH defines two roles:

| Role | Acts As | Examples |
|------|---------|----------|
| **Device** | Server | EVSE, inverter, heat pump, battery |
| **Controller** | Client | EMS, smart meter gateway (SMGW), phone app |

### 3.1 Connection Model

- **Controller always initiates** the connection (eliminates EEBUS double-connection race condition)
- **One persistent TCP/TLS connection** per controller-device pair
- **Bidirectional** communication over the same connection (either side can send requests)
- Automatic reconnect with exponential backoff

### 3.2 Pairing

Before communicating, a controller and device must be paired:

1. Device advertises itself via mDNS
2. Controller discovers the device and initiates pairing
3. User confirms via setup code (QR code / 8-digit code)
4. Cryptographic handshake (SPAKE2+) establishes trust
5. Controller issues a certificate to the device

After pairing, all communication is encrypted and mutually authenticated via TLS 1.3.

> **Detailed flows:** [Security & Commissioning](#8-security-commissioning)

---

## 4. Device Model

A device exposes its capabilities through a three-level hierarchy. The device contains endpoints, and each endpoint contains features. Endpoints represent physical or logical subsystems (e.g., an EV charging port, a battery, a PV string). Features represent specific capabilities within an endpoint (e.g., power measurement, energy control). Features are defined in the [next section](#5-features).

**Hierarchy:** Device > Endpoint > Feature

```
Device (evse-001)
├── Endpoint 0 (type: DEVICE_ROOT)
│   └── DeviceInfo
├── Endpoint 1 (type: EV_CHARGER, label: "Port 1")
│   ├── Electrical           ← Phase config, ratings (static)
│   ├── Measurement          ← Power, energy readings (telemetry)
│   ├── EnergyControl        ← Limits, control (commands)
│   └── ChargingSession      ← EV session info
└── Endpoint 2 (type: EV_CHARGER, label: "Port 2")  [optional for dual-port]
    ├── Electrical
    ├── Measurement
    ├── EnergyControl
    └── ChargingSession
```

### 4.1 Hybrid Inverter Example

```
Device (hybrid-inverter-001)
├── Endpoint 0 (type: DEVICE_ROOT)
│   └── DeviceInfo
├── Endpoint 1 (type: INVERTER, label: "Grid Connection")
│   ├── Electrical           ← AC phase config, ratings
│   ├── Measurement          ← AC power, energy, voltage, current
│   └── EnergyControl        ← Grid limits (LPC/LPP)
├── Endpoint 2 (type: PV_STRING, label: "Roof South")
│   ├── Electrical           ← DC ratings
│   └── Measurement          ← DC power, voltage, current, yield
├── Endpoint 3 (type: PV_STRING, label: "Roof West")
│   ├── Electrical
│   └── Measurement
└── Endpoint 4 (type: BATTERY, label: "LG Chem RESU")
    ├── Electrical           ← DC ratings, capacity
    ├── Measurement          ← DC power, SoC, SoH, temperature
    └── EnergyControl        ← Battery control (COB)
```

### 4.2 EndpointType Enum

```
DEVICE_ROOT       = 0x00  // Device-level info (always endpoint 0)
GRID_CONNECTION   = 0x01  // AC grid connection point (smart meter)
INVERTER          = 0x02  // Inverter AC side (grid-facing)
PV_STRING         = 0x03  // PV string / solar input (DC)
BATTERY           = 0x04  // Battery storage (DC)
EV_CHARGER        = 0x05  // EVSE / wallbox
HEAT_PUMP         = 0x06  // Heat pump
WATER_HEATER      = 0x07  // Water heater / boiler
HVAC              = 0x08  // HVAC system
APPLIANCE         = 0x09  // Generic controllable appliance
SUB_METER         = 0x0A  // Sub-meter / circuit monitor
```

### 4.3 Topology

**Topology is implicit from EndpointType:**
- AC endpoints (INVERTER, GRID_CONNECTION, EV_CHARGER) -> connect to grid
- DC endpoints (PV_STRING, BATTERY) -> internal to device, connect to DC bus
- No explicit parent/child relationships needed

### 4.4 Addressing

```
device_id / endpoint_id / feature_id / attribute_or_command
evse-001  / 1           / Measurement / acActivePower
```

**Endpoint Conventions:**
- Endpoint 0: Reserved for root device metadata (type: DEVICE_ROOT)
- Endpoint 1+: Functional endpoints with appropriate EndpointType

---

## 5. Features

Features are the building blocks of device functionality. Each feature groups related attributes (data) and commands (actions) around a single concern.

Features are organized by concern, separating static configuration from dynamic telemetry and control:

| Feature | Question | Update Frequency |
|---------|----------|------------------|
| **Electrical** | "What CAN this do?" | Rarely (hardware/installation) |
| **Measurement** | "What IS it doing?" | Constantly (telemetry) |
| **EnergyControl** | "What SHOULD it do?" | On command (control) |
| **Status** | "Is it working?" | On state change |

### 5.1 Feature Registry

See [`features/protocol-versions.yaml`](features/protocol-versions.yaml) for the canonical Feature ID registry — both `mash-go` and `mash-py` codegen consume it directly. Prose tables are illustrative only.

### 5.2 Feature Map

The `featureMap` global attribute is a 32-bit bitmap for quick capability discovery:

```
bit 0  (0x0001): CORE       - EnergyCore basics (always set)
bit 1  (0x0002): FLEX       - Flexible power adjustment
bit 2  (0x0004): BATTERY    - Battery-specific (SoC, SoH)
bit 3  (0x0008): EMOB       - E-Mobility/EVSE
bit 4  (0x0010): SIGNALS    - Incentive signals
bit 5  (0x0020): TARIFF     - Tariff data
bit 6  (0x0040): PLAN       - Power plan
bit 7  (0x0080): PROCESS    - Process lifecycle (OHPCF)
bit 8  (0x0100): FORECAST   - Power forecasting
bit 9  (0x0200): ASYMMETRIC - Per-phase asymmetric control
bit 10 (0x0400): V2X        - Vehicle-to-grid/home
```

**Discovery pattern:** Read `featureMap` for quick filtering ("show me all V2X devices"), then read feature attributes for details ("can it do asymmetric V2G?").

> **Full specification:** [Features](features/README.md)

---

## 6. Interaction Model

MASH uses 4 operations inspired by Matter (replacing SPINE's 7 RFE modes):

| Operation | Description | Example |
|-----------|-------------|---------|
| **Read** | Get current attribute values | Read power measurement |
| **Write** | Set attribute value (full replace) | Write power limit |
| **Subscribe** | Register for change notifications | Subscribe to power changes |
| **Invoke** | Execute command with parameters | StartCharging(targetSoC: 80) |

### 6.1 Message Format

All messages use CBOR (RFC 8949) with integer keys for compactness.
Typical messages are under 2 KB (vs 4 KB+ in EEBUS SPINE with JSON).

A request specifies an operation, target endpoint and feature, and payload:

```
Request  -> { messageId, operation, endpointId, featureId, payload }
Response -> { messageId, status, payload }
```

> **Full message format:** [Interaction Model](interaction-model.md)

### 6.2 Endpoint Discovery

After connecting, a controller reads the endpoint list to discover device structure:

```cbor
{
  endpoints: [
    { id: 0, type: DEVICE_ROOT, features: [DeviceInfo] },
    { id: 1, type: INVERTER, label: "Grid", features: [Electrical, Measurement, EnergyControl] },
    { id: 2, type: PV_STRING, label: "South", features: [Electrical, Measurement] },
    { id: 3, type: BATTERY, label: "Battery", features: [Electrical, Measurement, EnergyControl] }
  ]
}
```

### 6.3 Subscription Behavior

Subscriptions deliver an initial priming report (all current values) followed by delta notifications (only changed attributes). Interval parameters control notification frequency:

| Parameter | Purpose |
|-----------|---------|
| minInterval | Minimum time between notifications (coalescing) |
| maxInterval | Maximum time without notification (heartbeat) |

Subscriptions are lost on disconnect. Clients must re-subscribe after reconnection.

> **Full specification:** [Interaction Model](interaction-model.md)

---

## 7. Transport & Discovery

### 7.1 Transport

MASH runs over IPv6 with TCP and TLS 1.3 (mutual authentication).
Messages are length-prefixed (4-byte big-endian header) CBOR payloads.
Maximum message size is 8 KB (per DEC-069). Keep-alive uses application-layer
ping/pong every 30 seconds.

> **Full specification:** [Transport](transport.md)

### 7.2 Network Discovery

MASH uses mDNS/DNS-SD for network discovery with four service types:

| Service Type | Purpose | Advertised By |
|--------------|---------|---------------|
| `_mash-comm._tcp` | Commissionable device | Device in commissioning mode |
| `_mash._tcp` | Operational device | Commissioned device |
| `_mashp._udp` | Pairing request | Controller seeking specific device |

### 7.3 QR Code Format

```
MASH:<version>:<discriminator>:<setupcode>:<vendorid>:<productid>
Example: MASH:1:1234:12345678:0x1234:0x5678
```

The QR code carries an 8-digit setup code (~27 bits entropy) used for SPAKE2+ during commissioning.

### 7.4 Capability Discovery

After connecting, controllers discover device capabilities through global attributes:

1. **Read endpoint list** -- discover device structure
2. **Read `specVersion`** from DeviceInfo (endpoint 0) -- protocol compatibility check
3. **Read global attributes** per endpoint:
   - `featureMap` (bitmap32) -- which feature sets are supported (see [Feature Map](#52-feature-map))
   - `attributeList` -- which specific attributes are implemented
   - `acceptedCommandList` -- which commands can be invoked

This enables self-describing devices: controllers know exactly what is available without trial and error.

> **Full specification:** [Discovery](discovery.md)

---

## 8. Security & Commissioning

MASH uses binary trust (paired or not paired) with certificate-based identity. No EEBUS-style trust levels 0-100.

### 8.1 Trust Model

Trust is binary: a device is either paired with a controller (and they share a Zone CA) or it is not. There is no partial trust, no trust levels, and no trust negotiation.

### 8.2 Certificate Hierarchy

```
┌─────────────────────────────────────────────────────────┐
│  Zone CA (controller-generated, 20yr)                   │
│    ├── Controller Operational Cert (1yr)                │
│    └── Device Operational Certs (1yr each)              │
└─────────────────────────────────────────────────────────┘
```

Controller and device operational certificates are **siblings** -- both signed by the Zone CA, independently issued and renewed.

### 8.3 Certificate Lifecycle

| Certificate | Validity | Renewal | Revocation |
|-------------|----------|---------|------------|
| Device Operational | 1 year | Auto by controller (7 days before expiry, per DEC-077) | RemoveZone command |
| Controller Operational | 1 year | Auto (self-renewal) | Zone CA rotation |
| Zone CA | 20 years | Manual | Zone dissolution |

### 8.4 Commissioning Flow

Commissioning establishes trust between a controller and a device using SPAKE2+ (same as Matter).

```
Controller                         Device
     │                               │
     │◄── mDNS: _mash-comm._tcp ────────┤  Device advertising
     │                               │
     │── TCP Connect ───────────────►│
     │                               │
     │◄── SPAKE2+ (setup code) ─────►│  From QR code (8 digits)
     │                               │
     │── Request CSR ───────────────►│
     │◄── CSR with new key pair ─────┤
     │                               │
     │── Install Operational Cert ──►│  Signed by Zone CA
     │                               │
     │── Commissioning Complete ────►│
```

### 8.5 Admin Authorization

Phone apps act as zone admins, not owners. The EMS generates a temporary QR code; the app scans it, completes SPAKE2+, and receives an admin token. The app can then commission devices on behalf of the EMS by forwarding CSRs for signing.

### 8.6 Delegated Commissioning

For grid operator zones, commissioning is delegated through a backend:

```
User           Phone App       DSO Backend       SMGW          Device
 │                 │               │               │              │
 │── Scan QR ─────►│── Upload ────►│── Forward ───►│              │
 │                 │               │               │── SPAKE2+ ──►│
 │                 │               │               │◄── Accept ───┤
```

The user scans the device QR code with an app, which uploads the setup info to the DSO backend. The backend provisions the SMGW, which commissions the device directly.

### 8.7 Operational Sessions

After commissioning, devices use mutual TLS authentication:

```
Controller                         Device
     │                               │
     │── TLS with Operational Cert ─►│  Mutual authentication
     │◄── Verify same Zone ──────────┤
     │                               │
     │◄── Encrypted Session ────────►│  Read/Write/Subscribe/Invoke
```

Both sides present operational certificates and verify they are signed by the same Zone CA. All subsequent communication is encrypted with TLS 1.3.

> **Full specification:** [Security](security.md)

---

## 9. Multi-Zone Model

A device can be controlled by multiple controllers simultaneously through the zone model. Devices support up to 5 concurrent controller zones. Each zone has independent certificates and priority.

### 9.1 Zone Types and Priority

| Zone Type | Priority | Typical Owner |
|-----------|----------|---------------|
| GRID | 1 (highest) | DSO, SMGW, grid operator |
| LOCAL | 2 | Residential/building EMS |
| TEST | 3 (lowest) | Test tools, observer |

```
┌─────────────────────────────────────────────────────┐
│                   Device (EVSE)                      │
├─────────────────────────────────────────────────────┤
│  Zone 1: GRID                                        │
│    └── Operational Cert from SMGW (priority 1)       │
│                                                      │
│  Zone 2: LOCAL                                       │
│    └── Operational Cert from EMS (priority 2)        │
│                                                      │
│  Max 1 zone per type (DEC-043)                       │
└─────────────────────────────────────────────────────┘
```

### 9.2 Zone Roles

| Role | Capabilities | Typical Entity |
|------|-------------|----------------|
| Zone Owner | Has Zone CA, issues certs | EMS, SMGW |
| Zone Admin | Can commission devices (forwards CSRs to owner) | Phone App |
| Zone Member | Normal participant with operational cert | Devices |

**Key principle:** Apps are admins, NOT owners. Losing a phone does not break zone operations.

### 9.3 Priority Resolution

Priority resolution is **per-feature**, not global:

```
LIMITS:    Most restrictive wins (all zones constrain together)
SETPOINTS: Highest priority zone wins (only one controller active)
```

**Example:**
```
SMGW sets consumptionLimit = 6000W (priority 1)
EMS sets consumptionLimit = 8000W (priority 3)
→ Effective limit: 6000W (SMGW wins -- most restrictive)
→ EMS is notified that its limit was overridden
```

### 9.4 Connection Model

- One persistent TCP/TLS connection per zone
- Maximum simultaneous connections: `max_zones + 1` (operational + commissioning)
- User physical override (button on device) is always possible

> **Full specification:** [Multi-Zone](multi-zone.md)

---

## 10. Use Case Coverage

### 10.1 Initial Target Use Cases

| Use Case | EEBUS Equivalent | Status |
|----------|------------------|--------|
| EV Charging Control | EVSE, EVCEM, CEVC | Defined |
| Load/Production Control | LPC, LPP | Defined |
| Battery Control | COB | Defined |
| Grid Signals | ToUT, POEN, ITPCM | Defined |
| Heat Pump Flexibility | OHPCF | Defined |
| Device Monitoring | MPC, MGCP, MOB, MOI, MPS | Defined |
| Flexible Load | FLOA | Defined |
| Power Demand Forecast | PODF | Defined |

### 10.2 Future Use Cases

- HVAC Control
- Smart Appliances (washing machine, dishwasher)
- Water Heater Control
- Demand Response Programs

---

## 11. References

### 11.1 Matter Protocol Resources

- [Matter Specification](https://csa-iot.org/developer-resource/specifications-download-request/)
- [Matter SDK](https://github.com/project-chip/connectedhomeip)
