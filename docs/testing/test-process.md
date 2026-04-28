# MASH Conformance Test Process

> How the test harness drives protocol-level conformance testing against real devices.

**Status:** Current
**Created:** 2026-02-07

---

## 1. Overview

The MASH conformance test runner (`mash-test`) executes YAML-defined test cases against a real device over TLS. Each test case declares preconditions, a sequence of protocol steps, and expected outcomes.

```
YAML test files  -->  Loader  -->  Engine  -->  Runner  -->  CBOR over TLS  -->  Device
                                     |            |
                                  checks       builds
                                expectations   wire.Request
```

**Components:**

| Component | Package | Role |
|-----------|---------|------|
| Loader | `internal/testharness/loader` | Parses YAML files into `TestCase` / `Step` structs |
| Engine | `internal/testharness/engine` | Dispatches actions to handlers, checks expectations |
| Runner | `internal/testharness/runner` | Registers protocol-aware handlers, manages connections |
| Reporter | `internal/testharness/reporter` | Formats results as text, JSON, or JUnit XML |

The runner registers handlers for protocol operations (`read`, `write`, `subscribe`, `invoke`), commissioning (`commission`, `pase_request`, ...), test control (`trigger_test_event`), and utility actions (`wait`, `connect`, `disconnect`, `verify`).

---

## 2. The TEST Zone (DEC-060)

MASH devices support up to 3 zone slots when test mode is enabled. The test harness commissions as a **TEST zone** -- a dedicated observer zone that cannot interfere with production behavior.

### Zone Type Table

| Zone Type | ID Value | Priority | Max per Device | Resolution Role |
|-----------|----------|----------|----------------|-----------------|
| GRID | 1 | 2 (highest) | 1 | Participates in limits and setpoints |
| LOCAL | 2 | 1 | 1 | Participates in limits and setpoints |
| TEST | 3 | 0 (lowest) | 1 | **Observer-only**: excluded from resolution |

### Key Properties

- **Observer-only**: TEST zones are excluded from limit resolution (most-restrictive-wins) and setpoint resolution (highest-priority-wins). The test harness can read state and invoke commands without affecting production behavior.
- **Gated by TestMode**: Devices only accept TEST zones when `DeviceConfig.TestMode == true` and the enable-key matches.
- **MaxZones bumped to 3**: In production, devices allow 2 zones (GRID + LOCAL). When TestMode is enabled, the maximum is 3 (GRID + LOCAL + TEST).
- **Zone type from certificate**: The zone type is encoded in the Zone CA certificate's `OrganizationalUnit[0]` field. The device extracts the zone type via `cert.ExtractZoneTypeFromCert()`.
- **Zone ID derivation**: `SHA256(sessionKey)[:8]` hex-encoded, where `sessionKey` is the shared secret from the PASE handshake. Both sides compute this independently.

---

## 3. Test Lifecycle

### 3.1 Precondition Hierarchy

Each test case declares preconditions. The runner maps these to a numeric level and performs the minimum state transitions needed:

| Level | Name | Setup Required |
|-------|------|----------------|
| 0 | None | No-op (device booted, controller running, simulation flags) |
| 1 | Commissioning | Ensure disconnected (clean state for commissioning tests) |
| 2 | Connected | Establish commissioning TLS connection |
| 3 | Commissioned | Connect + PASE handshake + cert exchange + operational reconnect |

The runner tracks its current level and transitions forward or backward:

- **Forward** (e.g., 0 -> 3): Connects, performs PASE, exchanges certs, reconnects operationally.
- **Backward** (e.g., 3 -> 1): Sends RemoveZone, disconnects, waits for device to re-enter commissioning mode.

### 3.2 Per-Test Flow

```
1. setupPreconditions(tc)
   |-- Determine needed level from tc.Preconditions
   |-- Reset device state if modified by previous test (TriggerResetTestState)
   |-- Close stale zone connections
   |-- Transition to needed level (connect / commission / disconnect)
   |-- Handle special preconditions (two_zones_connected, device_zones_full, etc.)

2. For each step in tc.Steps:
   |-- engine.Dispatch(step.Action, step.Params)
   |     |-- handler builds wire.Request
   |     |-- CBOR-encode and send over TLS
   |     |-- Read and decode wire.Response
   |     |-- Return output map
   |-- engine.CheckExpectations(step.Expect, outputs)

3. teardownTest(tc)
   |-- Close per-test resources (security pool connections)
```

