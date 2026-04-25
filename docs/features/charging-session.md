# ChargingSession Feature

> EV charging session state, energy demands, and vehicle identification

**Feature ID:** 0x0006
**Direction:** OUT (device reports to controller)
**FeatureMap Bit:** EMOB (0x0008)
**Status:** Draft
**Last Updated:** 2025-01-25

!!! info "Normative reference"
    The authoritative attribute and command tables for ChargingSession are
    [generated from YAML](../generated/features/charging-session.md). This page
    is the design rationale and worked-example companion -- if anything here
    disagrees with the generated table, the generated table wins.

---

## Purpose

EV charging session state, energy demands, battery state, and vehicle identification. This feature exists only on endpoints with `EndpointType = EV_CHARGER`.

---

## Protocol Context

| Protocol | Smart Comm | SoC Info | Energy Demands | V2G |
|----------|------------|----------|----------------|-----|
| IEC 61851-1 | No (PWM only) | No | No | No |
| ISO 15118-2 | Yes | Yes | Scheduled mode | No |
| ISO 15118-20 | Yes | Yes | Dynamic + Scheduled | Yes |

Fields are nullable when the EV cannot provide that information.

---

## Attributes

```cbor
ChargingSession Feature:
{
  // SESSION STATE
  1: state,                    // ChargingStateEnum
  2: sessionId,                // uint32: unique session identifier
  3: sessionStartTime,         // timestamp: when EV connected
  4: sessionEndTime,           // timestamp?: when session ended

  // SESSION ENERGY
  10: sessionEnergyCharged,    // uint64 mWh: energy delivered to EV
  11: sessionEnergyDischarged, // uint64 mWh: energy returned from EV (V2G)

  // EV IDENTIFICATIONS
  20: evIdentifications,       // EvIdentification[]: list of EV identifiers

  // EV BATTERY STATE (from ISO 15118)
  30: evStateOfCharge,         // uint8?: current SoC %
  31: evBatteryCapacity,       // uint64 mWh?: EV battery capacity
  32: evMinStateOfCharge,      // uint8?: floor SoC for must-charge %
  33: evTargetStateOfCharge,   // uint8?: user-defined target SoC %

  // EV ENERGY DEMANDS
  40: evDemandMode,            // EvDemandModeEnum
  41: evMinEnergyRequest,      // int64 mWh?: energy to min SoC
  42: evMaxEnergyRequest,      // int64 mWh?: energy to full
  43: evTargetEnergyRequest,   // int64 mWh?: energy to target SoC
  44: evDepartureTime,         // timestamp?: when EV needs to leave

  // V2G DISCHARGE CONSTRAINTS
  50: evMinDischargingRequest, // int64 mWh?: must be <0 for discharge
  51: evMaxDischargingRequest, // int64 mWh?: must be >=0 for discharge
  52: evDischargeBelowTargetPermitted, // bool?: allow V2G below target

  // ESTIMATED TIMES
  60: estimatedTimeToMinSoC,   // uint32 s?
  61: estimatedTimeToTargetSoC,// uint32 s?
  62: estimatedTimeToFullSoC,  // uint32 s?

  // CHARGING MODE (optimization strategy)
  70: chargingMode,            // ChargingModeEnum: active optimization strategy
  71: supportedChargingModes,  // ChargingModeEnum[]: modes EVSE supports
  72: surplusThreshold,        // int64 mW?: threshold for PV_SURPLUS_THRESHOLD mode

  // START/STOP DELAYS (CEM can override, EVSE enforces)
  80: startDelay,              // uint32 s: delay before (re)starting charge
  81: stopDelay,               // uint32 s: delay before pausing charge
}
```

---

## Enumerations

### ChargingStateEnum

