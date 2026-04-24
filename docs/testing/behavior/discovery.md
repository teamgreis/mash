# Discovery Behavior

> Precise specification of mDNS/DNS-SD and QR code discovery

**Status:** Draft
**Created:** 2025-01-25
**Updated:** 2025-01-25

---

## 1. Overview

MASH discovery uses **two mDNS service types** plus a pairing-request signaling service (following the Matter model):

1. **Commissionable Discovery (`_mash-comm._tcp`)** - Find devices ready for pairing
2. **Operational Discovery (`_mash._tcp`)** - Find commissioned devices in zones
3. **Pairing Request (`_mashp._udp`)** - Controller signals a specific device to open its commissioning window (see `discovery.md §2.4`)

**Key principles:**
- DNS-SD compliant (RFC 6763)
- Separate namespaces for commissioning vs operational
- Minimal TXT record size for constrained devices
- QR code contains all commissioning information

---

## 2. Service Types

### 2.1 Commissionable Discovery (`_mash-comm._tcp`)

**Purpose:** Discover devices that are ready to be commissioned (paired).

```
Service Type:  _mash-comm._tcp
Domain:        local
Port:          8443 (UDP for PASE, then TCP for operational)
```

**When advertised:**
- Device is in commissioning mode (button pressed, window open)
- Removed when commissioning window closes (timeout or success)

**Instance name:** `MASH-<discriminator>`

**Example:**
```
_mash-comm._tcp.local.            PTR   MASH-1234._mash-comm._tcp.local.
MASH-1234._mash-comm._tcp.local.  SRV   0 0 8443 evse-001.local.
MASH-1234._mash-comm._tcp.local.  TXT   "D=1234" "cat=3" "serial=WB-2024-001234" "brand=ChargePoint" "model=Home Flex"
evse-001.local.               AAAA  fe80::1234:5678:9abc:def0
```

### 2.2 Operational Discovery (`_mash._tcp`)

**Purpose:** Discover commissioned devices for operational communication.

```
Service Type:  _mash._tcp
Domain:        local
Port:          8443 (TCP/TLS WebSocket)
```

**When advertised:**
- Device has at least one zone membership
- One service instance per zone membership (device in 2 zones = 2 instances)

**Instance name:** `<zone-id>-<device-id>`

Where:
- `zone-id`: First 16 hex chars (64 bits) of SHA-256(Zone CA certificate DER)
- `device-id`: Extracted from operational cert's CommonName (assigned by controller during commissioning)

**Device ID derivation (by controller):** Controller computes `hex(SHA-256(CSR public key DER)[0:8])` and embeds it in the certificate's CommonName during signing. Device extracts this value from the received certificate.

**Note:** Device ID is zone-specific. Same physical device in two zones has different device IDs (different operational certs per zone). This is intentional - the device has a distinct identity in each zone.

**Example (device in two zones):**
```
_mash._tcp.local.                                                 PTR   A1B2C3D4E5F6A7B8-F9E8D7C6B5A49382._mash._tcp.local.
_mash._tcp.local.                                                 PTR   1234567890ABCDEF-FEDCBA0987654321._mash._tcp.local.
A1B2C3D4E5F6A7B8-F9E8D7C6B5A49382._mash._tcp.local.               SRV   0 0 8443 evse-001.local.
A1B2C3D4E5F6A7B8-F9E8D7C6B5A49382._mash._tcp.local.               TXT   "ZI=A1B2C3D4E5F6A7B8" "DI=F9E8D7C6B5A49382"
1234567890ABCDEF-FEDCBA0987654321._mash._tcp.local.               SRV   0 0 8443 evse-001.local.
1234567890ABCDEF-FEDCBA0987654321._mash._tcp.local.               TXT   "ZI=1234567890ABCDEF" "DI=FEDCBA0987654321"
```

### 2.3 Service Type Summary

