# MASH Discovery

> mDNS/DNS-SD discovery and capability introspection

**Status:** Draft
**Last Updated:** 2025-01-27

---

## Related Documents

| Document | Description |
|----------|-------------|
| [Protocol Overview](protocol-overview.md) | Vision, architecture, device model |
| [Security](security.md) | Commissioning, QR codes |
| [Features](features/README.md) | Feature definitions |

---

## 1. Discovery Overview

MASH uses mDNS/DNS-SD for network discovery with three service types:

| Service Type | Purpose | Advertised By |
|--------------|---------|---------------|
| `_mash-comm._tcp` | Commissionable device | Device in commissioning mode |
| `_mash._tcp` | Operational device | Commissioned device |
| `_mashp._udp` | Pairing request | Controller seeking specific device |

After network discovery, controllers use capability discovery to learn device features.

---

## 2. mDNS/DNS-SD Discovery

### 2.1 Service Types

| Service | Protocol | Description |
|---------|----------|-------------|
| `_mash-comm._tcp.local` | TCP | Device ready for commissioning (DEC-067) |
| `_mash._tcp.local` | TCP | Operational device accepting connections |
| `_mashp._udp.local` | UDP | Pairing request for specific device |

**Authoritative port: SRV record.** The port a controller connects to is the value in the SRV record's port field for the relevant service instance — never an assumed default. For TCP services this matters because MASH supports two conformant deployment patterns (DEC-067 single-port and DEC-082 two-port):

- **Single-port deployment (DEC-067).** `_mash-comm._tcp` and `_mash._tcp` both advertise the same SRV port (default 8443); ALPN routing distinguishes the two flows.
- **Two-port deployment (DEC-082).** `_mash-comm._tcp` and `_mash._tcp` advertise *different* SRV ports; each port serves a single ALPN exclusively. The operational port defaults to 8443; the commissioning port has no canonical default — implementations choose a free port and advertise it.

Browsers and conformance tests treat both deployments identically: read the SRV port, dial it. No deployment-shape negotiation is needed.

### 2.2 Commissionable Device (`_mash-comm._tcp`)

When a device has an open commissioning window:

**Service Instance Name:**
```
MASH-<discriminator>._mash-comm._tcp.local
```

**TXT Records (canonical per DEC-078):**

| Key | Type | Required | Max Len | Description |
|-----|------|----------|---------|-------------|
| `D` | uint16 | Yes | 4 | Discriminator (0–4095 decimal) |
| `cat` | string | Yes | 15 | Device categories — comma-separated EEBUS SHIP v1.1.0 numeric codes (see below) |
| `serial` | string | Yes | 32 | Serial number printed on device label |
| `brand` | string | Yes | 32 | Vendor / brand name, human-readable |
| `model` | string | Yes | 32 | Model name, human-readable |
| `DN` | string | No | 32 | Device name — user-configurable friendly name |

**Device categories (`cat` values):**

| Value | Category |
|-------|----------|
| 1 | Grid Connection Point Hub (GCPH) |
| 2 | Energy Management System (EMS) |
| 3 | E-mobility (charging station, wallbox) |
| 4 | HVAC (heat pump, air conditioner) |
| 5 | Inverter (PV, battery, hybrid) |
| 6 | Domestic appliance |
| 7 | Metering (smart meter, sub-meter) |

Multi-category devices use a comma-separated list (e.g., `cat=2,5` for a hybrid EMS + inverter).

**Example (wallbox):**
```
MASH-1234._mash-comm._tcp.local
  SRV 0 0 8443 evse-001.local
  TXT D=1234 cat=3 serial=WB-2024-001234 brand=ChargePoint model="Home Flex" DN="Garage Charger"
```

**Example (hybrid inverter with EMS):**
```
MASH-5678._mash-comm._tcp.local
  SRV 0 0 8443 inverter-001.local
  TXT D=5678 cat=2,5 serial=INV-2024-567890 brand=SolarEdge model="Home Hub"
```

**Note:** No `CM` flag is needed — the presence of `_mash-comm._tcp` itself signals commissioning mode.

