# MASH Test Specification

> Test-Driven Specification: Tests and precise behavior defined together

**Status:** Draft
**Created:** 2025-01-25

---

## Philosophy

MASH takes a **test-driven specification** approach:

1. **Structure first** - Device model, features, message formats (done)
2. **PICS + Tests + Precise behavior** - Defined together, iteratively
3. **Implementation** - Against testable specification

The test specification IS the precise specification. If behavior can't be tested, it's not specified.

---

## Directory Structure

```
docs/testing/
├── README.md                    # This file
├── pics/
│   ├── pics-format.md           # PICS code format specification
│   ├── mash-base.pics           # Base protocol PICS
│   └── features/
│       ├── electrical.pics      # Electrical feature PICS
│       ├── measurement.pics     # Measurement feature PICS
│       ├── energy-control.pics  # EnergyControl feature PICS
│       └── ...
├── conformance/
│   ├── conformance-format.md    # Conformance rule format
│   └── features/
│       ├── electrical.xml       # Electrical conformance rules
│       ├── energy-control.xml   # EnergyControl conformance rules
│       └── ...
├── test-cases/
│   ├── format.md                # Test case format specification
│   ├── TC-TRANS-*.md            # Transport layer tests
│   ├── TC-STATE-*.md            # State machine tests
│   ├── TC-ZONE-*.md             # Multi-zone tests
│   ├── TC-CTRL-*.md             # EnergyControl tests
│   └── ...
└── behavior/
    ├── state-machines.md        # Precise state machine definitions
    ├── multi-zone-resolution.md # Precise resolution rules
    ├── timing-requirements.md   # Precise timing specs
    └── error-handling.md        # Precise error semantics
```

---

## Workflow: Adding a Feature Behavior

For each behavior that needs specification:

### Step 1: Identify the Ambiguity

From behavior spec review or new discovery.

Example: "What happens when a zone clears its limit?"

### Step 2: Define PICS Items

What capability variations exist?

```
MASH.S.CTRL.LIMIT_DEFAULT_UNLIMITED=1  # When all limits cleared, device is unlimited
MASH.S.CTRL.LIMIT_DEFAULT_ZERO=1       # When all limits cleared, device uses 0
```

### Step 3: Write Test Cases

```yaml
TC-CTRL-LIMIT-3: Behavior when all zones clear limits

PICS: [MASH.S.CTRL]

Preconditions:
  - Device commissioned to Zone 1 and Zone 2
  - Zone 1 has set consumptionLimit = 5000000 mW
  - Zone 2 has set consumptionLimit = 6000000 mW
  - effectiveConsumptionLimit = 5000000 mW

Steps:
  1. Zone 1 sends ClearLimit()
     Expected: effectiveConsumptionLimit = 6000000 mW (Zone 2's limit)

  2. Zone 2 sends ClearLimit()
     Expected:
       If MASH.S.CTRL.LIMIT_DEFAULT_UNLIMITED:
         effectiveConsumptionLimit = null (no limit)
       If MASH.S.CTRL.LIMIT_DEFAULT_ZERO:
         effectiveConsumptionLimit = 0 (device stops)

  3. Read myConsumptionLimit from Zone 1
     Expected: null (Zone 1 has no active limit)
```

### Step 4: Document Precise Behavior

In `behavior/multi-zone-resolution.md`:

```markdown
## Limit Clearing Behavior

When a zone clears its limit:
1. Device removes that zone's limit from internal tracking
2. Device recalculates effectiveLimit = min(remaining zone limits)
3. If no zone limits remain:
   - Device behavior is implementation-defined (declare via PICS)
   - LIMIT_DEFAULT_UNLIMITED: effectiveLimit = null (unlimited)
   - LIMIT_DEFAULT_ZERO: effectiveLimit = 0 (full stop)
4. Device notifies all subscribed zones of effectiveLimit change
```

### Step 5: Update Feature Spec

Back-port precise wording to `docs/features/energy-control.md`.

---

## PICS Format

### Code Structure

```
MASH.<Side>.<Feature>.<Type><ID>[.<Qualifier>]
```

