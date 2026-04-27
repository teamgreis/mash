# Electrical Feature

> Electrical characteristics and current capability envelope of an endpoint

**Feature ID:** 0x03
**Status:** Draft
**Last Updated:** 2025-01-25

!!! info "Normative reference"
    The authoritative attribute and command tables for Electrical are
    [generated from YAML](../generated/features/electrical.md). This page is the
    design rationale and worked-example companion -- if anything here disagrees
    with the generated table, the generated table wins.

---

## Purpose

Describes the electrical characteristics and **current capability envelope** of an endpoint. Answers the question: "What CAN this endpoint do right now?"

**Dynamic behavior:** For endpoints with connected devices (e.g., EVSE with EV), these values reflect the **effective system capability** - the intersection of the endpoint's hardware limits and the connected device's constraints. Values update when devices connect/disconnect.

---

## Attributes

```cbor
Electrical Feature Attributes:
{
  // Phase configuration
  1: phaseCount,              // uint8: 1, 2, or 3
  2: phaseMapping,            // map: {DevicePhase → GridPhase}

  // Voltage/frequency
  3: nominalVoltage,          // uint16 V (e.g., 230, 400)
  4: nominalFrequency,        // uint8 Hz (e.g., 50, 60)

  // Direction capability
  5: supportedDirections,     // DirectionEnum

  // Nameplate power ratings (hardware limits)
  10: nominalMaxConsumption,  // int64 mW (max charge/consumption power)
  11: nominalMaxProduction,   // int64 mW (max discharge/production power, 0 if N/A)
  12: nominalMinPower,        // int64 mW (minimum operating point)

  // Nameplate current ratings (per-phase)
  13: maxCurrentPerPhase,     // int64 mA (e.g., 32000 = 32A)
  14: minCurrentPerPhase,     // int64 mA (e.g., 6000 = 6A)

  // Per-phase capabilities
  15: supportsAsymmetric,     // AsymmetricSupportEnum

  // For storage devices
  20: energyCapacity          // int64 mWh (battery size, 0 if N/A)
}
```

### Attribute Summary

| ID | Name | Type | Description |
|----|------|------|-------------|
| 1 | phaseCount | uint8 | Number of phases (1, 2, or 3) |
| 2 | phaseMapping | map | Device phase to grid phase mapping |
| 3 | nominalVoltage | uint16 | Nominal voltage in V |
| 4 | nominalFrequency | uint8 | Nominal frequency in Hz |
| 5 | supportedDirections | DirectionEnum | Consumption/production capability |
| 10 | nominalMaxConsumption | int64 | Max consumption power in mW |
| 11 | nominalMaxProduction | int64 | Max production power in mW |
| 12 | nominalMinPower | int64 | Min operating point in mW |
| 13 | maxCurrentPerPhase | int64 | Max current per phase in mA |
| 14 | minCurrentPerPhase | int64 | Min current per phase in mA |
| 15 | supportsAsymmetric | AsymmetricSupportEnum | Per-phase asymmetric support |
| 20 | energyCapacity | int64 | Storage capacity in mWh |

---

## Enumerations

### DirectionEnum

```
CONSUMPTION       = 0x00  // Can only consume power
PRODUCTION        = 0x01  // Can only produce power
BIDIRECTIONAL     = 0x02  // Can consume and produce
```

### AsymmetricSupportEnum

```
NONE              = 0x00  // Symmetric only (all phases must have same value)
CONSUMPTION       = 0x01  // Asymmetric consumption (different values per phase when charging)
PRODUCTION        = 0x02  // Asymmetric production (different values per phase when discharging)
BIDIRECTIONAL     = 0x03  // Asymmetric both directions
```

### PhaseEnum (Device Phase)

```
A                 = 0x00
B                 = 0x01
C                 = 0x02
```

### GridPhaseEnum

```
L1                = 0x00
L2                = 0x01
L3                = 0x02
```

---

## Phase Mapping Examples

### 3-Phase EVSE (standard rotation)

```cbor
{
  phaseCount: 3,
  phaseMapping: {A: L1, B: L2, C: L3},
  maxCurrentPerPhase: 32000,
  supportsAsymmetric: CONSUMPTION    // Can charge asymmetrically
}
```

### 3-Phase V2H (bidirectional, asymmetric both ways)

```cbor
{
  phaseCount: 3,
  phaseMapping: {A: L1, B: L2, C: L3},
  maxCurrentPerPhase: 32000,
  supportsAsymmetric: BIDIRECTIONAL  // Can charge AND discharge asymmetrically
}
```

### Battery inverter (balanced phases)

```cbor
{
  phaseCount: 3,
  phaseMapping: {A: L1, B: L2, C: L3},
  maxCurrentPerPhase: 25000,
  supportsAsymmetric: NONE           // Always symmetric, inverter balances phases
}
```

### 1-Phase EVSE (connected to L3)

```cbor
{
  phaseCount: 1,
  phaseMapping: {A: L3},
  maxCurrentPerPhase: 32000,
  supportsAsymmetric: NONE           // Single phase, N/A
}
```

### 3-Phase with rotated installation

```cbor
{
  phaseCount: 3,
  phaseMapping: {A: L2, B: L3, C: L1},
  // Device's Phase A is connected to grid's L2
}
```

---

## Dynamic Capability Updates

For endpoints with connected devices (EVSE, some inverters), Electrical values reflect the **current effective capability**:

```
Effective capability = intersection(endpoint hardware, connected device)
```

**Example: EVSE with EV connection**

```cbor
// EVSE hardware: 22kW, 32A, min 0W
// Before EV connects:
{
  nominalMaxConsumption: 22000000,  // 22 kW
  nominalMinPower: 0,
  maxCurrentPerPhase: 32000,        // 32A
  minCurrentPerPhase: 0
}

// EV connects with 7.4kW max, 1.4kW min (6A-16A):
{
  nominalMaxConsumption: 7400000,   // 7.4 kW (limited by EV)
  nominalMinPower: 1400000,         // 1.4 kW (EV minimum)
  maxCurrentPerPhase: 16000,        // 16A (limited by EV)
  minCurrentPerPhase: 6000          // 6A (EV minimum)
}

// EV disconnects → values return to EVSE hardware limits
```

**CEM subscribes to Electrical** to track capability changes. When values change, CEM adjusts its limits/setpoints to stay within the new envelope.

---

## Usage Notes

- Phase mapping is critical for EMS to understand how device phases align with grid phases
- Installers should configure phase mapping during commissioning
- Values reflect current effective capability, including connected devices
- energyCapacity is only relevant for storage devices (BATTERY endpoints)
- CEM should subscribe to Electrical to track capability changes

---

## Related Features

| Feature | Relationship |
|---------|--------------|
| [Measurement](measurement.md) | Reports actual values vs Electrical's rated values |
| [EnergyControl](energy-control.md) | Uses Electrical limits as constraints |