| Service Type | Purpose | When Present | Instance Name |
|--------------|---------|--------------|---------------|
| `_mash-comm._tcp` | Find devices to commission | Commissioning window open | `MASH-<discriminator>` |
| `_mash._tcp` | Find operational devices | Device has zone(s) | `<zone-id>-<device-id>` |
| `_mashp._udp` | Pairing request signal | Controller seeks specific device | `<zone-id>-<discriminator>` |

### 2.4 Instance Naming Constraints

**All instance names:**
- Maximum length: 63 characters (DNS label limit)
- Allowed characters: A-Z, a-z, 0-9, hyphen (-), space (for zone names)
- Must not start or end with hyphen
- Case-insensitive comparison

**ID derivation:**
```
Zone ID   = hex(SHA-256(Zone CA certificate DER)[0:8])       // 16 hex chars, 64 bits
Device ID = hex(SHA-256(device op cert public key DER)[0:8]) // 16 hex chars, 64 bits
```

**Benefits of fingerprint-derived IDs:**
- No vendor registration required (works for open source)
- No ID assignment/coordination needed
- Cryptographically bound to certificates
- Device can compute its own ID
- Verifiable by anyone with the certificate
- 64 bits provides negligible collision probability

---

## 3. TXT Record Specification

### 3.1 TXT Record Format

TXT records follow RFC 6763 key=value format:

```
key=value
```

**Encoding rules:**
- Keys: ASCII only, case-insensitive, 1-9 characters
- Values: UTF-8, maximum 200 bytes per value
- Total TXT record: Maximum 400 bytes (leaves headroom for DNS overhead)

### 3.2 Commissionable TXT Records (`_mash-comm._tcp`)

| Key | Type | Required | Max Len | Description |
|-----|------|----------|---------|-------------|
| `D` | uint16 | Yes | 4 | Discriminator (0-4095 as decimal) |
| `cat` | string | Yes | 15 | Device categories (comma-separated list, e.g., "2,5") |
| `serial` | string | Yes | 32 | Serial number (printed on device label) |
| `brand` | string | Yes | 32 | Vendor/brand name (human-readable) |
| `model` | string | Yes | 32 | Model name (human-readable) |
| `DN` | string | No | 32 | Device name (user-configurable friendly name) |

**Device Categories (cat):**

| Value | Category | Examples |
|-------|----------|----------|
| 1 | Grid Connection Point Hub (GCPH) | Control unit from public grid operator |
| 2 | Energy Management System (EMS) | Home energy manager, building EMS |
| 3 | E-mobility | Charging station, wallbox |
| 4 | HVAC | Heat pump, air conditioner |
| 5 | Inverter | PV inverter, battery inverter, hybrid |
| 6 | Domestic appliance | Washing machine, dryer, fridge, dishwasher |
| 7 | Metering | Smart meter, sub-meter |

**Note:** Category numbers align with EEBUS "SHIP Requirements for Installation Process" v1.1.0. Numbers are stable - new categories will be added at the end, existing numbers will not change.

**Zone controllers:** Category 2 (EMS) devices are typically zone controllers - they create zones, commission other devices, and coordinate energy management. An EMS browses `_mash-comm._tcp` to find devices to commission; it does not itself advertise an operational mDNS service (commissioning and operational TLS are always controller-initiated). Category 1 (GCPH) may also act as a zone controller in grid operator scenarios.

**Multiple categories:** A device may belong to multiple categories. For example, a hybrid inverter with integrated EMS functionality uses `cat=2,5`. If functionality changes (e.g., user deactivates EMS because a separate EMS is used), the device updates its `cat` value dynamically.

**Example (wallbox):**
```
D=1234
cat=3
serial=WB-2024-001234
brand=ChargePoint
model=Home Flex
DN=Garage Charger
```

**Example (hybrid inverter with EMS):**
```
D=5678
cat=2,5
serial=INV-2024-567890
brand=SolarEdge
model=Home Hub
```

**Total size:** ~120 bytes typical, 180 bytes maximum

**Note:** No `CM` flag needed - presence of `_mash-comm._tcp` service indicates commissioning mode.