| Component | Values | Description |
|-----------|--------|-------------|
| Side | S, C | Server (device), Client (controller) |
| Feature | TRANS, ELEC, MEAS, CTRL, STAT, INFO, CHRG, SIG, TAR, PLAN | Feature code |
| Type | F, A, C, E, B | Feature flag, Attribute, Command, Event, Behavior |
| ID | Hex or name | Identifier |
| Qualifier | Rsp, Tx | For commands: accepts, generates |

### Examples

```
# Protocol support
MASH.S=1                        # Device implements MASH protocol
MASH.S.VERSION=1.0              # Protocol version (major.minor)

# Feature presence
MASH.S.CTRL=1                   # EnergyControl feature present
MASH.S.CHRG=1                   # ChargingSession feature present

# Feature flags (from featureMap)
MASH.S.F00=1                    # CORE
MASH.S.F03=1                    # EMOB
MASH.S.F09=1                    # ASYMMETRIC

# Attributes
MASH.S.CTRL.A0001=1             # deviceType
MASH.S.CTRL.A0014=1             # isPausable

# Commands
MASH.S.CTRL.C01.Rsp=1           # SetLimit accepted
MASH.S.CTRL.C09.Rsp=1           # Pause accepted

# Behavior options (for implementation-defined behavior)
MASH.S.CTRL.B_LIMIT_CLEAR=UNLIMITED  # Behavior when all limits cleared
MASH.S.CTRL.B_FAILSAFE_IMMEDIATE=1   # Failsafe applied immediately on disconnect
```

---

## Conformance Format

XML-based, following Matter's approach:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<conformance feature="EnergyControl" id="0x05">

  <!-- Feature flag conformance -->
  <featureFlags>
    <flag bit="0" name="CORE">
      <mandatoryConform/>
    </flag>
    <flag bit="9" name="ASYMMETRIC">
      <optionalConform>
        <condition>
          <attribute feature="Electrical" name="phaseCount">
            <greaterThan value="1"/>
          </attribute>
        </condition>
      </optionalConform>
    </flag>
  </featureFlags>

  <!-- Attribute conformance -->
  <attributes>
    <attribute id="0x0001" name="deviceType">
      <mandatoryConform/>
    </attribute>
    <attribute id="0x001E" name="effectiveCurrentLimitsConsumption">
      <mandatoryConform>
        <feature name="ASYMMETRIC"/>
      </mandatoryConform>
    </attribute>
    <attribute id="0x003C" name="flexibility">
      <optionalConform>
        <feature name="FLEX"/>
      </optionalConform>
    </attribute>
  </attributes>

  <!-- Command conformance -->
  <commands>
    <command id="0x01" name="SetLimit">
      <mandatoryConform>
        <attribute name="acceptsLimits" value="true"/>
      </mandatoryConform>
    </command>
    <command id="0x05" name="SetCurrentLimits">
      <mandatoryConform>
        <attribute name="acceptsCurrentLimits" value="true"/>
      </mandatoryConform>
    </command>
  </commands>

</conformance>
```

---

## Test Case Format

YAML-based for readability and tooling:

```yaml
id: TC-CTRL-LIMIT-1
name: SetLimit Basic Operation
description: Verify basic SetLimit command execution

pics:
  required:
    - MASH.S.CTRL
    - MASH.S.CTRL.A000A  # acceptsLimits = true

preconditions:
  - Device commissioned to test controller
  - No active limits

steps:
  - id: 1
    action: "Controller sends SetLimit(consumptionLimit: 5000000, cause: LOCAL_PROTECTION)"
    expected:
      - "Device returns success=true"
      - "effectiveConsumptionLimit = 5000000"
    verification: ReadAttribute(effectiveConsumptionLimit) == 5000000

  - id: 2
    action: "Controller reads myConsumptionLimit"
    expected:
      - "myConsumptionLimit = 5000000"

  - id: 3
    action: "Controller reads controlState"
    expected:
      - "controlState = LIMITED"

postconditions:
  - "Device is in LIMITED state with 5kW consumption limit"
