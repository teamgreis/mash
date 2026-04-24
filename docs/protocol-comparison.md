# Protocol Comparison: MASH vs Matter 1.5 vs EEBUS

> Three-way comparison of smart energy device protocols

**Status:** Reference
**Last Updated:** 2026-01-31

---

## Contents

- [1. Executive Summary](#1-executive-summary)
- [2. Architecture](#2-architecture)
- [3. Device / Data Model](#3-device--data-model)
- [4. Interaction Model](#4-interaction-model)
- [5. Transport & Serialization](#5-transport--serialization)
- [6. Security & Commissioning](#6-security--commissioning)
- [7. Discovery](#7-discovery)
- [8. Multi-Controller Support](#8-multi-controller-support)
- [9. Energy Management Coverage](#9-energy-management-coverage)
- [10. Resource Requirements & Constraints](#10-resource-requirements--constraints)
- [11. Summary Matrix](#11-summary-matrix)
- [12. References](#12-references)

---

## 1. Executive Summary

| | **MASH** | **Matter 1.5** | **EEBUS (SHIP/SPINE)** |
|---|---|---|---|
| **Purpose** | Energy device management | General-purpose IoT + energy | Energy device communication |
| **Design philosophy** | Minimal, deterministic | Broad interoperability | Maximum flexibility |
| **Specification size** | Single spec (~100 pages) | 3 specs (~3,000+ pages) | 2 specs + use case docs (~1,000+ pages) |
| **First energy features** | Core design goal | Added in 1.3, expanded in 1.5 | Original purpose |
| **Target devices** | Energy equipment (EVSE, inverter, heat pump, battery) | All consumer IoT including energy | Energy equipment |
| **Organization** | Open source | CSA (Connectivity Standards Alliance) | EEBus Initiative e.V. |

**Key takeaway:** Matter 1.5 has significantly expanded its energy management capabilities (EVSE, DEM, pricing, metering), but still lacks critical features for European energy management: phase-specific limits, controller-driven power curtailment, regulatory grid operator integration, and full bidirectional charging control. MASH occupies a distinct niche: purpose-built for energy with Matter-inspired architecture but dramatically lower complexity than either.

---

## 2. Architecture

### 2.1 Layer Models

**MASH (5 layers):**
```
Application / Use Cases
Data Model (CBOR serialization)
Interaction Model (Read/Write/Subscribe/Invoke)
Transport (TCP / TLS 1.3)
Discovery (mDNS/DNS-SD)
```

**Matter 1.5 (7+ layers):**
```
Application / Device Types
Data Model (Matter TLV)
Interaction Model (Read/Write/Subscribe/Invoke)
Secure Channel (PASE/CASE sessions, MRP)
Transport (UDP+MRP / TCP / BLE+BTP)
Network (IPv6 / Thread / Wi-Fi / Ethernet / BLE)
Discovery (mDNS/DNS-SD + BLE advertising)
```

**EEBUS:**
```
Use Cases (LPC, CEVC, COB, etc.)
SPINE Data Model (XML/JSON, Functions, Classes)
SPINE Protocol (Binding, Subscription, Discovery)
SHIP Transport (WebSocket framing)
TLS 1.2+
Discovery (mDNS/DNS-SD)
```

### 2.2 Specification Structure

| Aspect | MASH | Matter 1.5 | EEBUS |
|--------|------|------------|-------|
| Core spec docs | 1 (unified) | 3 (Core, Application Clusters, Device Library) | 2 (SHIP transport, SPINE data model) |
| Use case docs | Integrated into features | Integrated into clusters/device types | Separate per use case (13+ documents) |
| Data model definition | YAML + code gen | XML (ZAP tool) | XSD schemas |
| Test specification | Integrated (mash-test) | Separate certification program | Separate test specs per use case |

---

## 3. Device / Data Model

### 3.1 Hierarchy Comparison

| Level | MASH | Matter 1.5 | EEBUS (SPINE) |
|-------|------|------------|---------------|
| Level 1 | **Device** | **Node** | **Device** |
| Level 2 | **Endpoint** | **Endpoint** | **Entity** (with sub-entities) |
| Level 3 | **Feature** | **Cluster** | **Feature** (with Functions) |
| Level 4 | Attribute / Command | Attribute / Command / Event | Function (with Elements) |

### 3.2 Key Differences

**MASH:**
- Flat endpoint list (no nesting)
- EndpointType enum defines semantics (EV_CHARGER, INVERTER, BATTERY, etc.)
- Topology implicit from EndpointType
- 9 core features covering all energy use cases
- Feature IDs are 16-bit integers

**Matter 1.5:**
- Endpoints can compose via Descriptor cluster's PartsList (tree structure)
- Device Types define required/optional clusters per endpoint
- Explicit composition patterns (e.g., EVSE is always endpoint composition)
- Multiple energy-specific clusters added in 1.3-1.5
- Cluster IDs are 32-bit integers

**EEBUS (SPINE):**
- Entities can nest arbitrarily (entity hierarchy)
- EntityType and FeatureType define device structure
- Features have roles (client/server/special)
- Functions are the atomic data unit within features
- Addresses are multi-level: device/entity[]/feature
- 50+ standard classes (feature types)

### 3.3 Addressing

```
MASH:     deviceId / endpointId / featureId / attribute
          evse-001 / 1 / Measurement / acActivePower

Matter:   nodeId / endpointId / clusterId / attributeId
          (integer IDs at each level)

EEBUS:    device / entity[] / feature / function
          evse-001 / [0,1] / Measurement / powerDescription
```

### 3.4 Capability Discovery

| Mechanism | MASH | Matter 1.5 | EEBUS |
|-----------|------|------------|-------|
| Endpoint enumeration | Read endpoint list | Descriptor cluster PartsList | Detailed discovery (NodeManagement) |
| Feature capabilities | featureMap bitmap (32-bit) | FeatureMap per cluster | Function list per feature |
| Attribute enumeration | attributeList | AttributeList global attribute | Function list with operations |
| Command enumeration | acceptedCommandList | AcceptedCommandList / GeneratedCommandList | Role + function operations |
| Self-describing | Yes (complete) | Yes (complete) | Partial (requires use case knowledge) |

---

## 4. Interaction Model

### 4.1 Operations

| Operation | MASH | Matter 1.5 | EEBUS (SPINE) |
|-----------|------|------------|---------------|
| Read data | **Read** | **Read** | **read** (request) + **reply** (response) |
| Write data | **Write** (full replace) | **Write** (full or list) | **write** (full or partial/restricted) |
| Notifications | **Subscribe** | **Subscribe** | **Binding** (ownership) + **Subscription** (notifications) |
| Execute action | **Invoke** | **Invoke** | **call** (request) + **result** (response) |
| Unsolicited push | via Subscribe | via Subscribe | **notify** (autonomous push) |
| Error reporting | Status in response | Status Response action | **result** with error code |
| Partial update | Not supported (full replace) | Fabric-scoped list writes | **write** with partial flag |

**Total operation types:** MASH: 4 | Matter: 4 | EEBUS: 6 (read, reply, write, notify, call, result)

### 4.2 Subscription Model

| Aspect | MASH | Matter 1.5 | EEBUS |
|--------|------|------------|-------|
| Initial data | Priming report (all current values) | Priming report | Initial read required |
| Updates | Delta notifications | Delta reports | Notify messages |
| Interval control | minInterval / maxInterval | MinIntervalFloor / MaxIntervalCeiling (negotiated) | No interval parameters |
| Persistence | Lost on disconnect | Lost on session termination | Persistent (stored on both sides) |
| Binding (separate) | No (unified with subscribe) | No | Yes (binding = ownership, subscription = notification) |
| Max subscriptions | Per-connection limit | Per-node limit (3 per fabric min) | Implementation-defined |

### 4.3 Message Overhead Example

Approximate message sizes for reading a power measurement value:

| Protocol | Request | Response | Encoding |
|----------|---------|----------|----------|
| MASH | ~40 bytes | ~60 bytes | CBOR with integer keys |
| Matter | ~80 bytes | ~120 bytes | Matter TLV |
| EEBUS | ~400 bytes | ~800 bytes | JSON (XML-derived structure) |

---

## 5. Transport & Serialization

### 5.1 Transport Protocols

| Aspect | MASH | Matter 1.5 | EEBUS (SHIP) |
|--------|------|------------|--------------|
| Primary transport | TCP | UDP (with MRP reliability) | WebSocket (over TCP) |
| Alternative transports | None | TCP, BLE (BTP), NFC (NTL), Wi-Fi (PAFTP) | None |
| TLS version | 1.3 (mandatory) | 1.3 (for TCP-based TLS) | 1.2+ |
| Authentication | Mutual TLS | CASE sessions (custom crypto layer) | Mutual TLS (SKI verification) |
| Framing | 4-byte length prefix + CBOR | Message counter + protocol header | WebSocket frames |
| Max message size | 8 KB | ~1,280 bytes (UDP/MRP), larger over TCP | No explicit limit |
| Reliability | TCP (inherent) | MRP over UDP / TCP inherent | WebSocket + TCP (inherent) |
| Keep-alive | App-layer ping/pong (30s) | MRP retransmission / subscription heartbeat | SHIP ping mechanism |
| Connection model | Controller initiates, 1 per zone | Any node can connect, multi-session | Either side initiates (race condition possible) |

### 5.2 Serialization

| Aspect | MASH | Matter 1.5 | EEBUS |
|--------|------|------------|-------|
| Wire format | CBOR (RFC 8949) | Matter TLV (custom) | JSON (RFC 8259) |
| Schema definition | YAML | XML (ZAP) | XSD |
| Key encoding | Integer keys | Integer tags | String keys |
| Typical message size | < 2 KB | < 1.3 KB (UDP), larger over TCP | 4-10 KB |
| Human-readable | No (binary) | No (binary) | Yes (JSON text) |
| Schema evolution | Feature versioning (specVersion) | ClusterRevision per cluster | Namespace versioning |

### 5.3 Connection Model

```
MASH:    Controller ──TCP/TLS──► Device (always controller-initiated)
         One connection per controller-device pair per zone
         Bidirectional messaging over single connection

Matter:  Commissioner ──(various)──► Node
         Multiple sessions possible (PASE for commissioning, CASE for operational)
         MRP sessions maintain state over UDP, TCP connections are persistent

EEBUS:   Node A ◄──WebSocket/TLS──► Node B (either side can initiate)
         Double-connection race condition when both sides connect simultaneously
         Requires connection de-duplication logic
```

---

## 6. Security & Commissioning

### 6.1 Trust Model

| Aspect | MASH | Matter 1.5 | EEBUS (SHIP) |
|--------|------|------------|--------------|
| Trust type | Binary (paired or not) | Binary (in fabric or not) + ACL | Trust levels (0-100 range) |
| Access control | Zone-based priority | ACL entries per fabric | Trust level + role-based |
| Certificate authority | Zone CA (controller-generated) | RCAC (fabric root, commissioner-generated) | Self-signed (no mandatory CA) |
| Global trust registry | None | DCL (Distributed Compliance Ledger) | None |
| Device attestation | Optional (manufacturer) | Mandatory (DAC chain via CSA) | None |

### 6.2 Certificate Hierarchy

| Certificate Type | MASH | Matter 1.5 | EEBUS |
|------------------|------|------------|-------|
| Global root | None | PAA (CSA-approved) | None |
| Product CA | None | PAI (per manufacturer/product) | None |
| Device identity | Optional (Mfr Attestation) | DAC (mandatory, per device) | Self-signed certificate |
| Operational root | Zone CA (20yr) | RCAC (fabric root, ~20yr) | N/A |
| Operational intermediate | None | ICAC (optional) | N/A |
| Node certificate | Op Cert (1yr, auto-renewed) | NOC (~20yr, manual UpdateNOC) | Self-signed (no expiry typically) |

### 6.3 Commissioning

| Aspect | MASH | Matter 1.5 | EEBUS (SHIP) |
|--------|------|------------|--------------|
| PAKE protocol | SPAKE2+ | SPAKE2+ (PASE) | None (SKI-based trust) |
| Setup code | 8-digit (~27 bits) | 8-digit (27 bits) | SKI (Subject Key Identifier) |
| Setup code delivery | QR code | QR code / NFC / manual | Display / manual / QR code |
| Session establishment | SPAKE2+ then TLS with Op Cert | PASE (commissioning) then CASE (operational) | TLS handshake + SKI verification + trust negotiation |
| Commissioning window | 15 min default (3 min - 3 hr range) | 15 min default | No timeout defined |
| Steps to pair | 5 (discover, connect, SPAKE2+, CSR, cert install) | Multi-step (PASE, attestation, NOC install, CASE) | Multiple phases (connection, trust, access setup, binding, subscription) |

### 6.4 Certificate Renewal

| Aspect | MASH | Matter 1.5 | EEBUS |
|--------|------|------------|-------|
| Mechanism | Auto-renewal (controller-initiated) | Manual UpdateNOC command | No renewal mechanism |
| Trigger | 7 days before expiry (DEC-077) | Administrator action | N/A (self-signed, no expiry) |
| Key rotation | Fresh key pair each renewal | Fresh key pair | Not defined |
| In-session renewal | Yes (no TLS reconnect) | Requires fail-safe context | N/A |
| Revocation | Zone removal + natural expiry | DCL + fabric reset | Trust removal (SKI un-trust) |

---

## 7. Discovery

### 7.1 Network Discovery

| Aspect | MASH | Matter 1.5 | EEBUS (SHIP) |
|--------|------|------------|--------------|
| Protocol | mDNS/DNS-SD | mDNS/DNS-SD | mDNS/DNS-SD |
| Commissionable | `_mash-comm._tcp` | `_matterc._udp` | `_ship._tcp` |
| Operational | `_mash._tcp` | `_matter._tcp` | `_ship._tcp` |
| BLE discovery | Not supported | BLE advertising (commissioning) | Not supported |
| Thread/mesh | Not supported | Thread Border Router SRP | Not supported |

### 7.2 QR Code Format

```
MASH:   MASH:<version>:<discriminator>:<setupcode>:<vendorid>:<productid>
        MASH:1:1234:12345678:0x1234:0x5678

Matter:  MT:<base-38 encoded payload>
         (encodes passcode, discriminator, VID, PID, discovery capabilities)

EEBUS:   SKI-based (manufacturer-specific QR content)
```

---

## 8. Multi-Controller Support

### 8.1 Architecture

| Aspect | MASH | Matter 1.5 | EEBUS |
|--------|------|------------|-------|
| Concept | Multi-zone (up to 5 zones) | Multi-fabric (up to 5 fabrics typical) | Multi-connection (no explicit limit) |
| Priority model | Zone types with fixed priority (GRID > LOCAL > TEST) | No built-in priority; ACL per fabric | No built-in priority; application-level |
| Conflict resolution | Per-feature: limits = most restrictive wins, setpoints = highest priority wins | Application-level (no framework) | Not defined in spec |
| Independent certificates | Yes (separate Zone CA per zone) | Yes (separate RCAC per fabric) | Yes (separate SKI per connection) |
| Connections per device | Up to max_zones + 1 | Multiple sessions (fabric-limited) | Implementation-defined |

### 8.2 Priority Resolution (MASH-specific)

MASH is the only protocol of the three with built-in priority-based conflict resolution for energy management:

```
Zone Priority:
  GRID (1) > LOCAL (2) > TEST (3)

Resolution Rules:
  LIMITS:    min(all zone limits)          -- most restrictive wins
  SETPOINTS: highest_priority_zone_value   -- priority wins
```

Matter and EEBUS leave conflict resolution to application-level implementation.

---

## 9. Energy Management Coverage

### 9.1 Feature/Cluster Mapping

| Energy Domain | MASH Features | Matter 1.5 Clusters | EEBUS Use Cases |
|---------------|---------------|---------------------|-----------------|
| **Device identity** | DeviceInfo | Basic Information, Descriptor | DeviceClassification, NetworkManagement |
| **Operating state** | Status | State attributes within device-specific clusters (e.g., EVSE) | Status-specific functions per use case |
| **Electrical config** | Electrical | No direct equivalent | ElectricalConnection |
| **Power measurement** | Measurement | Electrical Power Measurement | Measurement (via MPC, MGCP, MOB, MOI, MPS) |
| **Energy measurement** | Measurement | Electrical Energy Measurement | Measurement |
| **Load/production control** | EnergyControl | Device Energy Management (PowerAdjustment) | LPC, LPP |
| **Battery control** | EnergyControl (on BATTERY endpoint) | Device Energy Management (on battery device) | COB |
| **EV charging** | ChargingSession + EnergyControl | Energy EVSE + Energy EVSE Mode | CEVC + OSCEV + OPEV + DBEVC |
| **Tariff/pricing** | Tariff | Commodity Price + Commodity Tariff | IncentiveTable (via ITPCM) |
| **Grid signals** | Signals (PRICE, CONSTRAINT, FORECAST) | Commodity Price (partial) | ToUT + POEN + ITPCM |
| **Device plan/forecast** | Plan | Device Energy Management (forecast) | Power sequences (via SPINE) |
| **Heat pump flexibility** | EnergyControl (processState) | Device Energy Management | OHPCF |
| **Water heater** | (future) | Water Heater Management | (not standardized) |
| **Metering** | Measurement (on SUB_METER/GRID_CONNECTION) | Commodity Metering | MGCP |
| **V2X (vehicle-to-grid)** | EnergyControl (V2X feature bit) | Energy EVSE (V2X feature) | DBEVC + OSCEV |

### 9.2 Energy-Specific Cluster Count

| Protocol | Energy-related features/clusters | Total features/clusters in spec |
|----------|----------------------------------|-------------------------------|
| MASH | 9 features (all energy-focused) | 9 + vendor extensions |
| Matter 1.5 | ~10 energy clusters | Dozens of clusters total |
| EEBUS | 50+ classes (all energy-focused) | 50+ classes |

### 9.3 Coverage Gaps

**MASH gaps vs Matter 1.5:**
- No water heater management (planned)
- No commodity metering cluster equivalent (Measurement covers basic metering)

**MASH gaps vs EEBUS:**
- No historical time-series data
- No HVAC-specific use cases (planned)
- Fewer smart appliance use cases (washing machine, dishwasher, etc.)

**Matter 1.5 gaps vs MASH:**
- No built-in multi-controller priority resolution
- No explicit state machine for control states (AUTONOMOUS, CONTROLLED, LIMITED, FAILSAFE)
- More complex commissioning (multi-step PASE+CASE vs 5 steps)
- No domain-specific zone types (GRID, LOCAL, TEST)

**Matter 1.5 gaps vs EEBUS:**
- No phase-specific current limits (EEBUS OPEV supports independent per-phase current limits for circuit overload protection)
- No controller-driven power curtailment (DEM is device-flexibility-oriented; EEBUS LPC/LPP allows controller to set consumption/production limits directly)
- No equivalent to EEBUS's cascading limit architecture (LPC instance 1 + instance 2)
- No grid operator integration (EEBUS supports regulatory grid limits e.g. German section 14a EnWG via grid operator LPC instances)
- Limited bidirectional charging control (V2X feature bit exists but lacks production-side limits and discharge scheduling depth of EEBUS DBEVC)
- No PV surplus charging workflow (EEBUS OSCEV provides optimization for self-consumption from solar production)

**EEBUS gaps vs both:**
- No modern PAKE commissioning (SKI-based trust only)
- No built-in multi-controller conflict resolution
- WebSocket+JSON is heavier than CBOR or TLV on constrained devices
- Double-connection race condition still present
- Persistent subscriptions complicate reconnection scenarios

---

## 10. Resource Requirements & Constraints

| Aspect | MASH | Matter 1.5 | EEBUS |
|--------|------|------------|-------|
| Target RAM | 256 KB (ESP32-class) | 512 KB+ (Thread), 4 MB+ (full) (est.) | 4 MB+ (typical) (est.) |
| Flash footprint (est.) | ~100 KB | ~500 KB - 2 MB (est.) | ~500 KB - 1 MB (est.) |
| Network requirements | IPv6 + TCP | IPv6 + UDP/TCP (+ Thread/BLE optional) | IPv4 or IPv6 + TCP + WebSocket |
| Crypto requirements | TLS 1.3, SPAKE2+, X.509 | TLS 1.3, SPAKE2+, CASE, X.509, Matter TLV certs | TLS 1.2+, X.509 |
| Typical message size | < 2 KB | < 1.3 KB (UDP), variable (TCP) | 4-10 KB |
| Connection overhead | 1 TCP/TLS per zone | Multiple sessions per fabric | 1 WebSocket/TLS per peer |

---

## 11. Summary Matrix

| Dimension | MASH | Matter 1.5 | EEBUS |
|-----------|------|------------|-------|
| **Spec complexity** | Low | High | Medium-High |
| **Implementation effort** | Low | High | Medium-High |
| **Wire efficiency** | High (CBOR) | High (TLV) | Low (JSON) |
| **RAM footprint** | Low (256 KB target) | Medium-High | Medium-High |
| **Transport flexibility** | Low (TCP only) | High (UDP, TCP, BLE, NFC, Wi-Fi) | Low (WebSocket only) |
| **Energy features** | Purpose-built | Added in 1.3-1.5 | Purpose-built |
| **Multi-controller** | Built-in priority model | Multi-fabric (no priority) | App-level (no framework) |
| **Commissioning** | Simple (5 steps, SPAKE2+) | Complex (PASE+CASE, attestation) | Complex (multi-phase, SKI trust) |
| **Certificate renewal** | Auto (1-year, in-session) | Manual (UpdateNOC) | None (self-signed) |
| **State machines** | ControlStateEnum + ProcessStateEnum | Per-cluster state | Per-use case state (heartbeat-inferred) |
| **Ecosystem** | Open source, no certification | CSA certification, large ecosystem | EEBus Initiative, certification available |
| **Maturity** | New (2025) | Mature (2022+, 1.5 in 2025) | Mature (2017+) |

---

## 12. References

### MASH
- [Protocol Overview](protocol-overview.md) -- Architecture and design
- [Features](features/README.md) -- Feature registry and EEBUS mapping
- [Security](security.md) -- Certificates, commissioning, zones
- [Matter Comparison](matter-comparison.md) -- PKI/certificate deep-dive
- [Decision Log](decision-log.md) -- Design decisions and rationale

### Matter 1.5
- [Matter Core Specification](https://csa-iot.org/developer-resource/specifications-download-request/) -- Interaction model, security, transport
- [Matter Application Cluster Specification](https://csa-iot.org/developer-resource/specifications-download-request/) -- Energy management clusters
- [Matter Device Library Specification](https://csa-iot.org/developer-resource/specifications-download-request/) -- EVSE and energy device types

### EEBUS
- [SHIP Specification v1.0.1 / v1.1.0 RC1](https://www.eebus.org/) -- Transport layer
- [SPINE Protocol Specification](https://www.eebus.org/) -- Data model, operations
- [SPINE Resource Specification](https://www.eebus.org/) -- Classes, device model
- Use case specifications: LPC, LPP, CEVC, COB, OHPCF, MPC, MGCP, etc.
