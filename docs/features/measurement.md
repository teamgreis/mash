# Measurement Feature

> Real-time telemetry - power, energy, voltage, current readings

**Feature ID:** 0x0004
**Direction:** OUT (device reports to controller)
**Status:** Draft
**Last Updated:** 2025-01-25

!!! info "Normative reference"
    The authoritative attribute and command tables for Measurement are
    [generated from YAML](../generated/features/measurement.md). This page is
    the design rationale and worked-example companion -- if anything here
    disagrees with the generated table, the generated table wins.

---

## Purpose

Provides real-time telemetry - power, energy, voltage, current readings. Subscribe for continuous updates. Supports both AC (grid-facing) and DC (internal) measurements. Answers the question: "What IS this device doing?"

---

## Attributes

```cbor
Measurement Feature Attributes:
{
  // ═══════════════════════════════════════════════════════════
  // AC POWER (signed: + consumption, - production)
  // Used by: INVERTER, GRID_CONNECTION, EV_CHARGER, etc.
  // ═══════════════════════════════════════════════════════════

  // Total AC power
  1: acActivePower,            // int64 mW (active/real power)
  2: acReactivePower,          // int64 mVAR (reactive power)
  3: acApparentPower,          // uint64 mVA (apparent power, always positive)

  // Per-phase AC power (optional)
  // map: {PhaseEnum → int64 mW}
  10: acActivePowerPerPhase,
  11: acReactivePowerPerPhase,
  12: acApparentPowerPerPhase,

  // ═══════════════════════════════════════════════════════════
  // AC CURRENT & VOLTAGE (per-phase)
  // ═══════════════════════════════════════════════════════════

  // map: {PhaseEnum → int64 mA} (signed: + consumption, - production)
  20: acCurrentPerPhase,

  // map: {PhaseEnum → uint32 mV} (phase-to-neutral voltage, RMS)
  21: acVoltagePerPhase,

  // map: {PhasePairEnum → uint32 mV} (phase-to-phase voltage, optional)
  22: acVoltagePhaseToPhasePair,

  // Grid frequency
  23: acFrequency,             // uint32 mHz (e.g., 50000 = 50.000 Hz)

  // Power factor (cos φ)
  24: powerFactor,             // int16 (0.001 units, -1000 to +1000 = -1.0 to +1.0)

  // ═══════════════════════════════════════════════════════════
  // AC ENERGY (cumulative since install/reset)
  // ═══════════════════════════════════════════════════════════

  30: acEnergyConsumed,        // uint64 mWh (total consumed from grid)
  31: acEnergyProduced,        // uint64 mWh (total produced/fed-in to grid)

  // ═══════════════════════════════════════════════════════════
  // DC MEASUREMENTS (for PV strings, batteries)
  // Used by: PV_STRING, BATTERY endpoints
  // ═══════════════════════════════════════════════════════════

  40: dcPower,                 // int64 mW (+ into device, - out of device)
  41: dcCurrent,               // int64 mA (+ into device, - out of device)
  42: dcVoltage,               // uint32 mV

  // DC energy (cumulative)
  43: dcEnergyIn,              // uint64 mWh (energy into this component)
  44: dcEnergyOut,             // uint64 mWh (energy out of this component)

  // ═══════════════════════════════════════════════════════════
  // BATTERY STATE (for BATTERY endpoints)
  // Requires: BATTERY feature flag
  // ═══════════════════════════════════════════════════════════

  50: stateOfCharge,           // uint8 % (0-100)
  51: stateOfHealth,           // uint8 % (0-100, battery degradation)
  52: stateOfEnergy,           // uint64 mWh (available energy at current SoC)
  53: useableCapacity,         // uint64 mWh (current useable capacity)
  54: cycleCount,              // uint32 (charge/discharge cycles)

  // ═══════════════════════════════════════════════════════════
  // TEMPERATURE
  // ═══════════════════════════════════════════════════════════

  60: temperature,             // int16 centi-°C (e.g., 2500 = 25.00°C)
}
```

---

## Enumerations

### PhasePairEnum (for phase-to-phase voltage)

```
AB                = 0x00  // Voltage between phase A and B
BC                = 0x01  // Voltage between phase B and C
CA                = 0x02  // Voltage between phase C and A
```

---

## Sign Convention

**Passive sign convention (load convention):**
- **Positive (+)** = power/current flowing INTO the component (consumption, charging)
- **Negative (-)** = power/current flowing OUT of the component (production, discharging)

