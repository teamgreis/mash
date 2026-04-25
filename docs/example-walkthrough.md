# End-to-End Walkthrough

> One realistic flow, traced from physical pairing through to a multi-zone
> override. The other documents specify each piece in isolation; this page
> shows them working together.

**Status:** Informative
**Last Updated:** 2026-04-25

---

## Scenario

A new EVSE (EV charger) ships from the factory. The home owner installs it and
their home energy management system (EMS) commissions it. Later, the local
distribution-system operator (DSO) attaches a GRID zone to the same EVSE so it
can be curtailed during peak demand. We trace the wire-level interactions
through six steps:

1. Discovery and pairing (commissioning)
2. Reading the static `attributeList`
3. Setting a charging limit (Invoke)
4. Subscribing to the effective limit
5. A second controller (GRID zone) attaches and overrides the LOCAL limit
6. The GRID zone goes offline, triggering failsafe

The reader is assumed to have read [Protocol Overview](protocol-overview.md)
and [Interaction Model](interaction-model.md) at least once. We refer to wire
fields by their CBOR integer keys -- e.g. "key 2" means the operation field of
a Request.

CBOR examples below are written as JSON-shaped pseudocode for readability; the
real wire bytes are CBOR with integer keys. See
[message-types.md](message-types.md) for the full envelope definition.

---

## Step 1 -- Discovery and pairing

The EVSE has just been powered on for the first time. It enters
**commissioning mode** and starts advertising via mDNS:

```
_mash-comm._tcp.local
  └── EVSE-ABC123._mash-comm._tcp.local.
        TXT: D=4F2A, V=1, ...
```

