# Failsafe Timing Specification

> Precise timing requirements for connection loss detection and failsafe behavior

**Status:** Draft
**Last Updated:** 2025-01-25

---

## Purpose

This document specifies the exact timing requirements for failsafe behavior, including connection loss detection, failsafe limit application, and transition to autonomous operation. These timing guarantees are critical for grid reliability.

---

## 1. Connection Loss Detection

### 1.1 Detection Mechanism

Connection loss is detected through the application-layer keep-alive mechanism:

| Parameter | Value |
|-----------|-------|
| Ping interval | 30 seconds (if no other activity) |
| Pong timeout | 5 seconds |
| Max missed pongs | 3 |

### 1.2 Maximum Detection Delay

**Maximum time to detect connection loss: 95 seconds**

Calculation:
```
Worst case scenario:
  T+0s:   Last successful communication
  T+30s:  Ping 1 sent (30s interval)
  T+35s:  Ping 1 times out (5s timeout)
  T+65s:  Ping 2 sent (30s after Ping 1)
  T+70s:  Ping 2 times out
  T+100s: Ping 3 sent (30s after Ping 2)
  T+105s: Ping 3 times out → Connection loss detected

  But actually: pings continue at 30s intervals even during timeout
  T+0s:   Last successful communication
  T+30s:  Ping 1 sent
  T+35s:  Ping 1 times out (missed pong = 1)
  T+60s:  Ping 2 sent
  T+65s:  Ping 2 times out (missed pong = 2)
  T+90s:  Ping 3 sent
  T+95s:  Ping 3 times out (missed pong = 3) → Connection loss detected
```

**Effective detection: 95 seconds maximum** (3 × 30s interval + 5s final timeout)

### 1.3 Typical Detection Delay

In typical scenarios, detection is faster:

| Scenario | Detection Time |
|----------|---------------|
| Clean disconnect (TCP RST) | Immediate (< 1s) |
| Network partition after ping | 5 seconds (single pong timeout) |
| Network partition mid-cycle | Up to 95 seconds |
| TLS error | Immediate |

### 1.4 Implementation Notes

Devices MUST:
- Respond to pings within 5 seconds
- Treat 3 consecutive missed pongs as connection loss
- NOT rely solely on TCP-level keepalive (insufficient granularity)

---

## 2. Failsafe State Transition

### 2.1 Entry into FAILSAFE

When connection loss is detected:

1. Device transitions controlState to FAILSAFE
2. Device applies failsafe limits immediately (within 1 second)
3. Device starts failsafeDuration countdown
4. Device MAY attempt to complete in-flight operations

**Timing requirements:**
- controlState transition: Within 1 second of detection
- Failsafe limit application: Within 1 second of detection
- Timer start: At moment of detection (not limit application)

### 2.2 Failsafe Limit Application

```
Effective limits during FAILSAFE:
  effectiveConsumptionLimit = failsafeConsumptionLimit
  effectiveProductionLimit = failsafeProductionLimit
```

All zone-specific limits are superseded by failsafe limits. When connection is restored, zone limits are reapplied.

### 2.3 FAILSAFE Duration

| Parameter | Range | Default |
|-----------|-------|---------|
| failsafeDuration | 2 hours - 24 hours | 4 hours |

Duration is configured during commissioning and stored in device configuration.

---

## 3. Reconnection During FAILSAFE

### 3.1 Reconnection Wins

**If a controller reconnects before failsafeDuration expires, the device returns to CONTROLLED state.**

```
Timeline:
  T+0s:     Connection lost → FAILSAFE
  T+3600s:  Controller reconnects (1 hour later)
            → TLS handshake completes
            → Device verifies certificate (same zone)
            → controlState transitions to CONTROLLED
            → Failsafe timer cancelled
            → Zone's previous limits may need to be re-sent
```

### 3.2 Race Condition: Reconnection vs AUTONOMOUS Transition

**Rule: If TLS handshake completes before FAILSAFE→AUTONOMOUS transition, reconnection wins**

```
failsafeDuration = 14400s (4 hours)

Timeline:
  T+0s:      Connection lost → FAILSAFE
  T+14398s:  Controller initiates reconnection
  T+14399s:  TLS handshake in progress
  T+14400s:  failsafeDuration would expire
  T+14401s:  TLS handshake completes
             → Device transitions to CONTROLLED (not AUTONOMOUS)
             → Reconnection wins because handshake completed
```

**Critical point:** The transition point is TLS handshake completion, not connection initiation.

### 3.3 Reconnection After AUTONOMOUS

If device has already transitioned to AUTONOMOUS:
- Device accepts new connections normally
- Controller must re-establish control relationship
- No special handling needed

---