### 2.3 Operational Device (`_mash._tcp`)

After commissioning, device updates mDNS:

**Service Instance Name:**
```
<device-id>._mash._tcp.local
```

**TXT Records:**

| Key | Description | Example |
|-----|-------------|---------|
| `DI` | Device ID | `PEN12345.EVSE001` |
| `VP` | Vendor:Product ID | `1234:5678` |
| `FW` | Firmware version | `1.2.3` |
| `EP` | Endpoint count | `2` |
| `FM` | Feature map (hex) | `0x001B` |

**Example:**
```
PEN12345-EVSE001._mash._tcp.local
  SRV 0 0 8443 evse-001.local
  TXT DI=PEN12345.EVSE001 VP=1234:5678 FW=1.2.3 EP=2 FM=0x001B
```

### 2.4 Pairing Request (`_mashp._udp`)

A pairing request allows a controller to signal a specific device to open its commissioning window. This enables deferred commissioning scenarios where the controller receives device credentials (via QR code) but the device is not yet advertising.

**Use Cases:**
- SMGW commissioning: Utility backend provisions SMGW with device credentials days/weeks after installation
- Manual pairing: User selects device by discriminator, controller requests it to open window
- Re-commissioning: Adding a device to an additional zone

**Service Instance Name:**
```
<zone-id>-<discriminator>._mashp._udp.local
```

**TXT Records:**

| Key | Description | Example |
|-----|-------------|---------|
| `D` | Target discriminator (12-bit) | `1234` |
| `ZI` | Requesting zone ID | `A1B2C3D4E5F6A7B8` |
| `ZN` | Zone name (optional, for display) | `Home-EMS` |

**Example:**
```
A1B2C3D4E5F6A7B8-1234._mashp._udp.local
  SRV 0 0 0 controller.local
  TXT D=1234 ZI=A1B2C3D4E5F6A7B8 ZN=Home-EMS
```

Note: The SRV port is set to 0 as no connection is made to this service; it is purely a signaling mechanism.

#### 2.4.1 Device Behavior

Devices SHOULD listen for `_mashp._udp` announcements when:
- Uncommissioned (no zones configured), OR
- Configured to accept additional zones

When a device receives a pairing request matching its discriminator:

1. Open commissioning window (default: 3 hours)
2. Start advertising `_mash-comm._tcp`
3. Accept PASE connections

**Commissioning Window Duration:**
- Default: 3 hours
- Configurable: 1-24 hours (device-specific setting)
- Can also be triggered manually (web UI, button, etc.)

**Rate Limiting:** Device SHOULD ignore duplicate pairing requests while a commissioning window is already open.

#### 2.4.2 Controller Behavior

Controllers announce `_mashp._udp` when they have device credentials but cannot find the device via `_mash-comm._tcp`:

1. Scan QR code or receive credentials from backend
2. Browse `_mash-comm._tcp` for device by discriminator
3. If NOT found: announce `_mashp._udp` with target discriminator
4. Continue announcing until:
   - Device appears on `_mash-comm._tcp`, OR
   - User cancels, OR
   - Timeout (configurable, e.g., 7 days for SMGW, 1 hour for interactive)
5. Once device is found, proceed with normal PASE commissioning

**Announcement Interval:** Controllers SHOULD re-announce every 2 minutes (mDNS TTL).

#### 2.4.3 Commissioning Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                     Commissioning Flow                           │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  Scenario A: Device already advertising                          │
│  ─────────────────────────────────────────                       │
│  1. Controller scans QR → discriminator + passcode               │
│  2. Controller browses _mash-comm._tcp                               │
│  3. Device found → connect, PASE with passcode                   │
│                                                                  │
│  Scenario B: Device not advertising (deferred)                   │
│  ─────────────────────────────────────────────                   │
│  1. Controller receives credentials (QR scan or backend)         │
│  2. Controller browses _mash-comm._tcp → device NOT found            │
│  3. Controller announces _mashp._udp with D=<discriminator>      │
│  4. Device sees pairing request → opens window                   │
│  5. Device advertises _mash-comm._tcp                                │
│  6. Controller discovers device → connect, PASE with passcode    │
│                                                                  │
│  Scenario C: SMGW / Backend-provisioned                          │
│  ─────────────────────────────────────────                       │
│  1. Installer scans QR → data sent to utility backend            │
│  2. [days/weeks pass]                                            │
│  3. Backend provisions SMGW with discriminator + passcode        │
│  4. SMGW announces _mashp._udp                                   │
│  5. Device sees request → opens window → advertises _mash-comm._tcp  │
│  6. SMGW discovers device → connect, PASE with passcode          │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