**UI pairing flow:** When QR scanning is unavailable, the controller browses `_mash-comm._tcp`, filters by `cat` to show compatible devices, and displays `serial`, `brand`, `model` to help the user identify the physical device (e.g., by matching serial number on label).

### 3.3 Operational TXT Records (`_mash._tcp`)

| Key | Type | Required | Max Len | Description |
|-----|------|----------|---------|-------------|
| `ZI` | string | Yes | 16 | Zone ID (first 64 bits of SHA-256(Zone CA cert DER)) |
| `DI` | string | Yes | 16 | Device ID (first 64 bits of SHA-256(device op cert pubkey DER)) |
| `VP` | string | No | 11 | Vendor:Product ID (optional, for debugging) |
| `FW` | string | No | 20 | Firmware version (semver) |
| `EP` | uint8 | No | 3 | Endpoint count |
| `FM` | string | No | 10 | Feature map (hex, e.g., "0x001B") |

**Example:**
```
ZI=A1B2C3D4E5F6A7B8
DI=F9E8D7C6B5A49382
FW=1.2.3
```

**Total size:** ~70 bytes typical, 120 bytes maximum

**Note:** Both ZI and DI are fingerprint-derived. VP is optional (useful for debugging but not required for open source implementations).

### 3.4 TXT Record Update Rules

**When to update mDNS:**

| Event | Service | Action |
|-------|---------|--------|
| Enter commissioning mode | `_mash-comm._tcp` | Register service |
| Exit commissioning mode (timeout) | `_mash-comm._tcp` | Deregister service |
| Commissioning complete | `_mash-comm._tcp` | Deregister service |
| Commissioning complete | `_mash._tcp` | Register new zone instance |
| Zone added | `_mash._tcp` | Register additional instance |
| Zone removed | `_mash._tcp` | Deregister that instance |
| All zones removed | `_mash._tcp` | Deregister all instances |
| Firmware update | `_mash._tcp` | Update FW in all instances |

**Update timing:**
- mDNS change within 1 second of state change
- mDNS goodbye (TTL=0) before removing old record
- mDNS announcement (3 times) for new record

### 3.5 Character Encoding

| Field | Encoding | Allowed Characters |
|-------|----------|--------------------|
| Keys | ASCII | a-z, A-Z, 0-9 |
| Discriminator (D) | Decimal | 0-9 |
| Categories (cat) | ASCII | 0-9, comma |
| Zone ID (ZI) | Hex | 0-9, A-F |
| Device ID (DI) | Hex | 0-9, A-F |
| Serial number | ASCII | A-Z, a-z, 0-9, hyphen |
| Brand/Model/Device name | UTF-8 | Any Unicode (avoid control chars) |
| Zone name (ZN) | UTF-8 | Any Unicode (avoid control chars) |
| Firmware (FW) | ASCII | 0-9, period, hyphen |
| Feature Map (FM) | Hex | 0-9, a-f, A-F, x |

---

## 4. QR Code Specification

### 4.1 Content Format

```
MASH:<version>:<discriminator>:<setupcode>
```

| Field | Format | Range | Example |
|-------|--------|-------|---------|
| version | Decimal | 1-255 | `1` |
| discriminator | Decimal | 0-4095 | `1234` |
| setupcode | Decimal | 00000000-99999999 | `12345678` |

**Complete example:**
```
MASH:1:1234:12345678
```

**Design rationale:** The QR code contains only the minimum needed for commissioning:
- **discriminator**: Find the device via mDNS (`MASH-<D>._mash-comm._tcp`)
- **setupcode**: Authenticate via SPAKE2+

Device identification (brand, model, serial) is available in the mDNS TXT records, eliminating the need for vendor/product IDs in the QR code. This:
- Simplifies the QR code (smaller, easier to scan)
- Removes dependency on vendor ID registration
- Works for open-source implementations

### 4.2 Numeric Formatting Rules

| Field | Leading Zeros | Prefix |
|-------|---------------|--------|
| version | No | None |
| discriminator | No | None |
| setupcode | **Yes** (8 digits) | None |

