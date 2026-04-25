# MASH Interaction Model

> Read, Write, Subscribe, Invoke operations and CBOR serialization

**Status:** Draft
**Last Updated:** 2025-01-25

---

## Related Documents

| Document | Description |
|----------|-------------|
| [Protocol Overview](protocol-overview.md) | Vision, architecture, device model |
| [Transport](transport.md) | Framing, connection model |
| [Features](features/README.md) | Feature definitions |

---

## 1. Overview

MASH has 4 operations (like Matter, not SPINE's 7):

| Operation | Description | Direction |
|-----------|-------------|-----------|
| **Read** | Get current attribute values | Bidirectional |
| **Write** | Set attribute value (full replace) | Bidirectional |
| **Subscribe** | Register for change notifications | Bidirectional |
| **Invoke** | Execute command with parameters | Bidirectional |

**Bidirectional Communication:** All operations can be initiated by either side of the connection. A controller can send requests to a device, AND a device can send requests to a controller that exposes features. This enables scenarios like:
- Smart Meter Gateway (SMGW) exposing grid meter data to EMS devices
- Dual-service entities that act as both device and controller

---

## 2. Data Model

### 2.1 Serialization

**Format:** CBOR (RFC 8949)

**Why CBOR:**
- Binary format (smaller than JSON)
- Self-describing
- Integer keys for compactness
- Native support for binary data
- Wide language support

### 2.2 Integer Keys

All CBOR maps use integer keys for efficiency:

```cbor
// Instead of:
{ "acActivePower": 5000000, "acReactivePower": 200000 }

// MASH uses:
{ 1: 5000000, 2: 200000 }
```

### 2.3 Nullable vs Absent

MASH distinguishes between **nullable values** and **absent keys**:

| In a message | Meaning |
|--------------|---------|
| Key absent | Attribute not included in this message |
| Key with value | Attribute has this value |
| Key with `null` | Attribute value is explicitly null |

**In delta notifications:**

```cbor
// Delta notification examples
{
  5: {
    1: 5500000,    // acActivePower changed to 5500000
    2: null,       // acReactivePower changed TO null (explicitly cleared)
                   // acApparentPower (key 3) not present = unchanged
  }
}
```

**Important distinctions:**

| Scenario | Representation |
|----------|----------------|
| "No limit is set" | `myConsumptionLimit: null` |
| "Limit is zero" | `myConsumptionLimit: 0` |
| "Limit unchanged" | Key absent from delta |

**Attributes are never "deleted":**
- Attributes exist or don't based on device capabilities (conformance)
- What changes is the VALUE, not the attribute's existence
- For nullable attributes, `null` is a valid value meaning "not set" or "cleared"

**Full replacement on Write:**
- Write operations replace the entire attribute value
- To "clear" a nullable attribute, write `null` to it
- There is no separate "delete" operation

### 2.4 Command Parameters vs Attributes

**Attributes:**
- Stored on device
- Readable via Read operation
- Subscribable for change notifications
- Can be nullable (value = `null` means "not set")

**Command parameters:**
- Sent with Invoke operation
- NOT stored as readable values
- NOT subscribable
- Control behavior but are not exposed after command completes

Example - the `duration` parameter in SetLimit:
```cbor
// SetLimit command
{
  1: 5000000,    // consumptionLimit - STORED as myConsumptionLimit attribute
  3: 60,         // duration - NOT stored, starts internal timer
  4: 2           // cause - NOT stored, for logging/audit
}
```

After this command:
- `myConsumptionLimit` = 5000000 (readable, subscribable)
- `duration` = not accessible (timer running internally)
- `cause` = not accessible (logged but not readable)

**Omitting optional command parameters:**

| In command | Meaning |
|------------|---------|
| Key absent | Use default value for this parameter |
| Key with value | Use this value |
| Key with `null` | Invalid - command parameters don't use null |

To "change" a parameter like duration, re-send the entire command with new values.

### 2.5 Message Size Target

| Metric | Target |
|--------|--------|
| Typical message | < 2 KB |
| Maximum message | 8 KB |
| EEBUS comparison | 4 KB+ typical |

---

## 3. Message Format

### 3.1 Request Message

```cbor
{
  1: messageId,        // uint32: unique per connection
  2: operation,        // uint8: 1=Read, 2=Write, 3=Subscribe, 4=Invoke
  3: endpointId,       // uint8: target endpoint
  4: featureId,        // uint8: target feature
  5: payload           // operation-specific data
}
```

### 3.2 Response Message

```cbor
{
  1: messageId,        // uint32: matches request
  2: status,           // uint8: 0=success, or error code
  3: payload           // operation-specific response data (if success)
}
```

### 3.3 Notification Message (from subscriptions)

```cbor
{
  1: 0,                // messageId 0 = notification
  2: subscriptionId,   // uint32: subscription that triggered this
  3: endpointId,       // uint8
  4: featureId,        // uint8
  5: changes           // map of changed attributes
}
```

### 3.4 Control Message (from transport keep-alive)

Control messages (Ping / Pong / Close) live alongside Request / Response /
Notification but are wrapped in **CBOR tag 77** (`D8 4D`) per DEC-083.
The tag is the unambiguous CONTROL marker — any frame that does not begin
with `D8 4D` is application-layer.

```cbor
77({
  1: type,             // uint8: 1=ping, 2=pong, 3=close
  2: sequence          // uint32: optional, omitted when zero
})
```

See `transport.md` §6 for keep-alive semantics and timing.

### 3.5 MessageID Management

**MessageID Space:**
- Range: 1 to 4,294,967,295 (0 reserved for notifications)
- Scope: Per-connection, per-direction (each side has independent counters)
- Size: 32-bit (aligned with Matter, optimal for constrained MCUs per DEC-003)

**Allocation Rules:**
1. Start at 1 for new connections
2. Increment monotonically for each request
3. On overflow (reaching 2³²-1), wrap to 1 (skipping 0)

**Reuse Safety:**
With a 10-second request timeout (see Section 8.3), at most 10 seconds worth of MessageIDs can be in-flight simultaneously. Even at 10,000 requests/second (far beyond typical smart home traffic), only ~100,000 MessageIDs would be pending—negligible compared to the 4.3 billion available.

---

## 4. Read Operation

### 4.1 Read Request

```cbor
{
  1: 12345,            // messageId
  2: 1,                // operation: Read
  3: 1,                // endpointId
  4: 2,                // featureId (e.g., Measurement)
  5: [1, 2, 3]         // attributeIds to read (empty = all)
}
```

### 4.2 Read Response

```cbor
{
  1: 12345,            // messageId
  2: 0,                // status: success
  3: {                 // attribute values
    1: 5000000,        // acActivePower
    2: 200000,         // acReactivePower
    3: 5004000         // acApparentPower
  }
}
```

### 4.3 Read All Attributes

To read all attributes, send empty array:

```cbor
{
  1: 12346,
  2: 1,                // Read
  3: 1,
  4: 2,
  5: []                // empty = read all
}
```

---

## 5. Write Operation

### 5.1 Write Request

```cbor
{
  1: 12347,            // messageId
  2: 2,                // operation: Write
  3: 1,                // endpointId
  4: 3,                // featureId (e.g., EnergyControl)
  5: {                 // attributes to write
    21: 6000000        // myConsumptionLimit = 6kW
  }
}
```

### 5.2 Write Response

```cbor
{
  1: 12347,            // messageId
  2: 0,                // status: success
  3: {                 // resulting values (may differ from requested)
    20: 5000000,       // effectiveConsumptionLimit (min of all zones)
    21: 6000000        // myConsumptionLimit
  }
}
```

### 5.3 Key Simplifications

- **No partial updates** - Write replaces entire attribute value
- **No deleteAll/replaceAll** - Just Write with new value
- **Arrays are replaced entirely** - No append/remove operations

---

## 6. Subscribe Operation

### 6.1 Subscribe Request

```cbor
{
  1: 12348,            // messageId
  2: 3,                // operation: Subscribe
  3: 1,                // endpointId
  4: 2,                // featureId
  5: {
    1: [1, 2, 3],      // attributeIds to subscribe (empty = all)
    2: 1000,           // minInterval (ms) - don't notify faster than this
    3: 60000           // maxInterval (ms) - notify at least this often
  }
}
```

### 6.2 Subscribe Response

```cbor
{
  1: 12348,            // messageId
  2: 0,                // status: success
  3: {
    1: 5001,           // subscriptionId
    2: {               // current values (initial snapshot)
      1: 5000000,
      2: 200000,
      3: 5004000
    }
  }
}
```

### 6.3 Change Notification

When subscribed attributes change:

```cbor
{
  1: 0,                // messageId 0 = notification
  2: 5001,             // subscriptionId
  3: 1,                // endpointId
  4: 2,                // featureId
  5: {                 // changed attributes only
    1: 5500000         // acActivePower changed
  }
}
```

### 6.4 Unsubscribe

```cbor
{
  1: 12349,
  2: 3,                // Subscribe operation
  3: 0,                // endpointId 0 = unsubscribe
  4: 0,                // featureId 0
  5: {
    1: 5001            // subscriptionId to cancel
  }
}
```

### 6.5 Subscription Behavior

#### Static attributeList (DEC-051)

`attributeList` is **immutable for the lifetime of a connection**. It reflects the device's hardware capabilities, not transient runtime state:

- Attributes that are supported but currently have no value report `null`
- Example: `evStateOfCharge` remains in `attributeList` when no EV is plugged in, but its value is `null`
- A change in hardware configuration (e.g., modular device reconfiguration) requires the device to close and re-establish the connection

This applies to all features. Controllers can read `attributeList` once at discovery time and build a stable data model.

#### AttributeList Size Bound (DEC-072)

An `attributeList` read response MUST fit in a single transport frame (8 KB per DEC-069). There is no chunking mechanism. Feature designers are responsible for keeping a feature's attribute count within budget:

- Current maximum observed (EnergyControl): <100 attributes, encoded `attributeList` well under 1 KB.
- Practical cap: ~3000 attribute IDs per feature (at 2 bytes each + CBOR overhead, well under 8 KB).
- If a feature's attribute set would exceed the cap: split across endpoints (one feature instance per endpoint, with distinct attribute subsets) rather than chunking the response.
- Reference implementation regression guard: `TestAttributeList_FitsInSingleMessage` in `pkg/features/features_test.go`.

#### Feature-Level Subscription (DEC-052)

The default subscription model is **subscribe to all attributes** of a feature:

```cbor
{
  1: 12348,            // messageId
  2: 3,                // operation: Subscribe
  3: 1,                // endpointId
  4: 2,                // featureId (e.g., Measurement)
  5: {
    1: [],             // attributeIds: empty = all
    2: 1000,           // minInterval (ms)
    3: 60000           // maxInterval (ms)
  }
}
```

When `attributeIds` is empty, the device reports all supported attributes. Combined with:
- **minInterval** for batching: simultaneous changes (e.g., power, current, voltage from one measurement cycle) are coalesced into a single notification
- **Delta notifications**: only changed attributes are sent
- **Nullable attributes**: device only sends attributes it has; unsupported attributes never appear

This means a single subscription to a feature delivers all relevant telemetry without the controller needing to know which specific attributes the device supports.

#### Priming Report (Initial Response)

When a subscription is established, the Subscribe Response includes **all** requested attributes' current values. This is the **priming report**:

```cbor
// Subscribe Response (priming report)
{
  1: 12348,            // messageId
  2: 0,                // status: success
  3: {
    1: 5001,           // subscriptionId
    2: {               // PRIMING REPORT: ALL subscribed attributes
      1: 5000000,      // acActivePower (current value)
      2: 200000,       // acReactivePower (current value)
      3: 5004000       // acApparentPower (current value)
    }
  }
}
```

**Why priming is required:**
- Client gets consistent baseline state at subscription start
- Without priming, client would only see future changes, not current state
- Essential for late-joining controllers

#### Subsequent Notifications (Deltas)

After the priming report, notifications contain **only changed attributes**:

```cbor
// Notification (delta)
{
  1: 0,                // messageId 0 = notification
  2: 5001,             // subscriptionId
  3: 1,                // endpointId
  4: 2,                // featureId
  5: {                 // ONLY CHANGED attributes
    1: 5500000         // acActivePower changed (others unchanged, not sent)
  }
}
```

#### Interval Parameters

| Parameter | Description |
|-----------|-------------|
| minInterval | Minimum time between notifications (coalescing). Multiple changes within minInterval are batched into one notification. |
| maxInterval | Maximum time without notification (heartbeat). If no changes occur, device sends notification with current values at maxInterval. |

#### Heartbeat Notification

If no changes occur within maxInterval, device sends a heartbeat notification containing the **current values** of all subscribed attributes (same format as priming report, but as a notification):

```cbor
// Heartbeat notification (no changes, but confirming device is alive)
{
  1: 0,                // messageId 0 = notification
  2: 5001,             // subscriptionId
  3: 1,                // endpointId
  4: 2,                // featureId
  5: {                 // Current values (heartbeat)
    1: 5500000,
    2: 200000,
    3: 5700000
  }
}
```

#### Reconnection

| Behavior | Description |
|----------|-------------|
| On reconnect | Subscriptions are lost; client must re-subscribe |
| New priming | Re-subscription triggers new priming report |
| Missed changes | Client should compare new priming data with last known state |

---

## 7. Invoke Operation

### 7.1 Invoke Request

```cbor
{
  1: 12350,            // messageId
  2: 4,                // operation: Invoke
  3: 1,                // endpointId
  4: 3,                // featureId (EnergyControl)
  5: {
    1: 1,              // commandId: SetLimit
    2: {               // command parameters
      1: 6000000,      // consumptionLimit
      4: 2             // cause: LOCAL_PROTECTION
    }
  }
}
```

### 7.2 Invoke Response

```cbor
{
  1: 12350,            // messageId
  2: 0,                // status: success
  3: {                 // command response
    1: true,           // success
    2: 5000000,        // effectiveConsumptionLimit
    3: null            // effectiveProductionLimit (unchanged)
  }
}
```

---

## 8. Error Handling

### 8.1 Status Codes

| Code | Name | Description |
|------|------|-------------|
| 0 | SUCCESS | Operation completed successfully |
| 1 | INVALID_ENDPOINT | Endpoint doesn't exist |
| 2 | INVALID_FEATURE | Feature doesn't exist on endpoint |
| 3 | INVALID_ATTRIBUTE | Attribute doesn't exist |
| 4 | INVALID_COMMAND | Command doesn't exist |
| 5 | INVALID_PARAMETER | Parameter value out of range |
| 6 | READ_ONLY | Cannot write to read-only attribute |
| 7 | WRITE_ONLY | Cannot read from write-only attribute |
| 8 | NOT_AUTHORIZED | Zone doesn't have permission |
| 9 | BUSY | Device is busy, try again |
| 10 | UNSUPPORTED | Operation not supported |
| 11 | CONSTRAINT_ERROR | Value violates constraint |
| 12 | TIMEOUT | Operation timed out |
| 13 | RESOURCE_EXHAUSTED | A device resource limit has been reached (e.g. subscription table full) |

### 8.2 Error Response

```cbor
{
  1: 12345,            // messageId
  2: 5,                // status: INVALID_PARAMETER
  3: {
    1: "consumptionLimit must be >= 0"  // error message (optional)
  }
}
```

### 8.3 Request Timeout

Clients MUST implement request timeouts:

| Parameter | Value |
|-----------|-------|
| Default timeout | 10 seconds |

**Timeout Behavior:**
1. If no response received within timeout, client SHOULD close the connection
2. Client MAY immediately attempt reconnection
3. Client MUST re-send any pending requests after reconnection

**Rationale:**
A missing response indicates connection failure (since devices MUST respond to every request). The 10-second timeout balances responsiveness with allowing time for slow operations like command execution.

Devices SHOULD respond within 5 seconds for typical operations.

---

## 9. Addressing

### 9.1 Address Format

```
device_id / endpoint_id / feature_id / attribute_or_command
evse-001  / 1           / Measurement / acActivePower
```

### 9.2 Wildcards

Not supported. Each request targets specific endpoint/feature.

---

## 10. Comparison with EEBUS SPINE

| Aspect | EEBUS SPINE | MASH |
|--------|-------------|------|
| Operations | 7 RFE modes | 4 operations |
| Serialization | JSON (verbose) | CBOR (compact) |
| Partial updates | Complex partial mechanisms | Full replace only |
| Bindings | Separate from subscriptions | Unified subscription |
| Events | Function-based | Dedicated event model |
| Error codes | Generic status | Specific error codes |

**Key simplifications:**
- 4 operations instead of 7 RFE modes
- No partial updates complexity
- Unified subscription model
- Binary format for efficiency

---

## 11. Bidirectional Communication

### 11.1 Overview

MASH supports bidirectional communication: both sides of a connection can send requests and handle responses. This enables advanced scenarios where:

- **Controllers expose features** that devices can query (e.g., grid meter data)
- **Dual-service entities** act as both device and controller simultaneously

### 11.2 Controller-Exposed Features

A controller can optionally expose a device model to connected devices. When configured, devices can:

- **Read** attributes from the controller's exposed features
- **Subscribe** to controller attribute changes
- **Write** to controller attributes (if writable)
- **Invoke** commands on the controller (if supported)

If a controller does not expose features, incoming requests from devices receive `StatusUnsupported`.

### 11.3 Example: Smart Meter Gateway (SMGW)

A Smart Meter Gateway acts as a controller but also exposes meter data:

```
SMGW (Controller + Exposed Device)
├── Controls: EV Charger, Heat Pump, Battery
└── Exposes: Grid Meter (Measurement feature)
    └── Attributes: acActivePower, acVoltage, acCurrent
```

**Data flow:**
1. SMGW connects to EV charger as controller
2. EV charger can read grid meter data from SMGW
3. EV charger can subscribe to grid meter changes
4. Both directions work over the same TLS connection

### 11.4 Example: Dual-Service Entity (EMS)

An Energy Management System (EMS) can be both:
- A **device** that SMGW controls (receives limits)
- A **controller** that manages household devices

```
                SMGW
                  │
       ┌─────────┼─────────┐
       │     EMS │         │
       │   (Dual-Service)  │
       │         │         │
    ┌──┴──┐   ┌──┴──┐   ┌──┴──┐
    │EVSE │   │Batt │   │Heat │
    │     │   │     │   │Pump │
    └─────┘   └─────┘   └─────┘
```

**EMS as device:** Accepts limits from SMGW, exposes Status/Measurement
**EMS as controller:** Sets limits on EVSE, Battery, Heat Pump

### 11.5 Subscription Behavior

Bidirectional subscriptions work independently:

| Direction | Description |
|-----------|-------------|
| Controller → Device | Controller subscribes to device's features (normal) |
| Device → Controller | Device subscribes to controller's exposed features |

Both directions use the same subscription mechanism:
- Priming report on subscription establishment
- Delta notifications on attribute changes
- Interval parameters (minInterval, maxInterval)

### 11.6 Implementation Notes

**On the controller side:**
- Optionally configure exposed device model
- Handle incoming requests through protocol handler
- Manage subscriptions from devices

**On the device side:**
- Can send requests to connected controllers
- Handle responses and notifications from controllers
- Track outbound subscriptions separately from inbound

**Connection semantics:**
- Single TLS connection serves both directions
- Message IDs are independent per direction
- Subscription IDs are independent per direction
