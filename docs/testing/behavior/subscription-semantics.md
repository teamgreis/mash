# Subscription Semantics

> Behavior specification for subscriptions, coalescing, and notifications

**Status:** Draft
**Last Updated:** 2025-01-25

---

## Purpose

This document specifies the exact behavior of MASH subscriptions, including coalescing, heartbeat, priming, and notification content. Clear semantics prevent interoperability issues from ambiguous subscription handling.

---

## 1. Subscription Overview

### 1.1 Subscription Parameters

| Parameter | Type | Description | Default |
|-----------|------|-------------|---------|
| minInterval | uint16 | Minimum seconds between notifications | 1 |
| maxInterval | uint16 | Maximum seconds without notification | 60 |
| attributeIds | array | Specific attributes to subscribe (empty = all) | [] |

### 1.2 Design Principles

1. **Coalesce to last value** - Multiple rapid changes result in one notification with final value
2. **Heartbeat confirms liveness** - maxInterval ensures periodic communication
3. **Priming provides initial state** - First notification includes current values
4. **Delta-only updates** - Only changed attributes in subsequent notifications
5. **Bounce-back optimization** - A→B→A within minInterval sends nothing

---

## 2. Coalescing Behavior

### 2.1 Last Value Wins

When multiple changes occur within minInterval, only the final value is sent:

```
minInterval = 5 seconds

Timeline:
  T+0.0s: Attribute changes to A
  T+0.5s: Attribute changes to B
  T+1.0s: Attribute changes to C
  T+5.0s: Notification sent with value C
```

**Rationale:** Controllers care about current state, not change history. Sending every intermediate value would overwhelm bandwidth without benefit.

### 2.2 Coalescing Window

The coalescing window starts when the first change occurs after the previous notification:

```
Last notification: T+0s
minInterval: 5s

  T+2s: First change → Window starts
  T+3s: Second change → Coalesced
  T+7s: Notification sent (5s from T+2s)
```

NOT:
```
  T+2s: First change
  T+5s: Notification (5s from T+0s) ← WRONG
```

### 2.3 No Pending Changes

If no changes occur during minInterval, no notification is sent until maxInterval (heartbeat).

```
minInterval = 5s, maxInterval = 60s

  T+0s:  Subscription established, priming sent
  T+0-60s: No changes occur
  T+60s: Heartbeat notification sent
```

---

## 3. Value Bounce-Back

### 3.1 Definition

A "bounce-back" occurs when a value changes and then returns to its original value within the coalescing window:

```
minInterval = 5s
Initial value: A

  T+0s: Value changes to B
  T+2s: Value changes back to A
  T+5s: No notification sent (net change is zero)
```

### 3.2 Rule: Suppress Bounce-Back

**If the value at notification time equals the value at the last notification, no notification is sent.**

This optimization prevents unnecessary network traffic when values briefly fluctuate but return to baseline.

### 3.3 Bounce-Back with Multiple Attributes

If subscribing to multiple attributes, bounce-back is evaluated per-attribute:

```
Subscribed: [attr1, attr2]
Last notified: {attr1: A, attr2: X}

  T+0s: attr1 changes to B
  T+1s: attr2 changes to Y
  T+2s: attr1 changes back to A (bounce-back)
  T+5s: Notification: {attr2: Y}  // Only attr2 changed net
```

---

## 4. Priming Notification

### 4.1 Definition

The **priming notification** is the first notification sent after a subscription is established. It provides the current state of all subscribed attributes.

### 4.2 Priming Content

The priming notification MUST include:
- All subscribed attributes (or all attributes if attributeIds is empty)
- Current value of each attribute
- Subscription ID

```cbor
// Priming notification for subscription to EnergyControl
{
  1: subscriptionId,           // uint32
  2: {                         // attributes map
    2: CONTROLLED,             // controlState
    20: 5000000,               // effectiveConsumptionLimit
    21: 7000000,               // myConsumptionLimit
    // ... all subscribed attributes
  }
}
```

### 4.3 Priming Timing

Priming notification is sent:
- Immediately upon subscription establishment
- Not subject to minInterval (sent right away)
- Before any change notifications

---

## 5. Heartbeat Notification

### 5.1 Purpose

Heartbeat notifications confirm the subscription is alive when no changes occur. They also allow devices to send current values in case of missed notifications.

### 5.2 Heartbeat Content

Heartbeat content is implementation-defined (PICS item):

| Mode | Content | PICS Value |
|------|---------|------------|
| Empty | Only subscriptionId, timestamp | "empty" |
| Full | All subscribed attributes with current values | "full" |

**Recommendation:** Use "full" mode for reliability, "empty" mode for bandwidth-constrained environments.

### 5.3 Heartbeat Timing

Heartbeat is sent when:
- Time since last notification >= maxInterval
- No other notification was sent during maxInterval