**Correct:**
```
MASH:1:1234:00001234   // setupcode has leading zeros
MASH:1:0:99999999      // discriminator 0 without leading zeros
```

**Incorrect:**
```
MASH:01:1234:12345678  // version has leading zero
MASH:1:1234:1234       // setupcode missing leading zeros (must be 8 digits)
```

### 4.3 QR Code Parameters

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| Error correction | M (15%) | Balance size vs readability |
| Minimum version | 2 | Fits typical content |
| Maximum version | 6 | Keeps code scannable |
| Encoding mode | Alphanumeric | Most efficient for MASH format |

**Recommended size:**
- Minimum: 20mm x 20mm (phone scan at 30cm)
- Recommended: 30mm x 30mm (reliable scanning)

### 4.4 QR Code Generation

The QR code encodes the string directly. No binary encoding.

**Content length:** 18-22 characters typical
- `MASH:` = 5
- Version + colon = 2-4
- Discriminator + colon = 2-5
- Setup code = 8

### 4.5 Parsing Algorithm

```python
def parse_qr(content: str) -> dict:
    """Parse MASH QR code content."""
    if not content.startswith("MASH:"):
        raise ValueError("Invalid prefix")

    parts = content.split(":")
    if len(parts) != 4:
        raise ValueError("Invalid field count")

    _, version, discriminator, setupcode = parts

    # Validate version
    version = int(version)
    if version < 1 or version > 255:
        raise ValueError("Version out of range")

    # Validate discriminator
    discriminator = int(discriminator)
    if discriminator < 0 or discriminator > 4095:
        raise ValueError("Discriminator out of range")

    # Validate setup code (must be 8 digits)
    if len(setupcode) != 8 or not setupcode.isdigit():
        raise ValueError("Invalid setup code format")

    return {
        "version": version,
        "discriminator": discriminator,
        "setupcode": setupcode  # Keep as string (preserve leading zeros)
    }
```

---

## 5. Discriminator Handling

### 5.1 Purpose

The discriminator helps filter multiple devices during discovery:
- 12-bit value (0-4095)
- Encoded in QR code and mDNS TXT record
- Used to match QR scan to discovered device

### 5.2 Assignment

| Method | Description |
|--------|-------------|
| Random | Generated at manufacturing (recommended) |
| Serial-derived | Hash of serial number mod 4096 |
| Configurable | User-configurable (less common) |

### 5.3 Collision Handling

If multiple devices have the same discriminator:
1. Controller discovers multiple devices with matching D value
2. Controller attempts commissioning with each
3. SPAKE2+ verification fails for wrong device (wrong setup code)
4. Controller retries with next matching device

**Probability:** With random assignment, ~0.02% collision chance per additional device.

### 5.4 Discriminator in Instance Name

Pre-commissioning instance name includes discriminator:

```
MASH-<discriminator>._mash-comm._tcp.local
```

This allows DNS-SD PTR query filtering:
```
; Query for specific discriminator
MASH-1234._mash-comm._tcp.local
```

### 5.5 UI Fallback Flow (Without QR Code)

When QR code scanning is not available (no camera, QR damaged, remote pairing), the controller uses a UI-based pairing flow:

**Step 1: Browse for commissionable devices**
```
Controller browses _mash-comm._tcp.local
```

**Step 2: Filter by device category**
```
User selects category filter (e.g., "E-mobility" = cat=3)
Controller filters results where cat contains "3"
```

**Step 3: Display device list**
```
┌─────────────────────────────────────────────────────┐
│  Available Devices (E-mobility)                      │
├─────────────────────────────────────────────────────┤
│  ○ ChargePoint Home Flex                            │
│    Serial: WB-2024-001234                           │
│    Discriminator: 1234                              │
│                                                     │
│  ○ ChargePoint Home Flex                            │
│    Serial: WB-2024-005678                           │
│    Discriminator: 5678                              │
│                                                     │
│  ○ Wallbox Pulsar Plus                              │
│    Serial: PLS-2024-999888                          │
│    Discriminator: 9988                              │
└─────────────────────────────────────────────────────┘
```