#### 2.4.4 Security Considerations

The pairing request mechanism does not authenticate the requesting controller. Security is provided by:

1. **PASE authentication:** Only controllers with the correct passcode can complete commissioning
2. **Physical access:** Passcode is obtained from QR code on device (requires physical access)
3. **Window expiration:** Commissioning window closes after timeout
4. **Rate limiting:** Device ignores requests while window is open

This model is equivalent to Matter's commissioning security: opening a window is not a security breach since PASE protects the actual commissioning.

---

## 3. QR Code Format

### 3.1 Content Structure

```
MASH:<version>:<discriminator>:<setupcode>:<vendorid>:<productid>
```

| Field | Bits | Description |
|-------|------|-------------|
| version | 8 | Protocol version (1) |
| discriminator | 12 | Device discriminator |
| setupcode | 27 | 8-digit setup code |
| vendorid | 16 | Vendor ID |
| productid | 16 | Product ID |

### 3.2 Example

```
MASH:1:1234:12345678:0x1234:0x5678
```

### 3.3 Physical Placement

QR code should be:
- Printed on device label
- Visible after installation
- Optionally in packaging/manual
- Scannable by phone camera

### 3.4 Manual Entry Fallback

If QR scanning fails, user can manually enter:
- 8-digit setup code
- Discriminator (if multiple devices)

---

## 4. Capability Discovery

After connecting to a device, controllers discover capabilities through global attributes.

### 4.1 Global Attributes

Every endpoint MUST implement these attributes (reserved IDs 0xFFF0-0xFFFF):

| Attribute | ID | Type | Description |
|-----------|-----|------|-------------|
| `featureMap` | 0xFFFC | bitmap32 | Supported optional features |
| `attributeList` | 0xFFFB | array[uint16] | Implemented attribute IDs |
| `acceptedCommandList` | 0xFFFA | array[uint8] | Accepted command IDs |
| `generatedCommandList` | 0xFFF9 | array[uint8] | Response command IDs |

> **Protocol version** is not a per-feature attribute. It is provided once via `specVersion` in DeviceInfo (endpoint 0) using major.minor format (e.g., "1.0"). See DEC-050.

### 4.2 Reading Global Attributes

```cbor
// Example: Reading global attributes from an EVSE endpoint
{
  0xFFFC: 0x001B,                      // featureMap: CORE|FLEX|EMOB|SIGNALS
  0xFFFB: [1, 2, 3, 10, 11, 14, 20, 21, 60, ...],  // attributeList
  0xFFFA: [1, 2, 5, 6, 10, 11],        // acceptedCommandList
  0xFFF9: [1, 2, 5, 6, 10, 11]         // generatedCommandList
}
```

---

## 5. Feature Map

The `featureMap` is a **32-bit bitmap** indicating which optional feature sets the device supports. This is aligned with Matter's BITMAP32 type.

### 5.1 Feature Map Bits

```
FeatureMapBits:
  bit 0  (0x0001): CORE      - EnergyCore basics (always set for energy devices)
  bit 1  (0x0002): FLEX      - Flexible power adjustment (FlexibilityStruct)
  bit 2  (0x0004): BATTERY   - Battery-specific attributes (SoC, SoH, capacity)
  bit 3  (0x0008): EMOB      - E-Mobility/EVSE (charging sessions, EV state)
  bit 4  (0x0010): SIGNALS   - Incentive signals support
  bit 5  (0x0020): TARIFF    - Tariff data support
  bit 6  (0x0040): PLAN      - Power plan support
  bit 7  (0x0080): PROCESS   - Optional process lifecycle (OHPCF-style)
  bit 8  (0x0100): FORECAST  - Power forecasting capability
  bit 9  (0x0200): ASYMMETRIC - Per-phase asymmetric control
  bit 10 (0x0400): V2X       - Vehicle-to-grid/home (bidirectional EV)
```

