# MASH Multi-Zone Architecture

> Zone types, roles, priority resolution, and multi-controller coordination

**Status:** Draft
**Last Updated:** 2026-01-30

---

## Related Documents

| Document | Description |
|----------|-------------|
| [Protocol Overview](protocol-overview.md) | Architecture overview, device model |
| [Security](security.md) | Certificates, commissioning, zone CA |
| [Transport](transport.md) | Connection model, TLS sessions |
| [EnergyControl](features/energy-control.md) | Limit/setpoint resolution algorithms |
| [Discovery](discovery.md) | mDNS service types, pairing requests |

**Behavior Specifications:**

| Document | Description |
|----------|-------------|
| [Zone Lifecycle](testing/behavior/zone-lifecycle.md) | Zone join/leave state machine |
| [Zone Management](testing/behavior/zone-management.md) | Zone operations and constraints |
| [Multi-Zone Resolution](testing/behavior/multi-zone-resolution.md) | Priority resolution test cases |
| [Failsafe Timing](testing/behavior/failsafe-timing.md) | Failsafe behavior on connection loss |

---

## 1. Overview

MASH devices can belong to multiple controller zones simultaneously. Each zone operates independently with its own certificates, connection, and priority level. This enables scenarios where a grid operator and a home energy manager both control the same device without conflicting.

**Key properties:**
- Maximum 5 zones per device
- Each zone has a dedicated TLS connection
- Zone priority determines conflict resolution
- Zones are independent -- adding or removing one zone does not affect others

---

## 2. Zone Types and Priority

Zone types define the purpose and priority of a controller. Higher priority (lower number) takes precedence in conflict resolution. Each device supports at most one zone per type.

| Zone Type | Priority | Typical Owner | Purpose |
|-----------|----------|---------------|---------|
| GRID | 1 (highest) | DSO, SMGW, grid operator | Regulatory/grid control |
| LOCAL | 2 | Residential/building EMS | Local energy management |
| TEST | 3 (lowest) | Test tools, observer | Testing, monitoring (DEC-060) |

```
┌──────────────────────────────────────────────────────────┐
│                      Device (EVSE)                       │
├──────────────────────────────────────────────────────────┤
│  Zone 1: GRID (priority 1)                               │
│    └── Operational Cert from SMGW                        │
│    └── Grid limits, regulatory constraints               │
│                                                          │
│  Zone 2: LOCAL (priority 2)                              │
│    └── Operational Cert from EMS                         │
│    └── Self-consumption optimization, local limits       │
│                                                          │
│  Max zones per type: 1 (DEC-043)                         │
│  Attempting same-type commission → ZONE_TYPE_EXISTS (10)  │
└──────────────────────────────────────────────────────────┘
```

Priority is **per-feature**, not global. The SMGW has priority for power limits but does not override the EMS's charging mode preference.

---

## 3. Zone Roles

Each zone has three roles that define what entities can do within the zone:

| Role | Capabilities | Typical Entity |
|------|-------------|----------------|
| **Zone Owner** | Holds Zone CA private key, issues certs, adds/removes members | EMS, SMGW |
| **Zone Admin** | Can commission devices on behalf of owner (forwards CSRs) | Phone App, installer tool |
| **Zone Member** | Has operational cert, communicates with other zone members | Devices (EVSE, inverter, etc.) |

**Key design principles:**

- **Apps are admins, NOT owners.** Losing a phone does not break zone operations. The EMS retains the Zone CA and can issue new admin tokens.
- **Apps always need an EMS.** There are no standalone app zones. An app commissions devices into an EMS-owned zone.
- **Multiple admins are supported.** Family members, installers, and service tools can all be zone admins concurrently.

---

## 4. Commissioning Flows

### 4.1 Direct Commissioning (EMS → Device)