User identifies physical device by matching serial number (printed on device label).

**Step 4: User selects device and enters setup code**
```
┌─────────────────────────────────────────────────────┐
│  Pair: ChargePoint Home Flex                         │
│  Serial: WB-2024-001234                              │
│                                                     │
│  Enter 8-digit setup code from device label:        │
│  ┌───┬───┬───┬───┬───┬───┬───┬───┐                  │
│  │ 1 │ 2 │ 3 │ 4 │ 5 │ 6 │ 7 │ 8 │                  │
│  └───┴───┴───┴───┴───┴───┴───┴───┘                  │
│                                                     │
│  [Cancel]                          [Pair]           │
└─────────────────────────────────────────────────────┘
```

**Step 5: Commission using discriminator + setup code**
```
Controller has:
  - discriminator (from mDNS: D=1234)
  - setupcode (from user input: "12345678")

Proceeds with normal commissioning flow (PASE, CSR, etc.)
```

**Complete UI fallback sequence:**

```
Controller                                              Device
    │                                                      │
    │─── Browse _mash-comm._tcp.local ────────────────────────►│
    │                                                      │
    │◄── PTR: MASH-1234._mash-comm._tcp.local ─────────────────│
    │◄── TXT: D=1234 cat=3 serial=WB-001234                │
    │        brand=ChargePoint model=Home Flex             │
    │                                                      │
    │  [Display to user: brand, model, serial]             │
    │  [User matches serial to physical device label]      │
    │  [User selects device]                               │
    │  [User enters setup code from device label]          │
    │                                                      │
    │═══ TCP Connect (from SRV record) ═══════════════════►│
    │                                                      │
    │─── TLS + PASE (with user-entered setup code) ───────►│
    │                                                      │
    │    ... normal commissioning continues ...            │
```

**Security consideration:** The setup code is the only secret. The mDNS TXT records (serial, brand, model) are public information that help identify the device but don't grant pairing access.

---

## 6. Discovery State Machine

### 6.1 Device States

```
┌────────────────────────────────────────────────────────────────────────────┐
│                        Discovery State Machine                              │
├────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│   ┌─────────────────┐                                                       │
│   │ UNREGISTERED    │  No mDNS services (powered off)                       │
│   └────────┬────────┘                                                       │
│            │ Power on                                                       │
│            ▼                                                                │
│   ┌─────────────────┐                                                       │
│   │ UNCOMMISSIONED  │  No mDNS services                                     │
│   │                 │  (Waiting for user action)                            │
│   └────────┬────────┘                                                       │
│            │ Button press                                                   │
│            ▼                                                                │
│   ┌─────────────────┐                                                       │
│   │ COMMISSIONING   │  _mash-comm._tcp: MASH-<D>                                │
│   │  OPEN (120s)    │  (Ready for pairing)                                  │
│   └────────┬────────┘                                                       │
│            │                                                                │
│       ┌────┴────┐                                                           │
│       │         │                                                           │
│       ▼         ▼                                                           │
│   Timeout    Success                                                        │
│       │         │                                                           │
│       ▼         ▼                                                           │
│   ┌─────────────────┐   ┌─────────────────────────────────────────┐         │
│   │ UNCOMMISSIONED  │   │            OPERATIONAL                   │         │
│   │ (no services)   │   │  _mash._tcp: <zone-id>-<device-id>      │         │
│   └─────────────────┘   │  (one instance per zone membership)      │         │
│                         └────────────────┬────────────────────────┘         │
│                                          │                                  │
│                                          │ Button press (add zone)          │
│                                          ▼                                  │
│                         ┌─────────────────────────────────────────┐         │
│                         │   OPERATIONAL + COMMISSIONING           │         │
│                         │  _mash._tcp: existing zones             │         │
│                         │  _mash-comm._tcp: MASH-<D> (for new zone)   │         │
│                         └─────────────────────────────────────────┘         │
│                                                                             │
└────────────────────────────────────────────────────────────────────────────┘
```