```
maxInterval = 60s, minInterval = 5s

  T+0s:  Priming notification
  T+30s: Change notification (timer resets)
  T+90s: Heartbeat (60s since T+30s)
```

---

## 6. Multi-Attribute Subscriptions

### 6.1 Subscribing to Multiple Attributes

A single subscription can cover multiple attributes:

```cbor
// Subscribe to multiple EnergyControl attributes
{
  1: 0x05,                     // feature: EnergyControl
  2: [2, 20, 21, 40, 41],      // attributeIds: controlState, limits, setpoints
  3: 1,                        // minInterval
  4: 60                        // maxInterval
}
```

### 6.2 Change Notification Content

Change notifications include **only changed attributes** (delta model):

```cbor
// Only effectiveConsumptionLimit changed
{
  1: subscriptionId,
  2: {
    20: 3000000                // effectiveConsumptionLimit (changed)
    // controlState, myConsumptionLimit, etc. NOT included (unchanged)
  }
}
```

### 6.3 Atomic Multi-Attribute Changes

When multiple attributes change together (e.g., SetLimit affects both effective and my limits):

1. All changes are coalesced into single notification
2. Notification includes all changed attributes
3. Order of attributes in notification is not guaranteed

```cbor
// SetLimit command caused multiple changes
{
  1: subscriptionId,
  2: {
    2: LIMITED,                // controlState changed
    20: 5000000,               // effectiveConsumptionLimit changed
    21: 5000000                // myConsumptionLimit changed
  }
}
```

---

## 7. Subscription Lifecycle

### 7.1 Establishment

1. Controller sends Subscribe request
2. Device validates request (feature exists, attributes valid)
3. Device creates subscription record
4. Device sends priming notification
5. Device sends Subscribe response with subscriptionId

### 7.2 Notification Flow

```
┌────────────┐              ┌────────────┐
│ Controller │              │   Device   │
└─────┬──────┘              └─────┬──────┘
      │    Subscribe(feature, attrs, min, max)
      │─────────────────────────────────────────>│
      │                                          │
      │               Priming notification       │
      │<─────────────────────────────────────────│
      │                                          │
      │       SubscribeResponse(subscriptionId)  │
      │<─────────────────────────────────────────│
      │                                          │
      │         ... time passes, value changes...│
      │                                          │
      │              Change notification         │
      │<─────────────────────────────────────────│
      │                                          │
      │         ... maxInterval with no changes..│
      │                                          │
      │             Heartbeat notification       │
      │<─────────────────────────────────────────│
```

### 7.3 Termination

Subscriptions end when:
- Controller sends Unsubscribe
- Connection is lost
- Device removes the subscription (resource limits)

On termination, device:
1. Removes subscription record
2. Stops sending notifications
3. Does NOT send a final notification

### 7.4 Reconnection

**Subscriptions do NOT survive connection loss.**

On reconnect, controller must:
1. Re-establish subscriptions
2. Receive new priming notifications
3. Compare with last known state to detect missed changes

---

## 8. Invalid Interval Handling

### 8.1 Validation Rules

| Condition | Behavior |
|-----------|----------|
| minInterval > maxInterval | Error: CONSTRAINT_ERROR |
| minInterval = 0 | Valid: every change notified (no coalescing) |
| maxInterval = 0 | Error: CONSTRAINT_ERROR (heartbeat required) |
| minInterval > 3600 | Warning: may delay notifications significantly |

### 8.2 Auto-Correction

Devices MAY auto-correct clearly invalid values:

```
Request: minInterval=100, maxInterval=50
Response: Error CONSTRAINT_ERROR

OR (implementation choice):

Response: Success, actual minInterval=50, maxInterval=100 (swapped)
```

PICS item `MASH.S.SUB.B_INTERVAL_AUTOCORRECT` indicates behavior.

---

## 9. Resource Limits

### 9.1 Subscription Limits

| Limit | Minimum Required | Typical |
|-------|-----------------|---------|
| Max subscriptions per connection | 10 | 50 |
| Max attributes per subscription | 20 | 100 |
| Max concurrent connections | 5 | 10 |

### 9.2 Exceeding Limits

When subscription limits are exceeded:

```cbor
// Response when max subscriptions reached
{
  1: false,                    // success = false
  2: RESOURCE_EXHAUSTED,       // error code
  3: "Maximum subscriptions reached"
}
```

Device MAY provide `currentCount` and `maxCount` in error response.

---

## 10. PICS Items

| PICS Code | Description | Values |
|-----------|-------------|--------|
| MASH.S.SUB.B_HEARTBEAT_CONTENT | Heartbeat content mode | "empty", "full" |
| MASH.S.SUB.B_COALESCE | Coalescing strategy | "last_value" |
| MASH.S.SUB.B_BOUNCE_BACK | Suppress bounce-back | 0, 1 |
| MASH.S.SUB.B_INTERVAL_AUTOCORRECT | Auto-correct invalid intervals | 0, 1 |
| MASH.S.SUB.B_MAX_SUBSCRIPTIONS | Maximum subscriptions | 10-255 |
| MASH.S.SUB.B_MAX_ATTRS_PER_SUB | Max attributes per subscription | 20-255 |