### 3.3 Expectation Checking

The engine compares step outputs against the YAML `expect` map. Standard checks include equality, boolean, and numeric comparisons. The runner also registers enhanced checkers for protocol-specific validations (e.g., `response_contains`, `value_greater_than`).

---

## 4. Connection Lifecycle

The test harness establishes connections through multiple phases. Here is the message flow for a full commission-and-test sequence:

```
Controller (test harness)                          Device
    |                                                |
    |  Phase 1: Commissioning TLS                    |
    |-------- TLS ClientHello (self-signed) -------->|
    |<------- TLS ServerHello + Certificate ---------|
    |-------- TLS Finished ----->------------------->|
    |           (TLS 1.3 established)                |
    |                                                |
    |  Phase 2: PASE (SPAKE2+)                       |
    |-------- PASERequest {1:1, 2:pA, 3:id} ------->|
    |<------- PASEResponse {1:2, 2:pB} --------------|
    |-------- PASEConfirm {1:3, 2:cA} ------------->|
    |<------- PASEComplete {1:4, 2:cB, 3:0} --------|
    |           (shared secret derived)              |
    |                                                |
    |  Phase 3: Certificate Exchange                 |
    |-------- CertRenewalRequest {1:30, 2:nonce} --->|
    |<------- CertRenewalCSR {1:31, 2:csr} ----------|
    |-------- CertRenewalInstall {1:32, 2:cert} ---->|
    |<------- CertRenewalAck {1:33, 2:0} ------------|
    |                                                |
    |  Phase 4: Transition to Operational            |
    |-------- TCP close ---->                        |
    |           (brief wait for device readiness)    |
    |-------- TLS ClientHello (mutual auth) -------->|
    |<------- TLS ServerHello + Certificate ---------|
    |-------- TLS Finished (with client cert) ------>|
    |           (operational TLS established)         |
    |                                                |
    |  Phase 5: Operational Messages                 |
    |-------- Read {1:id, 2:1, 3:ep, 4:ft, 5:...} ->|
    |<------- Response {1:id, 2:0, 3:data} ----------|
    |-------- Subscribe {1:id, 2:3, 3:ep, 4:ft} --->|
    |<------- Response {1:id, 2:0, 3:{1:subId,...}} -|
    |<------- Notification {1:0, 2:subId, ...} ------|
    |-------- Invoke {1:id, 2:4, 3:ep, 4:ft, 5:..} >|
    |<------- Response {1:id, 2:0, 3:result} --------|
    |                                                |
    |  Teardown                                      |
    |-------- Invoke RemoveZone -------->            |
    |-------- ControlClose {1:3} -------->           |
    |-------- TCP close ---->                        |
```

### Phase Details

**Phase 1 -- Commissioning TLS:** Self-signed server certificate, no client certificate required. Uses `transport.NewCommissioningTLSConfig()` (TLS 1.3, ALPN "mash/1").

**Phase 2 -- PASE (SPAKE2+):** 4-message handshake over the commissioning TLS connection. Derives a 32-byte shared secret used for zone ID derivation.

**Phase 3 -- Certificate Exchange:** Uses renewal message types (30-33) for the initial cert exchange. The controller generates a Zone CA, sends a nonce, receives a CSR, signs a device operational certificate, and installs it.

**Phase 4 -- Transition to Operational:** The commissioning TLS connection is closed. The controller reconnects using operational TLS with mutual authentication (controller cert + Zone CA trust chain).

**Phase 5 -- Operational Messages:** Standard MASH request/response protocol using CBOR-encoded messages with integer keys.

---

## 5. Protocol Message Format Reference

### 5.1 Request

```
{
  1: messageId,      // uint32 (must be non-zero; 0 = notification)
  2: operation,      // uint8:  1=Read, 2=Write, 3=Subscribe, 4=Invoke
  3: endpointId,     // uint8
  4: featureId,      // uint8
  5: payload         // operation-specific (may be omitted)
}
```

### 5.2 Response

```
{
  1: messageId,      // uint32 (matches request)
  2: status,         // uint8:  0=success, or error code
  3: payload         // operation-specific response data (if success)
}
```