### 6.2 Transition Rules

```
UNREGISTERED → UNCOMMISSIONED  : Power on (no zones)
UNREGISTERED → OPERATIONAL     : Power on (has zones)

UNCOMMISSIONED → COMMISSIONING_OPEN : Button press / API trigger
UNCOMMISSIONED → UNREGISTERED       : Power off

COMMISSIONING_OPEN → UNCOMMISSIONED : 120s timeout (no zones)
COMMISSIONING_OPEN → OPERATIONAL    : Commissioning success (first zone)
COMMISSIONING_OPEN → UNREGISTERED   : Power off

OPERATIONAL → OPERATIONAL+COMMISSIONING : Button press (add zone)
OPERATIONAL → UNCOMMISSIONED            : All zones removed
OPERATIONAL → UNREGISTERED              : Power off

OPERATIONAL+COMMISSIONING → OPERATIONAL : Timeout or success
```

### 6.3 mDNS Updates per State

| Transition | `_mash-comm._tcp` | `_mash._tcp` |
|------------|---------------|--------------|
| → UNCOMMISSIONED | - | - |
| → COMMISSIONING_OPEN | Register MASH-D | - |
| COMMISSIONING_OPEN timeout | Deregister | - |
| COMMISSIONING_OPEN success | Deregister | Register zone-device instance |
| → OPERATIONAL+COMMISSIONING | Register MASH-D | Keep existing |
| Add zone (operational) | - | Add new instance |
| Remove zone (operational) | - | Remove that instance |
| → UNREGISTERED | Deregister | Deregister all |

### 6.4 Zone Controller Discovery Presence

Zone controllers do not advertise a dedicated mDNS service. All commissioning and operational connections are controller-initiated (see DEC-081). A controller that browses `_mash-comm._tcp` to find commissionable devices is itself invisible on mDNS; its presence on the network is inferred only by devices that successfully complete PASE and are then connected to from that controller.

**Pairing-request signaling:** Where a controller holds device credentials (QR-derived) but the target device is not yet advertising, the controller announces `_mashp._udp` to trigger the device's commissioning window. This is the only controller-originated mDNS traffic. See `docs/discovery.md §2.4`.

---

## 7. Error Handling

### 7.1 mDNS Errors

| Error | Cause | Recovery |
|-------|-------|----------|
| Name conflict | Another device using same instance name | Append suffix (-2, -3, etc.) |
| Network unavailable | No link-local address | Retry every 5 seconds |
| mDNS responder failure | System mDNS crashed | Restart mDNS responder |

### 7.2 QR Code Errors

| Error | Cause | User Action |
|-------|-------|-------------|
| Invalid prefix | Not a MASH QR code | Scan correct QR code |
| Parse error | Malformed content | Report to manufacturer |
| Wrong version | Incompatible protocol | Update app or device |
| Discriminator not found | No device with D value | Ensure device in commissioning mode |

### 7.3 Discovery Timeouts

| Operation | Timeout | Action on Timeout |
|-----------|---------|-------------------|
| mDNS browse | 10 seconds | Report "No devices found" |
| Discriminator match | 30 seconds | Report "Device not found" |
| Commission attempt | 60 seconds | Report "Commissioning timeout" |

---

## 8. Privacy Considerations

### 8.1 Pre-Commissioning

**Minimal information exposure:**
- Discriminator: Essentially random, reveals nothing
- Vendor/Product: Type of device (acceptable for discovery)
- No personal information in mDNS records

### 8.2 Operational

**Information in `_mash._tcp`:**
- Zone ID: Partial fingerprint of Zone CA (reveals zone membership)
- Device ID: Unique but not personally identifiable
- Multiple instances reveal multi-zone membership

**Mitigation options:**
- Use randomized device ID (not serial number)
- Zone ID is truncated (8 chars) - reduces fingerprinting

### 8.3 QR Code Security