The TXT field `D` is the **discriminator** (see
[discovery.md §2](discovery.md) and
[DEC-078](decision-log.md#dec-078-commissionable-mdns-txt-schema-canonical));
it lets the EMS pre-filter candidate devices before initiating SPAKE2+. The
home owner scans the QR code on the EVSE, which encodes the discriminator and
the **setup code** (passcode for SPAKE2+).

The EMS opens a TCP connection to the device's advertised port and negotiates
TLS 1.3 with ALPN `mash-comm/1`. Because we are on the commissioning ALPN, the
device routes every framed message to its commissioning state machine
(see [DEC-067](decision-log.md#dec-067-single-port-alpn-based-commissioning-routing-and-stable-certificate)).

The two sides then run SPAKE2+ (PASE):

```
EMS                                    EVSE
 │  PBKDFParameterRequest (MsgType=1) │
 │ ────────────────────────────────►  │
 │   PBKDFParameterResponse (=2)      │
 │ ◄────────────────────────────────  │
 │   Pake1 (=3)  ── SPAKE2+ X         │
 │ ────────────────────────────────►  │
 │   Pake2 (=4)  ── SPAKE2+ Y, Cb     │
 │ ◄────────────────────────────────  │
 │   Pake3 (=5)  ── Ca                │
 │ ────────────────────────────────►  │
 │   StatusReport (=6)                │
 │ ◄────────────────────────────────  │
```

The MsgType numbers come from the **commissioning namespace** (see
[message-types.md §1](message-types.md#1-three-planes-three-classification-mechanisms)).
They cannot collide with operational MsgTypes because the ALPN is different.

After SPAKE2+ succeeds, the EMS issues a CSR challenge and the device responds
with a CSR. The EMS signs it with the **LOCAL Zone CA** and sends the
operational certificate back. The device stores it and closes the
commissioning connection
([DEC-066](decision-log.md#dec-066-close-commissioning-connection-after-certificate-exchange)).

The EVSE now has one zone: a LOCAL zone owned by the EMS.

---

## Step 2 -- Read attributeList

The EMS opens a fresh TCP connection to the EVSE on the same port. This time
TLS negotiates ALPN `mash/1` and both sides present operational certificates
signed by the LOCAL Zone CA -- mutual authentication succeeds.

The EMS does not yet know which attributes the device implements. It reads
the [`attributeList`](decision-log.md#dec-051-static-attributelist-immutability)
global attribute on the device's EnergyControl feature (`attributeList` is CBOR
key `0xFFFB`, but the wire encoding represents it as a normal attribute id):

```json
// EMS → EVSE   (Request)
{
  1: 12345,        // messageId
  2: 1,            // operation: Read
  3: 1,            // endpointId: 1 (the EV_CHARGER endpoint)
  4: 5,            // featureId: 5 (EnergyControl)
  5: [0xFFFB]      // read just attributeList
}
```

```json
// EVSE → EMS   (Response)
{
  1: 12345,        // messageId echoed
  2: 0,            // status: success
  3: {
    0xFFFB: [1, 2, 3, 10, 11, 12, 13, 14, 16, 20, 21, 22, 70, 71, 72, 80, 81]
  }
}
```

The returned list tells the EMS which optional attributes (e.g.
`failsafeConsumptionLimit` at id 70) are implemented on this particular device.
The EMS would `Read 0xFFFC` next to fetch the `featureMap` bitmap and confirm
which capability variants (CORE, EMOB, V2X, …) are present
([DEC-074](decision-log.md#dec-074-featuremap-bit-semantics)).

---

## Step 3 -- Invoke setLimit

The home owner has set a household-wide cap of 7.0 kW. The EMS wants the EVSE
to draw at most 5.0 kW so the rest of the house has headroom. It invokes the
EnergyControl `setLimit` command with `limitDirection=consumption` and
`value=5_000_000` mW (5.0 kW, passive sign convention):

```json
// EMS → EVSE   (Request, operation=4 Invoke)
{
  1: 12346,                 // messageId
  2: 4,                     // operation: Invoke
  3: 1,                     // endpointId
  4: 5,                     // featureId: EnergyControl
  5: {
    1: 1,                   // commandId: setLimit (id 1 in YAML)
    2: {                    // command parameters
      1: 0,                 // limitDirection: 0 = consumption
      2: 5000000,           // value (mW)
      3: 0                  // cause: 0 = none / unspecified
    }
  }
}
```

The device verifies the command (acceptsLimits is true, the value is in
range), accepts it, and answers:

```json
// EVSE → EMS   (Response)
{
  1: 12346,
  2: 0,                     // success
  3: {
    1: true,                // applied
    2: 5000000              // effective limit after resolution
  }
}
```

Internally the device updates two attributes:

- `controlState` (id 12) transitions from `CONTROLLED` → `LIMITED`
- `effectiveConsumptionLimit` (id 20) becomes `5000000`

Anyone subscribed to either of those attributes will receive a delta
notification (Step 4).

---

## Step 4 -- Subscribe to effectiveConsumptionLimit

The EMS wants to react in real time when the device's effective limit changes
(for example, if a higher-priority zone attaches and tightens it -- see
Step 5). It subscribes:

```json
// EMS → EVSE   (Request)
{
  1: 12347,
  2: 3,                     // operation: Subscribe
  3: 1,
  4: 5,
  5: {
    1: [20, 12],            // attributeIds: effectiveConsumptionLimit, controlState
    2: 0,                   // minIntervalSec: 0 = report on every change
    3: 60                   // maxIntervalSec: heartbeat at 60 s
  }
}
```

```json
// EVSE → EMS   (Response: "priming" -- current values)
{
  1: 12347,
  2: 0,
  3: {
    1: 7777,                // subscriptionId issued by the device
    2: {
      20: 5000000,
      12: 3                 // controlState = LIMITED
    }
  }
}
```

From this point on, the device sends a Notification (messageId=0) on this
connection whenever any subscribed attribute changes. The mechanics:

- A change to one attribute may trigger **one** Notification carrying every
  attribute changed by the same event (so an EMS-side handler that updates
  both `effectiveConsumptionLimit` and `controlState` together emits one
  Notification with two entries -- see
  [DEC-071](decision-log.md#dec-071-subscription-notification-ordering-across-connections)).
- A 60-second heartbeat re-confirms the subscription is alive even when no
  attribute has changed.

---

## Step 5 -- A GRID zone attaches and overrides

Months later the local DSO commissions the same EVSE into its **GRID zone**.
Discovery, SPAKE2+, and certificate issuance all repeat (Step 1), but the
operational certificate is now signed by the DSO's GRID Zone CA. The device
ends up with two operational zones:

| Zone | Type | Priority | Owner |
|---|---|---|---|
| Zone A | LOCAL | 2 | home EMS |
| Zone B | GRID | 1 | DSO SMGW |

This is the maximum a typical home device runs with -- one per type
([DEC-043](decision-log.md#dec-043-one-zone-per-zone-type-constraint)).

The DSO immediately invokes `setLimit` with a 3.0 kW consumption limit:

```json
// DSO → EVSE   (over the GRID zone TLS connection)
{
  1: 4001,
  2: 4,                     // Invoke
  3: 1,
  4: 5,
  5: {
    1: 1,                   // setLimit
    2: { 1: 0, 2: 3000000, 3: 1 }   // cause: 1 = grid_event
  }
}
```

The device now has two competing consumption limits:
LOCAL = 5.0 kW, GRID = 3.0 kW. Per
[multi-zone.md](multi-zone.md) and
[DEC-024](decision-log.md#dec-024-limit-resolution-stacked-limits-most-restrictive-wins),
**limits resolve to the most restrictive value across all zones**. The
effective limit becomes 3.0 kW.

The EMS's subscription from Step 4 fires on its TLS connection (Zone A):

```json
// EVSE → EMS   (Notification)
{
  1: 0,                     // messageId 0 = Notification
  2: 7777,                  // subscriptionId from Step 4
  3: 1,
  4: 5,
  5: {
    20: 3000000             // effectiveConsumptionLimit dropped to 3.0 kW
  }
}
```

Note `controlState` (id 12) was already `LIMITED` in Step 3 and remains
`LIMITED`, so it does not appear in the delta -- absent means unchanged
([interaction-model.md §3.3](interaction-model.md#33-notification-message-from-subscriptions)).

The EMS now knows it has 3.0 kW available and may inform the user UI. It does
not need to know who imposed the tighter limit -- the resolution model hides
that detail. (If it does want to know, it can read the per-zone limit
attributes on EnergyControl directly.)

---

## Step 6 -- GRID zone goes offline; failsafe

The DSO's SMGW loses internet connectivity. The TLS keepalive on Zone B times
out. The device detects the dead connection and the EnergyControl state
machine on that zone transitions to **FAILSAFE**
([DEC-045](decision-log.md#dec-045-transport-layer-heartbeat-sufficient-for-failsafe)).
While in FAILSAFE the device enforces the **`failsafeConsumptionLimit`**
attribute (id 70), which the DSO had written to 4.0 kW at commissioning time.

Limit resolution still applies across zones:

- Zone A (LOCAL): 5.0 kW
- Zone B (GRID, in FAILSAFE): 4.0 kW

The most restrictive is 4.0 kW, which becomes the new
`effectiveConsumptionLimit`. The EMS's subscription receives:

```json
// EVSE → EMS   (Notification)
{
  1: 0,
  2: 7777,
  3: 1,
  4: 5,
  5: {
    20: 4000000
  }
}
```

The device stays in FAILSAFE on Zone B for at most `failsafeDuration` seconds
(7200..86400, default 7200, see
[DEC-073](decision-log.md#dec-073-failsafe-limit-attributes-gated-on-acceptslimits)).
If Zone B does not reconnect within that window, the device transitions to
`AUTONOMOUS` on Zone B and the GRID zone's contribution to limit resolution
disappears. The EMS would then see another notification raising
`effectiveConsumptionLimit` back to 5.0 kW (its own LOCAL cap).

If Zone B reconnects before the timer fires, the operational state resumes:
the device transitions out of FAILSAFE and the live GRID limit (3.0 kW from
Step 5) re-applies. The EMS's subscription would deliver the resulting
3.0-kW notification.

---

## What just happened

In six steps a developer can see:

- All four interaction operations on the wire (Read in Step 2, Subscribe in
  Step 4, Invoke in Step 3, plus an implicit Write of `failsafeConsumptionLimit`
  during commissioning that was not shown).
- The two ALPN routes (`mash-comm/1` for Step 1, `mash/1` for Steps 2-6).
- Cross-zone limit resolution (Steps 5 and 6).
- Subscription priming, deltas, and the "absent = unchanged" rule.
- Failsafe transition and the device-local enforcement when a controller is
  lost.

Once these patterns are in muscle memory, the rest of the spec is mostly
filling in capability tables. Continue reading from
[Interaction Model](interaction-model.md) for the canonical reference, or
[Multi-Zone](multi-zone.md) for the deep dive into resolution semantics.