```
NOT_PLUGGED_IN        = 0x00  // No EV connected
PLUGGED_IN_NO_DEMAND  = 0x01  // EV connected, not requesting charge
PLUGGED_IN_DEMAND     = 0x02  // EV requesting charge, waiting
PLUGGED_IN_CHARGING   = 0x03  // Actively charging
PLUGGED_IN_DISCHARGING= 0x04  // V2G: actively discharging
SESSION_COMPLETE      = 0x05  // Charging finished, still plugged
FAULT                 = 0x06  // Error state
```

### EvDemandModeEnum

```
NONE                  = 0x00  // IEC 61851: no demand info
SINGLE_DEMAND         = 0x01  // Basic: just energy amount
SCHEDULED             = 0x02  // ISO 15118: EV plans based on incentives
DYNAMIC               = 0x03  // ISO 15118-20: CEM controls directly
DYNAMIC_BIDIRECTIONAL = 0x04  // ISO 15118-20: dynamic with V2G
```

### EvIdTypeEnum

```
PCID                  = 0x00  // Provisioning Certificate ID
MAC_EUI48             = 0x01  // MAC address 6 bytes
MAC_EUI64             = 0x02  // Extended MAC 8 bytes
RFID                  = 0x03  // RFID tag identifier
VIN                   = 0x04  // Vehicle Identification Number
CONTRACT_ID           = 0x05  // eMI3 Contract ID (EMAID)
EVCC_ID               = 0x06  // EVCC ID from ISO 15118
OTHER                 = 0xFF  // Vendor-specific
```

### ChargingModeEnum

```
OFF                   = 0x00  // No optimization, charge at maximum rate
PV_SURPLUS_ONLY       = 0x01  // Only self-produced energy, no grid
PV_SURPLUS_THRESHOLD  = 0x02  // Allow grid if surplus >= surplusThreshold
PRICE_OPTIMIZED       = 0x03  // Optimize based on price signals
SCHEDULED             = 0x04  // Follow time-based schedule/plan
```

---

## Commands

### SetChargingMode

Controller sets the optimization strategy. EVSE validates and enforces.

```cbor
Request:
{
  1: mode,                    // ChargingModeEnum: desired mode
  2: surplusThreshold,        // int64 mW?: required for PV_SURPLUS_THRESHOLD
  3: startDelay,              // uint32 s?: override start delay
  4: stopDelay,               // uint32 s?: override stop delay
}

Response:
{
  1: success,                 // bool
  2: activeMode,              // ChargingModeEnum: confirmed mode
  3: reason,                  // string?: rejection reason if not success
}
```

---

## Responsibility Model

Charging involves two domains of knowledge:

| Domain | Owner | Responsibility |
|--------|-------|----------------|
| System optimization | CEM/EMS | Goals, prices, grid constraints, PV forecasts |
| EV behavior | EVSE | Protocol handling, timing, hardware limits |

**Pattern: "CEM suggests, EVSE decides within safe bounds"**

- **CEM sets** charging mode and can override delays
- **EVSE validates** requests against EV/hardware constraints
- **EVSE enforces** behavior using its domain knowledge
- **EVSE reports** active mode, constraints, and deviations

The EVSE may reject a mode or delay value that would harm the EV or violate hardware constraints.

---

## Discovering Supported Charging Modes

The `supportedChargingModes` attribute tells the CEM what optimization strategies the EVSE can implement:

```cbor
// EVSE reports its capabilities
supportedChargingModes: [OFF, PV_SURPLUS_ONLY, PV_SURPLUS_THRESHOLD, PRICE_OPTIMIZED]
```

**Discovery flow:**
1. CEM connects to EVSE
2. CEM reads `supportedChargingModes` attribute
3. CEM knows which modes it can request via `SetChargingMode`
4. CEM can show user only the available options

**Mode requirements:**
- `OFF` - always supported (baseline)
- `PV_SURPLUS_ONLY` - requires EVSE to track surplus signals
- `PV_SURPLUS_THRESHOLD` - requires `surplusThreshold` attribute
- `PRICE_OPTIMIZED` - requires Signals feature support
- `SCHEDULED` - requires Plan feature support