**Protect the QR code:**
- Contains setup code (equivalent to password)
- Should not be photographed or shared
- Physical access = commissioning capability

---

## 9. PICS Items

```
# Service types
MASH.S.DISC.SVC_COMMISSION=_mash-comm._tcp  # Commissionable discovery service
MASH.S.DISC.SVC_OPERATIONAL=_mash._tcp  # Operational discovery service
MASH.S.DISC.SVC_PAIRING=_mashp._udp         # Pairing-request signaling service

# General mDNS
MASH.S.DISC.INSTANCE_MAX_LEN=63         # Maximum instance name length
MASH.S.DISC.TXT_MAX_LEN=400             # Maximum TXT record size

# Commissionable (_mash-comm._tcp)
MASH.S.DISC.COMM_TXT_D=1                # Discriminator in TXT
MASH.S.DISC.COMM_TXT_CAT=1              # Device categories in TXT (comma-separated)
MASH.S.DISC.COMM_TXT_SERIAL=1           # Serial number in TXT
MASH.S.DISC.COMM_TXT_BRAND=1            # Brand name in TXT
MASH.S.DISC.COMM_TXT_MODEL=1            # Model name in TXT
MASH.S.DISC.COMM_TXT_DN=1               # Device name in TXT (optional)

# Operational (_mash._tcp)
MASH.S.DISC.OPER_TXT_ZI=1               # Zone ID in TXT
MASH.S.DISC.OPER_TXT_DI=1               # Device ID in TXT
MASH.S.DISC.OPER_TXT_VP=1               # Vendor:Product in TXT
MASH.S.DISC.OPER_TXT_FW=1               # Firmware version in TXT (optional)
MASH.S.DISC.OPER_TXT_FM=1               # Feature map in TXT (optional)

# QR code
MASH.S.DISC.QR_VERSION=1                # QR code version
MASH.S.DISC.QR_ERROR_CORRECTION=M       # Error correction level
MASH.S.DISC.DISCRIMINATOR_BITS=12       # Discriminator size

# Timing
MASH.S.DISC.MDNS_UPDATE_DELAY=1         # Max delay for mDNS update (seconds)
MASH.S.DISC.BROWSE_TIMEOUT=10           # Browse timeout (seconds)
MASH.S.DISC.COMMISSION_WINDOW=120       # Commissioning window timeout (seconds)
```

---

## 10. Test Cases

### TC-MASHC-*: Commissionable Discovery (`_mash-comm._tcp`)

| ID | Description | Steps | Expected |
|----|-------------|-------|----------|
| TC-MASHC-1 | Register on button press | Press commissioning button | `_mash-comm._tcp` MASH-D instance appears |
| TC-MASHC-2 | Deregister on timeout | Wait 120s in commissioning | `_mash-comm._tcp` instance removed |
| TC-MASHC-3 | Deregister on success | Complete commissioning | `_mash-comm._tcp` instance removed |
| TC-MASHC-4 | TXT record format | Enter commissioning | D, cat, serial, brand, model fields present |
| TC-MASHC-5 | Instance conflict | Two devices same D | Suffix added (-2) |
| TC-MASHC-6 | Already operational | Device in zone, press button | `_mash-comm._tcp` added, `_mash._tcp` kept |

### TC-MASHO-*: Operational Discovery (`_mash._tcp`)

| ID | Description | Steps | Expected |
|----|-------------|-------|----------|
| TC-MASHO-1 | Register on commission | Complete commissioning | `_mash._tcp` zone-device instance appears |
| TC-MASHO-2 | Instance name format | Inspect instance | `<zone-id>-<device-id>` format |
| TC-MASHO-3 | TXT record format | Inspect TXT | ZI, DI, VP fields present |
| TC-MASHO-4 | Multi-zone instances | Device in 2 zones | Two `_mash._tcp` instances |
| TC-MASHO-5 | Zone removed | Remove from one zone | That instance removed, other kept |
| TC-MASHO-6 | All zones removed | Remove from all zones | No `_mash._tcp` instances |