The standard flow where an EMS directly commissions a device. See [Security: Commissioning](security.md#4-commissioning-pase-like).

### 4.2 Admin Authorization (Adding a Phone App)

```
User             Phone App           EMS Web UI
 │                   │                   │
 │── "Add admin" ────┼──────────────────►│
 │◄── Temp QR (5min) ┼───────────────────┤
 │── Scan QR ───────►│── SPAKE2+ ───────►│
 │◄── "Confirm?" ────┼───────────────────┤
 │── Yes ────────────┼──────────────────►│
 │                   │◄── Admin token ───┤
```

The EMS generates a temporary QR code (valid 5 minutes). The phone app scans it, completes SPAKE2+ with the EMS, and receives an admin token allowing it to commission devices.

### 4.3 App Commissioning Device (as Admin)

```
Phone App              EMS                 Device
    │                   │                    │
    │── SPAKE2+ ───────┼───────────────────►│  (app has setup code)
    │◄─────────────────┼───────── CSR ──────┤
    │── Forward CSR ──►│                    │
    │◄── Signed cert ──┤                    │
    │── Install cert ──┼───────────────────►│  (device in EMS zone)
```

The app performs SPAKE2+ with the device, receives the CSR, forwards it to the EMS for signing, and installs the signed certificate on the device.

### 4.4 Delegated Commissioning via SMGW

For grid operator zones, commissioning is delegated through a backend:

```
User           Phone App       DSO Backend       SMGW          Device
 │                 │               │               │              │
 │── Scan QR ─────►│── Upload ────►│── Forward ───►│              │
 │                 │               │               │── SPAKE2+ ──►│
 │                 │               │               │◄── Accept ───┤
```

1. User scans device QR code with app
2. App uploads setup info to DSO backend
3. DSO backend forwards to appropriate SMGW
4. SMGW commissions device directly
5. User is notified of success

See [Security: Delegated Commissioning](security.md#9-delegated-commissioning) for full details.

---

## 5. Priority Resolution

When multiple zones set conflicting values, the device resolves conflicts using two rules:

```
LIMITS:    Most restrictive wins (all zones constrain together)
SETPOINTS: Highest priority zone wins (only one controller active)
```

### 5.1 Limit Resolution

All zone limits are applied simultaneously. The most restrictive (lowest) value wins:

```
SMGW sets consumptionLimit = 6000W (priority 1)
EMS sets consumptionLimit = 8000W (priority 3)
→ Effective limit: min(6000, 8000) = 6000W
→ EMS can detect the override via subscription divergence (see below)
```

**Override detection (DEC-079).** MASH does not emit a dedicated override-notification
message. A controller detects that its written limit is not the active constraint by
subscribing to `effectiveConsumptionLimit` / `effectiveProductionLimit` and comparing
the effective value to the value it itself wrote (`myConsumptionLimit` /
`myProductionLimit`):

- `myLimit == effectiveLimit` → this controller's value is active
- `myLimit > effectiveLimit` → another zone imposed a stricter constraint; the
  effective value is that other zone's

Detection latency is bounded by the subscription's `maxInterval` (heartbeat). The
device does not expose *which* zone overrode — only the resolved effective value.
A controller that wants to know whether it has been overridden must subscribe to
the effective attributes; the protocol does not push this information otherwise.

Phase current limits are resolved per-phase:

```
Zone 1: SetCurrentLimits({A: 20000, B: 20000, C: 20000}, CONSUMPTION)
Zone 2: SetCurrentLimits({A: 16000, B: 10000, C: 16000}, CONSUMPTION)
→ Effective: {A: 16000, B: 10000, C: 16000} (per-phase minimum)
```

### 5.2 Setpoint Resolution

Only the highest-priority zone's setpoint is active:

```
SMGW (priority 1): SetSetpoint(consumptionSetpoint: 3000000)
EMS (priority 3):  SetSetpoint(consumptionSetpoint: 5000000)
→ Effective setpoint: 3000000 mW (SMGW wins)
```

### 5.3 Combined Resolution

Limits always constrain setpoints:

```
effectiveConsumptionLimit = 5000W (from limit resolution)
effectiveConsumptionSetpoint = 7000W (from setpoint resolution)
→ Device targets: min(7000, 5000) = 5000W (limit caps setpoint)
```

### 5.4 V2H Phase Balancing Example

A worked example showing multi-zone coordination for V2H phase balancing:

```
Scenario: House consumption L1=20A, L2=5A, L3=12A at 230V
          Grid limit 25A per phase
          EMS wants EV to discharge asymmetrically to balance

1. Grid operator sets limit:
   SetCurrentLimits({A: 25000, B: 25000, C: 25000}, PRODUCTION)

2. Home EMS sets asymmetric discharge setpoint:
   SetCurrentSetpoints({A: 10000, B: 2000, C: 5000}, PRODUCTION)
   cause: PHASE_BALANCING

3. V2H EV receives:
   effectiveCurrentLimitsProduction = {A: 25000, B: 25000, C: 25000}
   effectiveCurrentSetpointsProduction = {A: 10000, B: 2000, C: 5000}

4. EV discharges: 10A on L1, 2A on L2, 5A on L3
   Result: Net house import = L1: 10A, L2: 3A, L3: 7A (balanced)
```

For detailed resolution algorithms, see [EnergyControl: Multi-Zone Resolution](features/energy-control.md#multi-zone-resolution).

---

## 6. Connection Model

Each zone gets one persistent TCP/TLS connection to the device. The total connection capacity is bounded:

| Connection Type | Maximum | Description |
|-----------------|---------|-------------|
| Operational | max_zones (default: 5) | One persistent connection per paired zone |
| Commissioning | 1 | Single concurrent commissioning attempt |
| **Total** | max_zones + 1 | Maximum simultaneous connections |

**Rules:**
- When all zone slots are filled, the device does not advertise as commissionable
- Operational connections from existing zones are never blocked by commissioning
- User physical override (button on device) is always possible regardless of zone state

See [Transport](transport.md) for TLS configuration and connection management details.

---

## 7. Failsafe Behavior

When a zone's connection is lost, the device transitions to `FAILSAFE` state for that zone:

1. Device detects connection loss (TCP/TLS layer)
2. Device applies `failsafeConsumptionLimit` and `failsafeProductionLimit`
3. Device reports `controlState = FAILSAFE`
4. After `failsafeDuration` expires (2-24 hours), device transitions to `AUTONOMOUS`
5. Device can resume normal operation without controller

**Key design points:**
- Failsafe limits are pre-configured per device (set during commissioning or by the device)
- `failsafeDuration` is shared between consumption and production (one value for both)
- Other zones' connections and limits are unaffected by one zone's connection loss

**Applicability (DEC-073):** Failsafe-limit attributes (`failsafeConsumptionLimit`, `failsafeProductionLimit`, `failsafeDuration`) are writable only on endpoints that expose EnergyControl with `acceptsLimits = true`. Read-only endpoint types — `SUB_METER`, `PV_STRING`, `GRID_CONNECTION`, and any endpoint whose EnergyControl sets `acceptsLimits = false` — do not carry failsafe-limit semantics. A controller that attempts to write a failsafe-limit attribute on such an endpoint MUST receive `wire.StatusUnsupported` (status code 10). This matches the broader pattern that capability-gated attributes are only writable when the gate flag is true; see DEC-075 for the analogous treatment of the process-lifecycle commands.

For detailed timing requirements, see [Failsafe Timing](testing/behavior/failsafe-timing.md).

---

## 8. Zone Lifecycle

### 8.1 Adding a Zone

1. Controller commissions device (via PASE or delegated commissioning)
2. Device receives operational certificate signed by zone's CA
3. Device allocates a zone slot with the zone's priority
4. Controller establishes operational TLS connection
5. Device reports `controlState = CONTROLLED`

### 8.2 Removing a Zone

1. Controller sends RemoveZone command
2. Device deletes operational certificate for that zone
3. Connection is closed
4. Zone slot is freed; if no zones remain, device automatically re-enters commissioning mode (DEC-059)
5. Device re-evaluates effective limits/setpoints from remaining zones

### 8.3 Zone Slot Exhaustion

When all zone slots are filled:
- Device stops advertising `_mash-comm._tcp` (commissioning service)
- Device rejects commissioning connections at TLS level
- Existing operational connections continue normally
- A zone must be removed before a new one can be added

For detailed state machine and edge cases, see [Zone Lifecycle](testing/behavior/zone-lifecycle.md).