### 5.2 Feature-Dependent Attribute Conformance

| Attribute | Mandatory If | Optional If |
|-----------|-------------|-------------|
| `stateOfCharge`, `stateOfHealth`, `batteryCapacity` | BATTERY | - |
| `sessionEnergy`, `evseState`, `connectedVehicle` | EMOB | - |
| `flexibility` | - | FLEX |
| `forecast` | - | FORECAST |
| `processState`, `optionalProcess` | PROCESS | - |
| `effectiveCurrentSetpointsConsumption/Production` | ASYMMETRIC | - |
| Signals feature attributes | SIGNALS | - |
| Tariff feature attributes | TARIFF | - |
| Plan feature attributes | PLAN | - |

### 5.3 Two-Level Capability Discovery

FeatureMap bits indicate **high-level categories**. Detailed capability information is in feature attributes:

| FeatureMap Bit | Quick Check | Details In |
|----------------|-------------|------------|
| EMOB | Has EV charging | ChargingSession: `supportedChargingModes`, `evDemandMode` |
| ASYMMETRIC | Per-phase control | Electrical: `supportsAsymmetric` enum |
| V2X | Bidirectional EV | Electrical: `supportedDirections` enum |
| BATTERY | Has battery | Electrical: `energyCapacity` |

**Why two levels?**
- FeatureMap for quick filtering ("show me all V2X devices")
- Attributes for accurate matching ("can it do asymmetric V2G?")
- Prevents combinatorial explosion of featureMap bits
- Detailed enums are more expressive than boolean bits

**Example - V2H EVSE with asymmetric charging but symmetric discharging:**
```
featureMap: 0x060B (CORE | FLEX | EMOB | ASYMMETRIC | V2X)
  → Quick check: "yes, it does V2X and asymmetric"

Electrical.supportsAsymmetric = CONSUMPTION
  → Detail: "asymmetric only for charging"

Electrical.supportedDirections = BIDIRECTIONAL
  → Detail: "can charge and discharge"
```

---

## 6. Discovery Flow

When a controller connects to a device:

```
1. Read endpoint list (discover device structure)
   → Get: [{id: 0, type: DEVICE_ROOT}, {id: 1, type: EV_CHARGER}, ...]

2. Read specVersion from DeviceInfo (endpoint 0):
   → Protocol version (e.g., "1.0") for compatibility check

3. For each endpoint, read global attributes:
   a. featureMap       → Which feature sets are supported
   b. attributeList    → Which specific attributes are implemented
   c. acceptedCommandList → Which commands can be invoked

4. Based on featureMap, controller knows:
   - If EMOB (0x0008) is set → ChargingSession attributes available
   - If BATTERY (0x0004) is set → Battery attributes available
   - If PROCESS (0x0080) is set → OHPCF-style scheduling available
   - etc.

5. attributeList provides exact attribute IDs for fine-grained discovery
```

---

## 7. Example Configurations

### 7.1 Basic EVSE (V1G, no flexibility)

```cbor
{
  featureMap: 0x0009,        // CORE | EMOB
  attributeList: [1, 2, 3, 10, 11, 14, 20, 21, 0xFFF9, 0xFFFA, 0xFFFB, 0xFFFC],
  acceptedCommandList: [1, 2, 5, 6],     // SetLimit, ClearLimit, SetCurrentLimits, ClearCurrentLimits
  generatedCommandList: [1, 2, 5, 6]
}
```

### 7.2 Advanced V2H EVSE (bidirectional, asymmetric, flexibility)

```cbor
{
  featureMap: 0x060B,        // CORE | FLEX | EMOB | ASYMMETRIC | V2X
  attributeList: [1, 2, 3, 10-16, 20-23, 30-33, 40-43, 50-53, 60, 0xFFF9-0xFFFC],
  acceptedCommandList: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10],  // All limit/setpoint commands + Pause/Resume
  generatedCommandList: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]
}
```