### TC-QR-*: QR Code Parsing

| ID | Description | Input | Expected |
|----|-------------|-------|----------|
| TC-QR-1 | Valid QR | `MASH:1:1234:12345678` | Parse success |
| TC-QR-2 | Leading zeros setupcode | `MASH:1:0:00000001` | Parse success, setupcode="00000001" |
| TC-QR-3 | Invalid prefix | `EEBUS:1:1234:12345678` | Error: Invalid prefix |
| TC-QR-4 | Short setupcode | `MASH:1:1234:1234` | Error: Invalid setup code |
| TC-QR-5 | Wrong field count | `MASH:1:1234` | Error: Invalid field count |
| TC-QR-6 | Discriminator overflow | `MASH:1:9999:12345678` | Error: Discriminator out of range |

### TC-DISC-*: Discriminator Handling

| ID | Description | Setup | Expected |
|----|-------------|-------|----------|
| TC-DISC-1 | Match found | QR D=1234, device D=1234 | Device discovered via `_mash-comm._tcp` |
| TC-DISC-2 | No match | QR D=1234, no device D=1234 | Timeout, error |
| TC-DISC-3 | Multiple match | Two devices D=1234 | Both discovered |
| TC-DISC-4 | Collision resolve | Wrong device first | PASE fails, retry finds correct |

### TC-DSTATE-*: Discovery State Transitions

| ID | Description | Initial State | Action | Expected |
|----|-------------|---------------|--------|----------|
| TC-DSTATE-1 | Enter comm mode | UNCOMMISSIONED | Button press | `_mash-comm._tcp` registered |
| TC-DSTATE-2 | Comm timeout | COMMISSIONING_OPEN | Wait 120s | `_mash-comm._tcp` deregistered |
| TC-DSTATE-3 | Comm success | COMMISSIONING_OPEN | Complete pairing | `_mash-comm._tcp` removed, `_mash._tcp` added |
| TC-DSTATE-4 | Add zone (operational) | OPERATIONAL | Button press | `_mash-comm._tcp` added, `_mash._tcp` kept |
| TC-DSTATE-5 | Second zone added | OPERATIONAL+COMM | Complete | New `_mash._tcp` instance added |
| TC-DSTATE-6 | Remove all zones | OPERATIONAL | RemoveZone (last) | All `_mash._tcp` removed |

### TC-BROWSE-*: Service Browsing

| ID | Description | Setup | Expected |
|----|-------------|-------|----------|
| TC-BROWSE-1 | Browse commissionable | Multiple commissioning devices | All `_mash-comm._tcp` found |
| TC-BROWSE-2 | Browse operational | Multiple operational devices | All `_mash._tcp` found |
| TC-BROWSE-3 | Filter by discriminator | QR scan D=1234 | Only MASH-1234 in `_mash-comm._tcp` |
| TC-BROWSE-4 | Filter by zone | Zone ID A1B2C3D4 | Only A1B2C3D4-* in `_mash._tcp` |
| TC-BROWSE-5 | Browse timeout | No services | Error after 10s |

---

## 11. Implementation Notes

### 11.1 mDNS Libraries

| Platform | Library | Notes |
|----------|---------|-------|
| Linux | Avahi | Default on most distros |
| macOS | mDNSResponder | Built-in |
| Windows | Bonjour SDK | From Apple |
| Embedded | lwIP mDNS | Lightweight |
| Go | github.com/grandcat/zeroconf | Cross-platform |
| Python | zeroconf | Pure Python |

### 11.2 QR Code Libraries

| Platform | Library | Notes |
|----------|---------|-------|
| Go | github.com/skip2/go-qrcode | Generation |
| Python | qrcode | Generation |
| JavaScript | qrcode.js | Browser generation |
| Scanning | Platform camera APIs | OS-specific |

### 11.3 Constrained Device Considerations

- Pre-compute QR code at manufacturing (save at runtime)
- Use minimal TXT records (omit optional fields)
- Share mDNS state with main application (single responder)