### 5.3 Notification

```
{
  1: 0,              // messageId 0 = notification
  2: subscriptionId, // uint32
  3: endpointId,     // uint8
  4: featureId,      // uint8
  5: changes         // map of changed attribute IDs to values
}
```

### 5.4 Control Messages

```
{
  1: type,           // uint8: 1=Ping, 2=Pong, 3=Close
  2: sequence        // uint32 (optional)
}
```

### 5.5 Operation Codes

| Code | Operation | Description |
|------|-----------|-------------|
| 1 | Read | Get current attribute values |
| 2 | Write | Set attribute values (full replace) |
| 3 | Subscribe | Register for change notifications |
| 4 | Invoke | Execute a command with parameters |

### 5.6 Status Codes

| Code | Name | Description |
|------|------|-------------|
| 0 | SUCCESS | Operation completed successfully |
| 1 | INVALID_ENDPOINT | Endpoint doesn't exist |
| 2 | INVALID_FEATURE | Feature doesn't exist on the endpoint |
| 3 | INVALID_ATTRIBUTE | Attribute doesn't exist |
| 4 | INVALID_COMMAND | Command doesn't exist |
| 5 | INVALID_PARAMETER | Parameter value out of range |
| 6 | READ_ONLY | Attempt to write a read-only attribute |
| 7 | WRITE_ONLY | Attempt to read a write-only attribute |
| 8 | NOT_AUTHORIZED | Zone doesn't have permission |
| 9 | BUSY | Device busy, try again later |
| 10 | UNSUPPORTED | Operation not supported |
| 11 | CONSTRAINT_ERROR | Value violates a constraint |
| 12 | TIMEOUT | Operation timed out |
| 13 | RESOURCE_EXHAUSTED | Resource limit reached |

### 5.7 Commissioning Message Types

| Type | Name | Direction | Description |
|------|------|-----------|-------------|
| 1 | PASERequest | C -> D | Initiates SPAKE2+ exchange (contains pA, client identity) |
| 2 | PASEResponse | D -> C | Server's public value (pB) |
| 3 | PASEConfirm | C -> D | Client's confirmation MAC |
| 4 | PASEComplete | D -> C | Server's confirmation + error code (0=success) |
| 10 | CSRRequest | C -> D | Request CSR from device (contains nonce) |
| 11 | CSRResponse | D -> C | Device's PKCS#10 CSR |
| 12 | CertInstall | C -> D | Operational cert + CA cert + zone type/priority |
| 13 | CertInstallResponse | D -> C | Confirms installation (error code) |
| 20 | CommissioningComplete | C -> D | Commissioning finished |
| 30 | CertRenewalRequest | C -> D | Nonce + optional Zone CA (initial exchange uses this) |
| 31 | CertRenewalCSR | D -> C | New CSR + nonce hash (DEC-047 binding) |
| 32 | CertRenewalInstall | C -> D | New cert + sequence number |
| 33 | CertRenewalAck | D -> C | Status + active sequence |
| 255 | CommissioningError | D -> C | Error code + message + retry-after (DEC-063) |

### 5.8 Feature IDs

| ID | Feature | Description |
|----|---------|-------------|
| 0x01 | DeviceInfo | Device identity and structure |
| 0x02 | Status | Operating state and fault information |
| 0x03 | Electrical | Static electrical configuration |
| 0x04 | Measurement | Power, energy, voltage, current telemetry |
| 0x05 | EnergyControl | Limits, setpoints, and control commands |
| 0x06 | ChargingSession | EV charging session data |
| 0x07 | Tariff | Price structure and power tiers |
| 0x08 | Signals | Time-slotted prices, limits, and forecasts |
| 0x09 | Plan | Device intended behavior/schedule |
| 0x0A | TestControl | Test control and event triggering |

---

## 6. Worked Examples

### Example 1: Read DeviceInfo

**YAML step:**

```yaml
- action: read
  params:
    endpoint: 0
    feature: DeviceInfo
  expect:
    read_success: true
```

**What the runner does:**