### 7.3 Heat Pump with Optional Process (OHPCF)

```cbor
{
  featureMap: 0x0083,        // CORE | FLEX | PROCESS
  attributeList: [1, 2, 3, 10, 14, 16, 20, 21, 60, 70-72, 80, 81, 0xFFF9-0xFFFC],
  acceptedCommandList: [1, 2, 9, 10, 11, 12, 13],  // SetLimit, ClearLimit, Pause, Resume, Stop, ScheduleProcess, CancelProcess
  generatedCommandList: [1, 2, 9, 10, 11, 12, 13]
}
```

### 7.4 Battery Storage

```cbor
{
  featureMap: 0x0107,        // CORE | FLEX | BATTERY | FORECAST
  attributeList: [1, 2, 3, 10-16, 20-23, 40-43, 50-53, 60, 61, 70-72, 0xFFF9-0xFFFC],
  acceptedCommandList: [1, 2, 3, 4, 7, 8, 9, 10],  // Limits + Setpoints + Pause/Resume
  generatedCommandList: [1, 2, 3, 4, 7, 8, 9, 10]
}
```

---

## 8. attributeList Immutability (DEC-051)

`attributeList` is **immutable for the lifetime of a connection**. It reflects the device's hardware capabilities, not transient runtime state:

- Attributes that are supported but currently have no value report `null`
- Example: a ChargingSession feature on an EVSE always lists `evStateOfCharge` in `attributeList`, even when no EV is plugged in -- the value is simply `null`
- A change in hardware configuration (e.g., modular device reconfiguration) requires the device to close and re-establish the connection

This guarantees that controllers can read `attributeList` once at discovery time and build a stable data model without handling dynamic schema changes.

---

## 9. Endpoint Type Conformance (DEC-053)

Which attributes a device must support depends on its endpoint type. The two-layer conformance model:

| Layer | Defines | Location |
|-------|---------|----------|
| **Feature YAML** | Superset of all possible attributes | `docs/features/<feature>/1.0.yaml` |
| **Endpoint type registry** | Per-type mandatory/recommended attributes | `docs/features/endpoint-conformance.yaml` |

Example: Measurement feature conformance varies by endpoint type:

| Endpoint Type | Mandatory | Recommended |
|---------------|-----------|-------------|
| GRID_CONNECTION | acActivePower | acCurrentPerPhase, acVoltagePerPhase, acFrequency, acEnergyConsumed, acEnergyProduced, powerFactor |
| BATTERY | dcPower, stateOfCharge | dcVoltage, stateOfHealth, temperature, dcEnergyIn, dcEnergyOut |
| PV_STRING | dcPower | dcVoltage, dcCurrent, dcEnergyOut |

See `docs/features/endpoint-conformance.yaml` for the complete registry.

---

## 10. Benefits of This Approach

| Benefit | Explanation |
|---------|-------------|
| **Self-describing** | Controller knows exactly what's available without trial/error |
| **Version-safe** | `specVersion` in DeviceInfo provides protocol version; `attributeList` handles capability discovery (DEC-050) |
| **Fine-grained** | `attributeList` gives exact attribute availability |
| **Compact** | Bitmap `featureMap` is efficient for quick capability checks |
| **Predictable** | No implicit assumptions about what "EVSE" means |
| **Stable** | `attributeList` is immutable per connection (DEC-051) |

---

## 11. Comparison with EEBUS Discovery

| Aspect | EEBUS | MASH |
|--------|-------|------|
| Network discovery | mDNS with SHIP service | mDNS with MASH service |
| Capability discovery | nodeManagementUseCaseData | featureMap + attributeList |
| Granularity | Use case level | Attribute level |
| Version info | Per use case scenario | specVersion in DeviceInfo (major.minor) |
| Required attributes | Inferred from use case | Explicit in attributeList |

**Key improvements:**
- Fine-grained attribute-level discovery
- No need to lookup spec to know what attributes exist
- Clear version negotiation
- Self-describing devices