---

## 11. Test Cases

### TC-SUB-001: Priming Contains All Attributes

**Preconditions:**
- EnergyControl feature present

**Test Steps:**
1. Subscribe to EnergyControl with attributeIds = [2, 20, 21]
2. Capture priming notification

**Expected:**
- Priming contains all three attributes
- Values match current device state

### TC-SUB-002: Coalescing Within minInterval

**Preconditions:**
- Subscription active with minInterval = 5

**Test Steps:**
1. Trigger attribute change to value A
2. After 1 second, trigger change to value B
3. After 1 second, trigger change to value C
4. Wait for notification

**Expected:**
- Single notification received ~5 seconds after first change
- Notification contains value C only

### TC-SUB-003: Bounce-Back Suppression

**Preconditions:**
- Subscription active with minInterval = 5
- Attribute initially = X

**Test Steps:**
1. Change attribute to Y
2. After 2 seconds, change back to X
3. Wait 10 seconds

**Expected:**
- No notification sent (net change is zero)
- OR heartbeat sent at maxInterval (if reached)

### TC-SUB-004: Heartbeat at maxInterval

**Preconditions:**
- Subscription with minInterval = 5, maxInterval = 30
- Priming received

**Test Steps:**
1. Make no changes for 35 seconds
2. Monitor for notifications

**Expected:**
- Heartbeat notification received at ~30 seconds
- Content depends on PICS (empty or full)

### TC-SUB-005: Delta-Only Change Notifications

**Preconditions:**
- Subscription to [controlState, effectiveConsumptionLimit, myConsumptionLimit]
- Priming received

**Test Steps:**
1. Send SetLimit that changes only effectiveConsumptionLimit
2. Capture notification

**Expected:**
- Notification contains only effectiveConsumptionLimit
- controlState and myConsumptionLimit NOT in notification

### TC-SUB-006: Multi-Attribute Atomic Change

**Preconditions:**
- Subscription to multiple attributes
- Priming received

**Test Steps:**
1. Trigger action that changes multiple attributes simultaneously
2. Wait for notification within minInterval

**Expected:**
- Single notification with all changed attributes
- Not multiple separate notifications

### TC-SUB-007: Subscription Re-establishment on Reconnect

**Preconditions:**
- Active subscription

**Test Steps:**
1. Disconnect from device
2. Reconnect
3. Check subscription status

**Expected:**
- Original subscription no longer exists
- Must re-subscribe to receive notifications

### TC-SUB-008: Invalid Interval Handling

**Preconditions:**
- Device accepting subscriptions

**Test Steps:**
1. Subscribe with minInterval = 100, maxInterval = 50

**Expected:**
- Error response CONSTRAINT_ERROR
- OR success with corrected values (if auto-correct enabled)

### TC-SUB-009: Resource Limit Enforcement

**Preconditions:**
- Device with max 10 subscriptions

**Test Steps:**
1. Create 10 subscriptions successfully
2. Attempt 11th subscription

**Expected:**
- 11th subscription fails with RESOURCE_EXHAUSTED
- Existing subscriptions unaffected

### TC-SUB-010: minInterval = 0 (No Coalescing)

**Preconditions:**
- Subscription with minInterval = 0

**Test Steps:**
1. Rapidly change attribute: A → B → C
2. Monitor notifications

**Expected:**
- Three separate notifications (A, B, C)
- Each change triggers immediate notification

### TC-SUB-011: Partial Attribute Subscription

**Preconditions:**
- EnergyControl feature with many attributes

**Test Steps:**
1. Subscribe to only [effectiveConsumptionLimit]
2. Trigger change to myConsumptionLimit (not subscribed)
3. Trigger change to effectiveConsumptionLimit (subscribed)

**Expected:**
- No notification for myConsumptionLimit change
- Notification for effectiveConsumptionLimit change

### TC-SUB-012: Coalescing Window Start

**Preconditions:**
- Subscription with minInterval = 10
- Priming received at T+0

**Test Steps:**
1. At T+5: Trigger change A
2. At T+7: Trigger change B
3. Monitor for notification

**Expected:**
- Notification at T+15 (10 seconds from first change at T+5)
- NOT at T+10 (not 10 seconds from priming)

---

## Related Documents

| Document | Description |
|----------|-------------|
| [Interaction Model](../../interaction-model.md) | Subscribe operation |
| [Duration Semantics](duration-semantics.md) | Timer-based value expiry |
| [Transport](../../transport.md) | Connection handling |
