# Status Feature

> Per-endpoint operating state and health

**Feature ID:** 0x0002
**Direction:** OUT (device reports to controller)
**Status:** Draft
**Last Updated:** 2025-01-25

!!! info "Normative reference"
    The authoritative attribute and command tables for Status are
    [generated from YAML](../generated/features/status.md). This page is the
    design rationale and worked-example companion -- if anything here disagrees
    with the generated table, the generated table wins.

---

## Purpose

Per-endpoint operating state and health. Any endpoint can have this feature, including measurement-only endpoints that aren't controllable. Answers the question: "Is this device working?"

---

## Attributes

```cbor
Status Feature Attributes:
{
  1: operatingState,         // OperatingStateEnum
  2: stateDetail,            // uint32 (vendor-specific detail code, optional)
  3: faultCode,              // uint32 (fault/error code when state=FAULT, optional)
  4: faultMessage,           // string (human-readable fault description, optional)
}
```

---

## Enumerations

### OperatingStateEnum

```
UNKNOWN           = 0x00  // State not known
OFFLINE           = 0x01  // Not connected / not available
STANDBY           = 0x02  // Ready but not active
STARTING          = 0x03  // Powering up / initializing
RUNNING           = 0x04  // Actively operating
PAUSED            = 0x05  // Temporarily paused (can resume)
SHUTTING_DOWN     = 0x06  // Powering down
FAULT             = 0x07  // Error condition (check faultCode)
MAINTENANCE       = 0x08  // Under maintenance / firmware update
```

---

## Examples by Endpoint Type

### Inverter AC endpoint (normal operation)

```cbor
{ operatingState: RUNNING }
```

### PV String endpoint (fault condition)

```cbor
{
  operatingState: FAULT,
  faultCode: 1001,
  faultMessage: "String overcurrent protection"
}
```

### Battery endpoint (standby)

```cbor
{
  operatingState: STANDBY,
  stateDetail: 0x0001           // Vendor: waiting for charge command
}
```

### EVSE endpoint (no vehicle / actively charging)

```cbor
{ operatingState: STANDBY }     // No vehicle connected
{ operatingState: RUNNING }     // Actively charging
```

### Device root (firmware updating)

```cbor
{
  operatingState: MAINTENANCE,
  stateDetail: 0x0010           // Firmware update in progress
}
```

---

## State Transitions

```
              ┌─────────┐
    power ───►│ OFFLINE │◄─── power off / disconnect
              └────┬────┘
                   │ connect
                   ▼
              ┌──────────┐
              │ STARTING │
              └────┬─────┘
                   │ ready
                   ▼
              ┌─────────┐    start     ┌─────────┐
              │ STANDBY │─────────────►│ RUNNING │
              └────┬────┘◄─────────────└────┬────┘
                   │         stop           │
                   │                        │ pause
                   │                        ▼
                   │                   ┌────────┐
                   │                   │ PAUSED │
                   │                   └────┬───┘
                   │                        │ resume
                   │                        ▼
                   │              (back to RUNNING)
                   │
                   │ shutdown
                   ▼
              ┌──────────────┐
              │ SHUTTING_DOWN│───► OFFLINE
              └──────────────┘

  Any state ──► FAULT (on error)
  Any state ──► MAINTENANCE (for updates)
  FAULT ──► STANDBY (after recovery)
  MAINTENANCE ──► STANDBY (after completion)
```

---

## Usage Notes

- **Per-endpoint**: Each endpoint has its own Status feature with independent state
- **Subscribe for updates**: Controllers should subscribe to status changes
- **Fault handling**: When `operatingState = FAULT`, check `faultCode` and `faultMessage`
- **Vendor extensions**: Use `stateDetail` for vendor-specific sub-states
- **Read-only**: Status is for observation; use EnergyControl for commands

---

## Relationship to Other Features

| Feature | Responsibility |
|---------|---------------|
| **Status** | Operating state, faults, health (read-only) |
| **EnergyControl** | Control commands, limits, setpoints (write) |
| **Measurement** | Electrical readings, energy values (read-only) |

**Example: EVSE with all three features:**
```
Status:        operatingState = RUNNING
EnergyControl: effectiveConsumptionLimit = 11000000 mW
Measurement:   acActivePower = 10500000 mW
```

---

## Related Features

| Feature | Relationship |
|---------|--------------|
| [EnergyControl](energy-control.md) | controlState is about external control; operatingState is about device health |
| [Measurement](measurement.md) | Telemetry values that explain current operating state |