```

---

## Test Categories

### TC-TRANS-*: Transport Layer
- Connection establishment
- Keep-alive behavior
- Reconnection
- Message framing

### TC-STATE-*: State Machines
- ControlState transitions
- ProcessState transitions
- State interaction (orthogonality)

### TC-ZONE-*: Multi-Zone
- Limit resolution
- Setpoint resolution
- Zone commissioning/removal
- Priority handling

### TC-CTRL-*: EnergyControl
- Limit commands
- Setpoint commands
- Pause/Resume/Stop
- Process scheduling

### TC-SUB-*: Subscriptions
- Subscription establishment
- Change notifications
- Interval behavior
- Reconnection re-establishment

### TC-DISC-*: Discovery
- mDNS advertising
- Capability discovery
- QR code parsing

### TC-SEC-*: Security
- Commissioning (SPAKE2+)
- Certificate handling
- Zone isolation

### TC-BIDIR-*: Bidirectional Communication
- Controller exposing features to devices
- Device reading from controller
- Device subscribing to controller
- Bidirectional notification delivery
- Controller without exposed features (StatusUnsupported)

### TC-CONF-*: Conformance
- PICS validation
- Feature flag consistency
- Attribute presence

---

## Iteration Plan

### Phase 1: Foundation
1. PICS format specification
2. Conformance XML schema
3. Test case YAML schema
4. Basic tooling (PICS parser, conformance checker)

### Phase 2: Core Features
For each feature, in order:
1. EnergyControl (most complex, most gaps)
2. Electrical
3. Measurement
4. Status
5. DeviceInfo

### Phase 3: Advanced Features
1. ChargingSession
2. Signals
3. Tariff
4. Plan

### Phase 4: Protocol Behavior
1. Transport layer
2. State machines
3. Multi-zone resolution
4. Error handling

---

## Tooling Requirements

### PICS Tools
- `pics-parse`: Parse PICS file to structured format
- `pics-validate`: Validate PICS against conformance rules
- `pics-generate`: Auto-generate PICS from device capabilities

### Test Tools
- `test-runner`: Execute test cases against device
- `test-report`: Generate test results report
- `test-coverage`: Analyze PICS coverage

### Conformance Tools
- `conformance-check`: Validate device against conformance rules
- `conformance-report`: Generate conformance report

---

## Current Status

### Completed Behavior Specifications

| Document | Description | Status |
|----------|-------------|--------|
| `behavior/multi-zone-resolution.md` | Limit/setpoint resolution, duration semantics | Complete |
| `behavior/state-machines.md` | ControlState/ProcessState transitions, interactions | Complete |
| `behavior/connection-state-machine.md` | Transport layer connection lifecycle, keep-alive, reconnection | Complete |
| `behavior/message-framing.md` | Wire-level encoding, CBOR rules, size limits, compatibility | Complete |
| `behavior/commissioning-pase.md` | SPAKE2+ commissioning, certificate flow, admin delegation | Complete |
| `behavior/discovery.md` | mDNS/DNS-SD, QR code format, discriminator handling | Complete |
| `behavior/zone-lifecycle.md` | Zone creation, device add/remove, cert renewal, revocation | Complete |
| `behavior/connection-establishment.md` | mDNS records, TLS handshake, PASE→operational transition, cert validation | Complete |
| `behavior/tls-profile.md` | TLS 1.3 cipher suites, extensions, key exchange, alerts, session handling | Complete |
| `behavior/zone-management.md` | Zone operations, priority, connection tracking, capacity enforcement | Complete |
| `behavior/electrical-feature.md` | Direction capability, phase config, power ratings, asymmetric support | Complete |
| `behavior/measurement-feature.md` | Sign convention, nullable attrs, helper methods, cumulative energy | Complete |
| `behavior/energycontrol-feature.md` | Command handlers, effective vs own values, OptOut states | Complete |
| `behavior/chargingsession-feature.md` | Session lifecycle, EV demand modes, V2G constraints, CanDischarge | Complete |
| `behavior/status-feature.md` | Operating state transitions, fault management, helper methods | Complete |
| `behavior/protocol-behaviors.md` | Request/response correlation, subscription priming, notifications | Complete |

### Completed Test Cases

| Test Suite | Count | Description |
|------------|-------|-------------|
| TC-ZONE-LIMIT | 13 | Limit resolution across multiple zones |
| TC-SUB | 12 | Subscription priming, deltas, heartbeats |
| TC-STATE | 15 | ControlState transitions and failsafe |
| TC-PROCESS | 17 | ProcessState transitions and commands |
| TC-INTERACTION | 15 | ControlState + ProcessState interactions |
| TC-CONN | 5 | Connection establishment |
| TC-KEEPALIVE | 4 | Keep-alive ping/pong behavior |
| TC-RECONN | 4 | Reconnection with backoff |
| TC-CLOSE | 4 | Graceful close handshake |
| TC-MULTI | 4 | Multi-connection handling |
| TC-FRAME | 6 | Frame parsing |
| TC-CBOR | 7 | CBOR encoding/parsing |
| TC-NULL | 4 | Null vs absent handling |
| TC-INT | 4 | Integer range handling |
| TC-COMPAT | 3 | Forward compatibility |
| TC-PASE | 5 | SPAKE2+ protocol |
| TC-COMM | 5 | Commissioning flow |
| TC-ZONE-COMM | 5 | Multi-zone commissioning |
| TC-ADMIN | 4 | Admin delegation |
| TC-CERT | 4 | Certificate handling |
| TC-MASHC | 6 | Commissionable discovery (`_mash-comm._tcp`) |
| TC-MASHO | 6 | Operational discovery (`_mash._tcp`) |
| TC-QR | 6 | QR code parsing |
| TC-DISC | 4 | Discriminator handling |
| TC-DSTATE | 6 | Discovery state transitions |
| TC-BROWSE | 6 | Service browsing |
| TC-ZONE-CREATE | 3 | Zone creation |
| TC-ZONE-ADD | 5 | Adding devices to zone |
| TC-CERT-RENEW | 5 | Certificate renewal |
| TC-ZONE-REMOVE | 5 | Removing devices from zone |
| TC-D2D | 3 | Device-to-device verification |
| TC-QR-GEN | 4 | QR code generation |
| TC-MULTIIF | 10 | Multi-interface behavior |
| TC-SUB-RESTORE | 5 | Subscription restoration after reconnection |
| TC-INFLIGHT | 5 | In-flight command handling on connection loss |
| TC-NOTFOUND | 6 | Device not found error handling |
| TC-TLS-VERSION | 3 | TLS version negotiation |
| TC-TLS-CIPHER | 4 | Cipher suite selection |
| TC-TLS-KX | 4 | Key exchange groups |
| TC-TLS-ALPN | 3 | ALPN protocol negotiation |
| TC-TLS-CHAIN | 4 | Certificate chain validation |
| TC-TLS-RESUME | 3 | Session resumption (prohibited) |
| TC-TLS-ALERT | 4 | TLS alert handling |
| TC-BIDIR | 4 | Bidirectional communication |
| TC-STATE | 15 | ControlState/ProcessState transitions |
| TC-ZONE-LIMIT | 13 | Multi-zone limit resolution |
| TC-SUB | 12 | Subscription priming, deltas, heartbeats |
| TC-ELEC | 8 | Electrical feature capabilities |
| TC-MEAS | 10 | Measurement feature and sign convention |
| TC-CTRL | 12 | EnergyControl commands and behaviors |
| TC-CHRG | 10 | ChargingSession lifecycle and V2G |
| TC-STAT | 6 | Status feature and fault management |
| TC-CONN | 5 | Connection establishment |
| TC-ERR | 6 | Error handling |

---

## Next Steps

1. **Implement test harness** - Execute YAML test cases against devices
2. **Build PICS tooling** - Parser and validator for PICS files
3. **Create conformance XML** - Formal attribute/command conformance rules
4. **Implement remaining features** - Tariff, Signals, Plan (code implementation)

### Summary Statistics

| Category | Count |
|----------|-------|
| Behavior specifications | 16 documents |
| Runnable test cases | 110+ tests |
| PICS feature files | 6 files |
| Test case YAML files | 23 files |