## 4. Timer Accuracy and Persistence

### 4.1 Timer Accuracy Requirements

| Requirement | Value |
|-------------|-------|
| Accuracy | +/- 1% or +/- 60 seconds, whichever is greater |
| Clock source | Monotonic preferred, wall-clock acceptable |
| Drift compensation | Not required |

For a 4-hour failsafe:
- Acceptable range: 3h 57m 36s to 4h 2m 24s
- Or: 3h 59m to 4h 1m (with +/- 60s rule)

### 4.2 Timer Persistence Across Power Cycle

**Rule: Failsafe timer state SHOULD be persisted across device restart**

| Implementation | Behavior | PICS |
|----------------|----------|------|
| Persistent | Timer resumes from saved state | MASH.S.CTRL.B_FAILSAFE_PERSISTENT=1 |
| Non-persistent | Timer resets on restart | MASH.S.CTRL.B_FAILSAFE_PERSISTENT=0 |

**Persistent (recommended):**
```
T+0s:     Connection lost → FAILSAFE, timer starts (4h)
T+3600s:  Device loses power
T+3700s:  Device restarts
          → Device reads persisted state
          → Remaining duration: 4h - 3600s = 10800s
          → Timer resumes with 3h remaining
T+14400s: failsafeDuration expires → AUTONOMOUS
```

**Non-persistent:**
```
T+0s:     Connection lost → FAILSAFE, timer starts (4h)
T+3600s:  Device loses power
T+3700s:  Device restarts
          → Device starts in AUTONOMOUS (no persisted state)
          → Controller must reconnect and recommission if needed
```

### 4.3 Timer During Shutdown

If device is commanded to shut down during FAILSAFE:
- Timer pauses during shutdown sequence
- If device restarts, timer resumes (if persistent) or resets
- Shutdown does not count toward failsafeDuration

---

## 5. Multi-Zone Failsafe Behavior

### 5.1 Per-Zone Connection Tracking

Each zone's connection is tracked independently. The state machine in
§2 is **applied per zone slot** — each zone has its own connection-loss
detection, its own FAILSAFE entry, its own `failsafeDuration` countdown
to AUTONOMOUS, and its own failsafe limits.

