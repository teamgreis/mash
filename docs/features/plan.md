# Plan Feature

> Device's intended power behavior over time

**Feature ID:** 0x0009
**Direction:** OUT (device reports to controller)
**FeatureMap Bit:** PLAN (0x0040)
**Status:** Draft
**Last Updated:** 2025-01-25

!!! info "Normative reference"
    The authoritative attribute and command tables for Plan are
    [generated from YAML](../generated/features/plan.md). This page is the
    design rationale and worked-example companion -- if anything here disagrees
    with the generated table, the generated table wins.

---

## Purpose

Device reports its intended power behavior over time. This is the **output** - the device's response to signals, constraints, and internal optimization. Complements [Signals](signals.md) (input).

---

## Design Principles

1. **Plan = Device's intention** - what it plans to consume/produce
2. **Response to signals** - reflects optimization based on prices/constraints
3. **Commitment levels** - preliminary vs committed plans
4. **Regular updates** - device updates plan as conditions change

---

## Attributes

```cbor
Plan Feature:
{
  // Plan identification
  1: planId,                 // uint32: unique identifier
  2: planVersion,            // uint32: version (increments on update)
  3: commitment,             // CommitmentEnum: how firm is this plan

  // Plan timing
  10: startTime,             // timestamp: when plan starts
  11: endTime,               // timestamp: when plan ends
  12: lastUpdated,           // timestamp: when plan was last modified

  // Plan slots
  20: slots,                 // PlanSlot[]: planned behavior (max 96)

  // Plan totals
  30: totalEnergyPlanned,    // int64 mWh: total planned energy
  31: estimatedCost,         // int32?: estimated cost (0.0001 currency)

  // Signal references
  40: basedOnSignals,        // uint32[]?: signal IDs this plan responds to
}

PlanSlot:
{
  1: duration,               // uint32 s: slot duration
  2: plannedPower,           // int64 mW: intended power (+ consume, - produce)
  3: minPower,               // int64 mW?: flexibility range min
  4: maxPower,               // int64 mW?: flexibility range max
  5: confidence,             // uint8?: confidence level (0-100%)
}
```

---

## Enumerations

### CommitmentEnum

```
PRELIMINARY       = 0x00  // May change significantly
TENTATIVE         = 0x01  // Likely but not guaranteed
COMMITTED         = 0x02  // Device intends to follow
EXECUTING         = 0x03  // Currently executing this plan
```

---

## Commands

### RequestPlan

Controller requests device to generate/update plan.

```cbor
Request:
{
  1: startTime,            // timestamp?: plan start (null = now)
  2: duration,             // uint32 s?: plan duration
  3: constraints,          // Constraint[]?: additional constraints
}

Response:
{
  1: success,              // bool
  2: planId,               // uint32: plan identifier
}
```

### AcceptPlan

Controller accepts a device's plan.

```cbor
Request:
{
  1: planId,               // uint32: which plan to accept
  2: planVersion,          // uint32: expected version
}

Response:
{
  1: success,              // bool
  2: newCommitment,        // CommitmentEnum: COMMITTED or EXECUTING
}
```

---

## Plan Events

### PlanUpdated

Device notifies controller when plan changes.

```cbor
Event:
{
  1: planId,               // uint32
  2: planVersion,          // uint32: new version
  3: reason,               // PlanChangeReasonEnum
}
```

### PlanChangeReasonEnum

```
NEW_SIGNAL            = 0x00  // New signal received
CONSTRAINT_CHANGE     = 0x01  // Constraints changed
EXECUTION_DEVIATION   = 0x02  // Actual differs from planned
USER_REQUEST          = 0x03  // User changed preferences
INTERNAL_OPTIMIZATION = 0x04  // Device re-optimized
```

---

## CEVC Flow Example

```
1. EMS sends price signals:
   Signals: [{ type: PRICE, slots: [...] }]

2. EVSE calculates optimal charging plan:
   Plan: {
     commitment: PRELIMINARY,
     slots: [
       { duration: 3600, plannedPower: 0 },        // Wait during peak
       { duration: 7200, plannedPower: 7400000 },  // Charge during off-peak
       { duration: 3600, plannedPower: 3700000 }   // Finish at low rate
     ]
   }

3. EMS reviews and accepts:
   AcceptPlan(planId: 1001, planVersion: 1)

4. EVSE commits and executes:
   Plan: { commitment: EXECUTING, ... }

5. EVSE notifies if deviating:
   Event: { reason: EXECUTION_DEVIATION }
```

---

## Example Plan (EV Charging)

```cbor
{
  planId: 1001,
  planVersion: 3,
  commitment: COMMITTED,
  startTime: 1706180400,
  endTime: 1706220000,
  lastUpdated: 1706180000,
  slots: [
    { duration: 7200,  plannedPower: 0,        confidence: 95 },  // 2h pause
    { duration: 10800, plannedPower: 7400000,  confidence: 90 },  // 3h @ 7.4kW
    { duration: 3600,  plannedPower: 11000000, confidence: 85 },  // 1h @ 11kW
    { duration: 14400, plannedPower: 3700000,  confidence: 80 }   // 4h @ 3.7kW
  ],
  totalEnergyPlanned: 41400000,  // 41.4 kWh
  estimatedCost: 82800,          // 8.28 EUR
  basedOnSignals: [1001, 2001]   // ToU price + grid constraint signals
}
```

---

## Related Features

| Feature | Relationship |
|---------|--------------|
| [Signals](signals.md) | Input that device optimizes against |
| [Tariff](tariff.md) | Price structure for cost calculation |
| [EnergyControl](energy-control.md) | Immediate control; Plan is scheduled intent |
| [ChargingSession](charging-session.md) | EV demands that inform the plan |