1. Resolver maps `"DeviceInfo"` to feature ID `0x01`.
2. Runner builds `wire.Request{MessageID: 1, Operation: 1, EndpointID: 0, FeatureID: 1}`.
3. CBOR encodes to: `{1: 1, 2: 1, 3: 0, 4: 1}`.
4. Sends via `framer.WriteFrame(data)`.
5. Reads response frame, decodes `wire.Response{MessageID: 1, Status: 0, Payload: {1: "MASH-1234", ...}}`.
6. Returns `{read_success: true, value: ..., status: SUCCESS}`.
7. Engine checks `read_success == true` -- PASS.

### Example 2: Invoke SetLimit

**YAML step:**

```yaml
- action: invoke
  params:
    endpoint: 1
    feature: EnergyControl
    command: SetLimit
    args:
      consumptionLimit: 7000000
      cause: 3
  expect:
    invoke_success: true
```

**What the runner does:**

1. Resolver maps `"EnergyControl"` to `0x05`, `"SetLimit"` to command ID `0x01`.
2. Runner builds:
   ```
   wire.Request{
     MessageID:  2,
     Operation:  4,        // OpInvoke
     EndpointID: 1,
     FeatureID:  5,        // EnergyControl
     Payload: &wire.InvokePayload{
       CommandID:  1,       // SetLimit
       Parameters: {"consumptionLimit": 7000000, "cause": 3},
     },
   }
   ```
3. CBOR encodes to: `{1: 2, 2: 4, 3: 1, 4: 5, 5: {1: 1, 2: {"consumptionLimit": 7000000, "cause": 3}}}`.
4. Sends request, reads response `{1: 2, 2: 0, 3: {"effectiveConsumptionLimit": 7000000}}`.
5. Returns `{invoke_success: true, result: ..., status: SUCCESS}`.

### Example 3: Subscribe to Measurement

**YAML step:**

```yaml
- action: subscribe
  params:
    endpoint: 1
    feature: Measurement
  expect:
    subscribe_success: true
    priming_received: true
```

**What the runner does:**

1. Resolver maps `"Measurement"` to `0x04`.
2. Runner builds `wire.Request{MessageID: 3, Operation: 3, EndpointID: 1, FeatureID: 4}`.
3. Sends request.
4. Reads response: `{1: 3, 2: 0, 3: {1: 42, 2: {1: 1500000, 2: 230000}}}`.
   - Key 1 in payload: subscriptionId = 42
   - Key 2 in payload: currentValues (priming report) with attribute values
5. Returns `{subscribe_success: true, subscription_id: 42, priming_received: true}`.
6. Subsequent notifications arrive as: `{1: 0, 2: 42, 3: 1, 4: 4, 5: {1: 1600000}}`.

### Example 4: TestControl Trigger

**YAML step:**

```yaml
- action: trigger_test_event
  params:
    event_trigger: "0x0001000000000001"
  expect:
    trigger_sent: true
    success: true
```

**What the runner does:**

1. Parses hex trigger `0x0001000000000001` = `TriggerEnterCommissioningMode`.
2. Gets enable-key from runner config (e.g., `"00112233445566778899aabbccddeeff"`).
3. Builds invoke request targeting endpoint 0, feature `0x0A` (TestControl), command `0x01` (triggerTestEvent).
4. Payload: `{1: 1, 2: {"enableKey": "0011...", "eventTrigger": 281474976710657}}`.
5. Sends request, reads response with `success: true`.
6. Returns `{trigger_sent: true, event_trigger: 281474976710657, success: true}`.

---

## 7. TestControl / Enable-Key Mechanism

The TestControl feature (ID `0x0A`) on endpoint 0 provides a safety-gated mechanism for the test harness to trigger device state changes needed for conformance testing.

### TriggerTestEvent Command

| Field | Value |
|-------|-------|
| Feature | TestControl (0x0A) |
| Endpoint | 0 (DEVICE_ROOT) |
| Command ID | 0x01 |
| Parameters | `enableKey` (string, 32-char hex) + `eventTrigger` (uint64) |
| Response (success) | `{1: success: bool}` per `docs/features/test-control/1.0.yaml` |

**Safety gates (DEC-087):**
- Device MUST have `TestMode = true` in its configuration. Devices that do not differentiate calling zone types (i.e. cannot enforce the TEST-zone rule below) MUST NOT implement TestControl.
- The enable-key in the request MUST match the device's configured enable-key (128-bit, hex-encoded). On mismatch, the device MUST surface a non-success Status (see *Failure Status semantics* below); it MUST NOT silently return `{1: false}`.
- TestControl commands are accepted ONLY from controllers whose calling zone type is **TEST** (see [`multi-zone.md`](../multi-zone.md)). Calls from GRID or LOCAL zones MUST be rejected with `Status.NOT_AUTHORIZED`. Devices that lack a TEST-zone capability cannot meet this gate and therefore cannot ship TestControl.