| Component | Positive (+) | Negative (-) |
|-----------|--------------|--------------|
| Inverter AC | Consuming from grid | Feeding to grid |
| PV String | N/A (always produces) | Producing |
| Battery DC | Charging | Discharging |
| EV Charger | Charging EV | V2G discharge |

---

## Examples by Endpoint Type

### Inverter AC endpoint (feeding 5kW to grid)

```cbor
{
  acActivePower: -5000000,                  // -5 kW (producing)
  acReactivePower: 200000,                  // 200 VAR
  acApparentPower: 5004000,                 // 5.004 kVA
  acCurrentPerPhase: {A: -7200, B: -7300, C: -7100},  // ~7A per phase out
  acVoltagePerPhase: {A: 230500, B: 231000, C: 229800},
  acFrequency: 50020,                       // 50.02 Hz
  powerFactor: -998,                        // -0.998 (producing)
  acEnergyConsumed: 125000000,              // 125 kWh lifetime from grid
  acEnergyProduced: 8450000000              // 8450 kWh lifetime to grid
}
```

### PV String endpoint (producing 3.2kW)

```cbor
{
  dcPower: -3200000,                        // -3.2 kW (producing)
  dcCurrent: -8000,                         // -8 A out
  dcVoltage: 400000,                        // 400 V
  dcEnergyOut: 4200000000                   // 4200 kWh total yield
}
```

### Battery endpoint (charging at 2kW)

```cbor
{
  dcPower: 2000000,                         // +2 kW (charging)
  dcCurrent: 4000,                          // +4 A in
  dcVoltage: 500000,                        // 500 V
  dcEnergyIn: 850000000,                    // 850 kWh total charged
  dcEnergyOut: 780000000,                   // 780 kWh total discharged
  stateOfCharge: 65,                        // 65%
  stateOfHealth: 97,                        // 97%
  stateOfEnergy: 6500000,                   // 6.5 kWh available
  useableCapacity: 10000000,                // 10 kWh useable
  cycleCount: 342,                          // 342 cycles
  temperature: 2850                         // 28.5°C
}
```

### 3-Phase EVSE (charging EV at 11kW)

```cbor
{
  acActivePower: 11040000,                  // +11.04 kW (consuming)
  acCurrentPerPhase: {A: 16000, B: 16000, C: 16000},
  acVoltagePerPhase: {A: 230000, B: 231000, C: 229000},
  acFrequency: 50000,                       // 50.00 Hz
  acEnergyConsumed: 2500000000              // 2500 kWh lifetime
}
```

### Grid Connection Point / Smart Meter (house importing 3kW)

```cbor
{
  acActivePower: 3000000,                   // +3 kW import
  acReactivePower: 150000,                  // 150 VAR
  acCurrentPerPhase: {A: 5200, B: 4100, C: 3800},
  acVoltagePerPhase: {A: 230200, B: 230800, C: 229500},
  acFrequency: 49980,                       // 49.98 Hz
  acEnergyConsumed: 45000000000,            // 45000 kWh imported
  acEnergyProduced: 12000000000             // 12000 kWh exported
}
```

---

## EEBUS Use Case Coverage

| EEBUS Use Case | Measurement Attributes Used |
|----------------|----------------------------|
| **MPC** (Power Consumption) | acActivePower, acActivePowerPerPhase, acCurrentPerPhase, acVoltagePerPhase, acFrequency, acEnergyConsumed/Produced |
| **MGCP** (Grid Connection) | Same as MPC (on GRID_CONNECTION endpoint) |
| **EVCEM** (EV Measurement) | acActivePower, acCurrentPerPhase, acEnergyConsumed |
| **MOI** (Inverter) | All AC power types, powerFactor, acEnergy, temperature |
| **MOB** (Battery) | dcPower, dcCurrent, dcVoltage, dcEnergy, stateOfCharge/Health/Energy, cycleCount, temperature |
| **MPS** (PV String) | dcPower, dcCurrent, dcVoltage, dcEnergyOut |

---

## Related Features

| Feature | Relationship |
|---------|--------------|
| [Electrical](electrical.md) | Provides rated values; Measurement reports actual values |
| [EnergyControl](energy-control.md) | Sets limits that affect measured values |
| [Status](status.md) | Reports operating state that explains measurement values |