**Commanded limits and setpoints persist across a zone's connection
loss** — see [Multi-Zone Resolution §6.2](multi-zone-resolution.md#62-zone-disconnects).
A grid operator's regulatory limit must remain in force even if its
controller's TCP connection drops momentarily, otherwise a controller
flap could lift the constraint. Persistence does **not** mean the
disconnected zone's commanded limit continues to dominate resolution
once the slot enters FAILSAFE: from that point the slot's contribution
is its failsafe limit (§2.2).

```
Device with Zone 1 (GRID) and Zone 2 (LOCAL):

T+0s:    Zone 2 connection lost (e.g. TCP RST)
         → Zone 2 connection-loss detected per §1
         → Zone 2 slot enters FAILSAFE per §2.1 (immediately)
         → Zone 2's contribution to resolution = its failsafe limit
         → Zone 2's failsafeDuration countdown to AUTONOMOUS starts
         → Zone 1 unaffected (still CONTROLLED)
         → Device-wide controlState aggregates: FAILSAFE
           (per the unified-controlState rule:
            FAILSAFE > OVERRIDE > LIMITED > CONTROLLED > AUTONOMOUS)

T+later: Zone 2 reconnects before its failsafeDuration expires
         → Zone 2 slot exits FAILSAFE, returns to CONTROLLED
         → Zone 2's commanded limit (which persisted) is again
           Zone 2's contribution to resolution
         → Device-wide controlState aggregates back to CONTROLLED
```

The "limits persist" rule is about the **commanded limit attribute**:
the controller does not have to re-issue `SetLimit` after a flap. While
the slot is in FAILSAFE the commanded limit is dormant; the failsafe
limit takes over for resolution purposes.

### 5.2 Per-Zone vs. Device-Wide FAILSAFE

FAILSAFE is a **per-zone-slot** state. The device's unified `controlState`
attribute aggregates across all zone slots (see
[energy-control.md "Unified Control State"](../../features/energy-control.md#unified-control-state)),
but the FAILSAFE precedence is **scoped to authoritative zones**: a lost
slot does NOT pull a still-controlled device into device-wide FAILSAFE
when at least one other authoritative zone (GRID or LOCAL) remains
connected.

- **Partial loss** (at least one authoritative zone still connected):
  the lost zone's slot enters FAILSAFE per §2.1 — its commanded limit
  becomes dormant and its failsafe limit contributes to resolution
  (§5.1). The device-wide `controlState` reflects the **most-concerning
  state across the still-authoritative zones**. The lost slot's
  FAILSAFE does not propagate to the device-wide aggregate; otherwise
  any momentary flap of any controller would shove the entire device
  into FAILSAFE even while another authoritative controller is fully
  in command. Per the "most restrictive wins" rule for limits, the
  lost zone's failsafe limit can still tighten the effective limit
  even though `controlState` does not flip to FAILSAFE.
- **Complete loss** (no authoritative zones connected): every slot
  is in FAILSAFE; device-wide `controlState` is FAILSAFE. Each slot
  independently runs its `failsafeDuration` timer to AUTONOMOUS
  (§2.3 / §3).

> **Design note (DEC-090).** An earlier draft of this section stated
> that any single lost zone takes the device into device-wide FAILSAFE.
> That semantics is too aggressive: a flapping LOCAL controller should
> not collapse a GRID-controlled device into FAILSAFE. The corpus
> (TC-FAIL-008 "Per-Zone Connection Loss Does Not Trigger FAILSAFE")
> encoded the safer behavior first; this section was amended to match.

### 5.3 Failsafe Configuration Source

failsafeConsumptionLimit and failsafeProductionLimit are device-level, not per-zone:

- Set during initial commissioning
- Highest-priority zone MAY update values
- Apply uniformly regardless of which zone(s) were connected

---

## 6. Grace Period (Optional)

### 6.1 Definition

Some devices support a **grace period** - a brief window after failsafeDuration during which the device still accepts reconnection before fully transitioning to AUTONOMOUS behavior.

### 6.2 Grace Period Behavior

| With Grace Period | Without Grace Period |
|-------------------|---------------------|
| failsafeDuration expires → grace state | failsafeDuration expires → AUTONOMOUS |
| graceDuration additional time | No additional time |
| Reconnection during grace → CONTROLLED | Reconnection after → new commission |

### 6.3 PICS Item

```
MASH.S.CTRL.B_FAILSAFE_GRACE = 0 (no grace period)
MASH.S.CTRL.B_FAILSAFE_GRACE = 1 (grace period supported)
MASH.S.CTRL.B_GRACE_DURATION = 300 (5 minutes, if supported)
```

---

## 7. PICS Items

| PICS Code | Description | Values |
|-----------|-------------|--------|
| MASH.S.CTRL.B_FAILSAFE_PERSISTENT | Timer persists across restart | 0, 1 |
| MASH.S.CTRL.B_FAILSAFE_GRACE | Grace period supported | 0, 1 |
| MASH.S.CTRL.B_GRACE_DURATION | Grace period duration (s) | 0-3600 |
| MASH.S.CTRL.B_DETECTION_MAX | Max detection delay (s) | 95 |
| MASH.S.CTRL.B_FAILSAFE_MIN | Minimum failsafeDuration (s) | 7200 |
| MASH.S.CTRL.B_FAILSAFE_MAX | Maximum failsafeDuration (s) | 86400 |

---

## 8. Test Cases

### TC-FAIL-001: Maximum Detection Delay

**Preconditions:**
- Device in CONTROLLED state
- Subscription to controlState active

**Test Steps:**
1. Record time T0
2. Block all network traffic to device (simulate partition)
3. Monitor for controlState change

**Expected:**
- controlState changes to FAILSAFE within 95 seconds of T0
- Timestamp of change: T0 + (65-95 seconds)

### TC-FAIL-002: Failsafe Limit Application Timing

**Preconditions:**
- Device in LIMITED state
- effectiveConsumptionLimit = 7400000 mW
- failsafeConsumptionLimit = 3700000 mW

**Test Steps:**
1. Block network traffic (trigger FAILSAFE)
2. Monitor actual device power

**Expected:**
- Within 1 second of FAILSAFE transition, device limits to 3700000 mW
- Power reading shows immediate compliance

### TC-FAIL-003: Reconnection During FAILSAFE

**Preconditions:**
- Device in FAILSAFE state
- failsafeDuration = 4 hours
- 1 hour elapsed since FAILSAFE entry

**Test Steps:**
1. Reconnect controller
2. Complete TLS handshake
3. Read controlState

**Expected:**
- controlState = CONTROLLED (not FAILSAFE or AUTONOMOUS)
- Failsafe timer cancelled

### TC-FAIL-004: Reconnection Race at Boundary

**Preconditions:**
- Device in FAILSAFE state
- failsafeDuration = 60 seconds (test value)

**Test Steps:**
1. Wait until T+58 seconds
2. Initiate connection
3. Complete TLS handshake at T+61 seconds

**Expected:**
- If handshake completed before transition: CONTROLLED
- Device behavior consistent with race resolution rule

### TC-FAIL-005: AUTONOMOUS Transition

**Preconditions:**
- Device in FAILSAFE state
- failsafeDuration = 60 seconds (test value)

**Test Steps:**
1. Wait 65 seconds without reconnecting
2. Read controlState

**Expected:**
- controlState = AUTONOMOUS
- Device operating without external control

### TC-FAIL-006: Timer Persistence (if supported)

**Preconditions:**
- Device in FAILSAFE state
- failsafeDuration = 4 hours
- 1 hour elapsed
- MASH.S.CTRL.B_FAILSAFE_PERSISTENT = 1

**Test Steps:**
1. Power cycle device
2. Wait for device to restart
3. Monitor for AUTONOMOUS transition

**Expected:**
- Device remains in FAILSAFE after restart
- AUTONOMOUS transition at original T+4h (accounting for downtime)

### TC-FAIL-007: Per-Zone Connection Loss (Failsafe Limit Replaces Commanded)

**Preconditions:**
- Zone 1 (GRID) and Zone 2 (LOCAL) connected
- Zone 1 commanded limit = 5000000 mW
- Zone 2 commanded limit = 7000000 mW
- Zone 2 failsafeConsumptionLimit = 3000000 mW
- effectiveLimit = `min(5000000, 7000000)` = 5000000 mW

**Test Steps:**
1. Disconnect Zone 2 (clean disconnect → detection within 1 s per §1.3)
2. Read controlState
3. Read effectiveConsumptionLimit

**Expected:**
- Zone 2 slot enters FAILSAFE on detection (per §2.1, applied per-slot)
- Zone 2's commanded limit persists (multi-zone-resolution.md §6.2) but
  is dormant while the slot is in FAILSAFE
- Zone 2's contribution to resolution is now its failsafe limit
  (3000000 mW), not its commanded value
- effectiveLimit = `min(5000000, 3000000)` = 3000000 mW
- Device-wide controlState = FAILSAFE (any slot in FAILSAFE wins per
  the unified-controlState rule)

### TC-FAIL-007b: Reconnection Restores Commanded Limit

**Preconditions:**
- Continuing from TC-FAIL-007 (Zone 2 in FAILSAFE)
- Zone 2's failsafeDuration has not elapsed

**Test Steps:**
1. Reconnect Zone 2 (TLS handshake + operational session)
2. Read effectiveConsumptionLimit
3. Read controlState

**Expected:**
- Zone 2 slot exits FAILSAFE → CONTROLLED
- Zone 2's persisted commanded limit (7000000 mW) is again its
  contribution
- effectiveLimit = `min(5000000, 7000000)` = 5000000 mW
- Device-wide controlState aggregates back to CONTROLLED

### TC-FAIL-008: All Zones Lost — Device-Wide FAILSAFE Persists

**Preconditions:**
- Zone 1 and Zone 2 connected
- Each zone's failsafeDuration is configured (e.g. 60 s test value)

**Test Steps:**
1. Disconnect Zone 1
2. Verify controlState = FAILSAFE (Zone 1 slot in FAILSAFE)
3. Disconnect Zone 2
4. Verify controlState = FAILSAFE (both slots in FAILSAFE)
5. Wait until both slots' failsafeDuration elapses without any reconnect
6. Read controlState

**Expected:**
- Step 2: controlState = FAILSAFE — Zone 1 slot in FAILSAFE, Zone 2 still
  CONTROLLED, aggregate is FAILSAFE
- Step 4: controlState = FAILSAFE — both slots in FAILSAFE
- Step 6: each slot has independently transitioned FAILSAFE → AUTONOMOUS
  (§2.3); device-wide controlState aggregates to AUTONOMOUS

### TC-FAIL-009: Timer Accuracy

**Preconditions:**
- Device in FAILSAFE state
- failsafeDuration = 3600 seconds (1 hour, for testing)

**Test Steps:**
1. Record FAILSAFE entry time T0
2. Monitor for AUTONOMOUS transition
3. Record transition time T1

**Expected:**
- T1 - T0 = 3600s +/- 1% (3564s to 3636s)
- Or +/- 60s (3540s to 3660s), whichever is greater

### TC-FAIL-010: Clean Disconnect Detection

**Preconditions:**
- Device in CONTROLLED state

**Test Steps:**
1. Send TCP RST to device
2. Monitor controlState

**Expected:**
- FAILSAFE entered within 1 second (immediate detection)
- No wait for ping/pong timeout

---

## 9. Related Documents

| Document | Description |
|----------|-------------|
| [Transport](../../transport.md) | Keep-alive mechanism |
| [EnergyControl](../../features/energy-control.md) | Failsafe attributes |
| [State Machines](state-machines.md) | ControlStateEnum transitions |
| [Security](../../security.md) | TLS connection handling |