### Failure Status semantics (DEC-087)

The success-path response shape (`{1: success: bool}`) describes what a successful trigger looks like on the wire. Failure paths use the regular non-success Status envelope per `docs/interaction-model.md §8`:

| Failure | Status | Notes |
|---------|--------|-------|
| Calling zone is not TEST | `NOT_AUTHORIZED` (8) | TEST-zone gate. |
| `enableKey` does not match the device's configured key | `NOT_AUTHORIZED` (8) | Auth-style failure. |
| `enableKey` parameter missing or wrong type | `INVALID_PARAMETER` (5) | Schema check before auth. |
| `eventTrigger` parameter missing or wrong type | `INVALID_PARAMETER` (5) | Schema check before auth. |
| Trigger opcode unknown to this device | `UNSUPPORTED` (10) or `CONSTRAINT_ERROR` (11) | `UNSUPPORTED` if the upper-2-byte feature domain isn't recognised; `CONSTRAINT_ERROR` if the domain is recognised but the device lacks the named feature on any endpoint. |
| Device cannot apply the trigger (e.g. failsafe active) | `BUSY` (9) | Reserved for transient refusals. |
| Device has no trigger handler registered (uninitialised TestMode build) | `UNSUPPORTED` (10) | Diagnostic; a bare `{1: false}` is forbidden because it is indistinguishable from a successful no-op. |

Implementations MUST map their internal failures to one of these statuses. The bare `{1: false}` shape is reserved for the success path only — devices MUST NOT return it from a failure.

### Trigger Opcode Registry

The full trigger opcode catalogue lives in [`docs/features/test-control/triggers.yaml`](../features/test-control/triggers.yaml) (DEC-087). New triggers MUST be added to that registry first; implementations follow.

The opcode layout is:

```
bits 63..48 — domain: feature-id of the affected feature
bits 47..32 — sub-domain selector (parameterised triggers only; zero for fixed opcodes)
bits 31..0  — payload value or fixed selector
```

Common examples (the registry is authoritative):

| Opcode | Name | Domain | Effect |
|--------|------|--------|--------|
| `0x0001_0000_0000_0001` | EnterCommissioningMode | DeviceInfo | Device opens commissioning window |
| `0x0001_0000_0000_0002` | ExitCommissioningMode | DeviceInfo | Device closes commissioning window |
| `0x0001_0000_0000_0003` | FactoryReset | DeviceInfo | Full device reset |
| `0x0001_0000_0000_0004` | ResetTestState | DeviceInfo | Reset test-modified state to defaults |
| `0x0001_0001_0000_XXXX` | AdjustClockBase | DeviceInfo | Offset device clock by N seconds (low 32 bits, signed) |
| `0x0002_0000_0000_0001` | Fault | Status | Trigger a fault condition |
| `0x0002_0000_0000_0002` | ClearFault | Status | Clear a fault condition |
| `0x0004_0000_0000_0001` | SetPower100 | Measurement | Set power to 100 W |
| `0x0004_0000_0000_0002` | SetPower1000 | Measurement | Set power to 1 kW |
| `0x0004_0001_0000_XXXX` | SetPowerCustomBase | Measurement | Set power to N milliwatts (low 32 bits, signed) |
| `0x0005_0000_0000_0001` | ControlStateAutonomous | EnergyControl | Set control state |
| `0x0006_0000_0000_0001` | EVPlugIn | ChargingSession | Simulate EV plug-in |

### Offline Simulation

When the runner is not connected to a device, known triggers (EnterCommissioningMode, ExitCommissioningMode) are simulated by manipulating runner-side state. Unknown triggers return an error.

---

## 8. Multi-Zone Test Setup

Some tests verify behavior with multiple concurrent zones. The runner handles this via special preconditions.

### `two_zones_connected` Precondition

This precondition commissions two zones sequentially (GRID then LOCAL), each on a separate TLS connection:

```
1. Send RemoveZone on existing connection (if any)
2. Disconnect
3. Wait for device to re-enter commissioning mode (~600ms)
4. Commission as GRID zone (PASE + cert exchange)
5. Move GRID connection to zone tracker
6. Wait for device to re-enter commissioning mode (~600ms)
7. Commission as LOCAL zone (PASE + cert exchange)
8. Move LOCAL connection to zone tracker
9. Wait for operational reconnect to settle (~200ms)
```

Each zone's connection is stored in `activeZoneConns` with the zone name as key. The derived zone ID is stored in `activeZoneIDs` for RemoveZone cleanup.

### `device_zones_full` Precondition

Fills all 3 zone slots (GRID + LOCAL + TEST) using the same sequential commission pattern. After filling all slots, the runner waits 800ms for the device to stabilize.

### Zone Connection Cleanup

Between tests, `closeActiveZoneConns()`:
1. Sends RemoveZone invoke on each zone connection
2. Sends ControlClose frame
3. Closes TCP connection
4. Clears PASE state to force re-commission

---

## 9. Writing a Test Case

### YAML Structure

```yaml
id: TC-CATEGORY-NNN
name: Short Descriptive Name
description: |
  Multi-line explanation of what this test validates.

pics_requirements:
  - MASH.S.FEATURE_CODE
  - MASH.S.FEATURE.ATTRIBUTE_OR_COMMAND

preconditions:
  - session_established: true    # Level 3: commissioned
  # OR
  - connection_established: true # Level 2: connected only
  # OR
  - device_in_commissioning_mode: true  # Level 1: clean state

steps:
  - name: Human-readable step description
    action: read | write | subscribe | invoke | connect | disconnect |
            commission | trigger_test_event | wait | verify
    params:
      endpoint: 0          # Endpoint ID (0 = DEVICE_ROOT)
      feature: DeviceInfo  # Human-readable name (resolved to 0x01)
      attribute: deviceId  # For read/write (resolved to attribute ID)
      command: SetLimit    # For invoke (resolved to command ID)
      args: { ... }        # Command arguments
    expect:
      read_success: true
      value: "expected"
      status: SUCCESS

timeout: "10s"
tags:
  - category
  - feature
```

### Common Actions

| Action | Description | Key Parameters |
|--------|-------------|----------------|
| `read` | Read attribute(s) | `endpoint`, `feature`, `attribute` (optional) |
| `write` | Write attribute(s) | `endpoint`, `feature`, `attribute`, `value` |
| `subscribe` | Subscribe to changes | `endpoint`, `feature` |
| `invoke` | Execute a command | `endpoint`, `feature`, `command`, `args` |
| `connect` | Establish TLS connection | `commissioning` (bool), `target`, `client_cert` |
| `disconnect` | Close connection | `graceful` (bool) |
| `commission` | Full PASE + cert exchange | `setup_code`, `zone_type` |
| `trigger_test_event` | TestControl trigger | `event_trigger` (hex or numeric) |
| `wait` | Pause execution | `duration_ms` |
| `receive_notification` | Wait for subscription notification | `timeout` |

### Name Resolution

Human-readable names in YAML are resolved to numeric IDs by the runner's `Resolver`:

| YAML Name | Resolved To |
|-----------|-------------|
| `DeviceInfo` | Feature ID `0x01` |
| `Measurement` | Feature ID `0x04` |
| `EnergyControl` | Feature ID `0x05` |
| `TestControl` | Feature ID `0x0A` |
| `acActivePower` | Attribute ID (feature-specific) |
| `SetLimit` | Command ID `0x01` (on EnergyControl) |
| `triggerTestEvent` | Command ID `0x01` (on TestControl) |

The resolver lowercases all names before lookup, so `DeviceInfo`, `deviceinfo`, and `deviceInfo` all resolve to the same feature ID.

---

## Related Documents

| Document | Scope |
|----------|-------|
| [Testing README](README.md) | Test philosophy, PICS format, test categories |
| [Decision Log (DEC-060)](../decision-log.md) | TEST zone type rationale |
| [Security spec](../security.md) | Commissioning, certificate renewal |
| [Multi-zone spec](../multi-zone.md) | Zone types, limit/setpoint resolution |
| [Interaction model](../interaction-model.md) | 4-operation protocol design |