If CEM requests an unsupported mode, EVSE returns `success: false` with reason.

---

## Start/Stop Delay Semantics

Start/stop delays prevent EVs from stopping completely due to frequent charge interruptions (common with fluctuating PV):

| Delay | Purpose |
|-------|---------|
| startDelay | Wait time before (re)starting after limit raised above minimum |
| stopDelay | Wait time before pausing after limit dropped below minimum |

- EVSE provides sensible defaults based on connected EV
- CEM can override if system-wide optimization requires different values
- EVSE enforces the delays, protecting the EV

---

## Constraint Layers

Charging power is constrained by two layers:

```
┌─────────────────────────────────────────────────────────────┐
│ Layer 1: Current System Capability (Electrical feature)     │
│   nominalMaxConsumption, maxCurrentPerPhase, nominalMinPower│
│   → Dynamic: reflects EVSE hardware AND connected EV        │
│   → EVSE calculates intersection, reports effective envelope│
├─────────────────────────────────────────────────────────────┤
│ Layer 2: Active Limits (EnergyControl feature)              │
│   effectiveConsumptionLimit, myCurrentLimits                │
│   → CEM-set operational limits (must be within Layer 1)     │
└─────────────────────────────────────────────────────────────┘
```

**Electrical is dynamic:** When an EV connects, the EVSE updates Electrical to reflect the current system capability (intersection of EVSE hardware and EV constraints). CEM subscribes to Electrical and sees the updated envelope.

**Example:** EVSE hardware 22kW/32A, EV connects with 7.4kW/16A max, 1.4kW/6A min
- Electrical updates: `nominalMaxConsumption=7400000`, `maxCurrentPerPhase=16000`, `nominalMinPower=1400000`
- CEM sees new envelope, adjusts limits accordingly

**Important:** `nominalMinPower` is often not zero. Many EVs cannot charge below ~1.4kW (6A single-phase). Setting limits below this will cause the EV to stop charging entirely.

---

## Energy Request Semantics

Energy requests are **deltas from current SoC** (ISO 15118-20 convention):

- **Positive** = energy needs to be charged
- **Negative** = energy can be discharged
- **Zero** = SoC level reached

**Example - EV at 60% SoC, 80kWh battery:**
```
evStateOfCharge = 60
evBatteryCapacity = 80000000 mWh
evMinEnergyRequest = -16000000 mWh   // Can discharge to 40%
evTargetEnergyRequest = 16000000 mWh // Needs 16 kWh to reach 80%
evMaxEnergyRequest = 32000000 mWh    // Needs 32 kWh to reach 100%
```

---

## V2G Discharge Rules

Discharging is permitted when:

```
Can discharge = (evMinDischargingRequest < 0)
              AND (evMaxDischargingRequest >= 0)
              AND (evTargetEnergyRequest <= 0 OR evDischargeBelowTargetPermitted)
```

---

## Examples

### IEC 61851 Basic Charger

```cbor
{
  state: PLUGGED_IN_CHARGING,
  sessionId: 12345,
  sessionStartTime: 1706180400,
  sessionEnergyCharged: 5500000,    // 5.5 kWh
  sessionEnergyDischarged: 0,
  evDemandMode: NONE
  // No EV info available
}
```

### ISO 15118-2 Smart Charger

```cbor
{
  state: PLUGGED_IN_CHARGING,
  sessionId: 12346,
  sessionStartTime: 1706180400,
  sessionEnergyCharged: 12000000,     // 12 kWh
  sessionEnergyDischarged: 0,
  evIdentifications: [
    { type: RFID, value: "04E57CD2A1B3" },
    { type: MAC_EUI48, value: "AA:BB:CC:DD:EE:FF" }
  ],
  evStateOfCharge: 65,                // 65%
  evBatteryCapacity: 75000000,        // 75 kWh
  evDemandMode: SCHEDULED,
  evMinEnergyRequest: -18750000,      // -18.75 kWh (can discharge to 40%)
  evMaxEnergyRequest: 26250000,       // 26.25 kWh (to full)
  evTargetEnergyRequest: 11250000,    // 11.25 kWh (to 80% target)
  evDepartureTime: 1706223600         // tomorrow 8am
}
```

### ISO 15118-20 V2G Bidirectional

```cbor
{
  state: PLUGGED_IN_DISCHARGING,
  sessionId: 12347,
  sessionEnergyCharged: 8000000,
  sessionEnergyDischarged: 3500000,
  evIdentifications: [
    { type: PCID, value: "PCID-VW-2024-ABC123" },
    { type: VIN, value: "WVWZZZ3CZWE123456" }
  ],
  evStateOfCharge: 72,
  evBatteryCapacity: 82000000,
  evDemandMode: DYNAMIC_BIDIRECTIONAL,
  evMinEnergyRequest: -26240000,
  evMaxEnergyRequest: 22960000,
  evTargetEnergyRequest: -8200000,
  evMinDischargingRequest: -16400000,
  evMaxDischargingRequest: 8200000,
  evDischargeBelowTargetPermitted: true
}
```

### PV Surplus Charging (OSCEV 2.0)

```cbor
{
  state: PLUGGED_IN_CHARGING,
  sessionId: 12348,
  sessionStartTime: 1706180400,
  sessionEnergyCharged: 12500000,     // 12.5 kWh

  // EV demands
  evDemandMode: DYNAMIC,
  evStateOfCharge: 45,
  evTargetEnergyRequest: 28000000,    // wants 28 kWh more
  evDepartureTime: 1706220000,

  // Charging mode - user wants PV optimization with some grid allowed
  chargingMode: PV_SURPLUS_THRESHOLD,
  supportedChargingModes: [OFF, PV_SURPLUS_ONLY, PV_SURPLUS_THRESHOLD, PRICE_OPTIMIZED],
  surplusThreshold: 1400000,          // 1.4 kW minimum surplus required

  // EVSE-provided delays (EV needs gentle handling)
  startDelay: 60,                     // 60s before restarting
  stopDelay: 120                      // 120s before pausing
}
// Note: EV charging constraints (min/max power) are in the Electrical feature,
// which updates dynamically when EV connects
```

### Dual-Port EVSE (Two Endpoints)

A dual-port EVSE uses separate endpoints for each port, each with its own ChargingSession:

```
Endpoint 1 (Port 1): ChargingSession
  state: PLUGGED_IN_CHARGING
  sessionId: 1001
  evStateOfCharge: 45%

Endpoint 2 (Port 2): ChargingSession
  state: NOT_PLUGGED_IN
  sessionId: 0 (no active session)
```

Each port operates independently with its own session state, energy accounting, and EV demands. The Electrical feature on each endpoint reflects the dynamic power envelope for that specific port.

---

## EEBUS Use Case Coverage

| EEBUS Use Case | ChargingSession Mapping |
|----------------|------------------------|
| EVSOC | evStateOfCharge, evMinEnergyRequest, evBatteryCapacity |
| EVCC/EVSECC | evIdentifications, sessionId |
| EVCC charging limits | Electrical feature (dynamic - updates when EV connects) |
| CEVC | evMinEnergyRequest, evTargetEnergyRequest, evDepartureTime |
| DBEVC | evDemandMode, evMin/MaxDischargingRequest |
| OSCEV 2.0 | chargingMode, supportedChargingModes, surplusThreshold, startDelay, stopDelay |

---

## Related Features

| Feature | Relationship |
|---------|--------------|
| [Measurement](measurement.md) | Actual power/energy values |
| [EnergyControl](energy-control.md) | Control commands for the EVSE |
| [Signals](signals.md) | Incentives that inform charging decisions |
| [Plan](plan.md) | EVSE's intended charging response |
