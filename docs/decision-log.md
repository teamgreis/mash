# MASH Protocol Decision Log

> Tracking what we evaluated, decided, and declined

**Created:** 2025-01-24
**Last Updated:** 2026-02-10

---

## How to Use This Document

Each decision is logged with:
- **Context**: Why we considered this
- **Options Evaluated**: What alternatives we looked at
- **Decision**: What we chose
- **Rationale**: Why we chose it
- **Declined Alternatives**: What we rejected and why

When the docs cite a decision inline (e.g., "per DEC-077") and you want a quick gist without scrolling, the [Index by topic](#index-by-topic) below groups entries by area.

---

## Index by topic

### Foundations
- [DEC-001: Protocol Naming](#dec-001-protocol-naming)
- [DEC-002: Design Philosophy - Simplicity over Flexibility](#dec-002-design-philosophy-simplicity-over-flexibility)
- [DEC-003: Target Hardware Constraints](#dec-003-target-hardware-constraints)
- [DEC-004: Initial Use Case Scope](#dec-004-initial-use-case-scope)
- [DEC-005: Commissioning Model](#dec-005-commissioning-model)
- [DEC-050: Protocol Versioning Strategy](#dec-050-protocol-versioning-strategy)

### Wire format & interaction model
- [DEC-006: Serialization Format](#dec-006-serialization-format) (CBOR with integer keys)
- [DEC-007: Device Model Hierarchy](#dec-007-device-model-hierarchy) (Device > Endpoint > Feature)
- [DEC-008: Interaction Model](#dec-008-interaction-model) (Read / Write / Subscribe / Invoke)
- [DEC-009: Connection Initiation](#dec-009-connection-initiation)
- [DEC-035: Matter-Style Capability Discovery](#dec-035-matter-style-capability-discovery)
- [DEC-037: Two-Level Capability Discovery Pattern](#dec-037-two-level-capability-discovery-pattern)
- [DEC-038: Command Parameters vs Stored Attributes](#dec-038-command-parameters-vs-stored-attributes)
- [DEC-051: Static attributeList Immutability](#dec-051-static-attributelist-immutability)
- [DEC-052: Feature-Level Subscription Model](#dec-052-feature-level-subscription-model)
- [DEC-069: Max Message Size 8 KB](#dec-069-max-message-size-8-kb)
- [DEC-070: Attribute Range Validation via YAML min / max](#dec-070-attribute-range-validation-via-yaml-min-max)
- [DEC-071: Subscription Notification Ordering Across Connections](#dec-071-subscription-notification-ordering-across-connections)
- [DEC-072: AttributeList Must Fit in a Single Message](#dec-072-attributelist-must-fit-in-a-single-message)
- [DEC-074: featureMap Bit Semantics](#dec-074-featuremap-bit-semantics)
- [DEC-080: Remove Events from the Protocol Surface](#dec-080-remove-events-from-the-protocol-surface)

### Security & commissioning
- [DEC-010: Multi-Controller and Pairing Model](#dec-010-multi-controller-and-pairing-model)
- [DEC-011: Transport Layer](#dec-011-transport-layer) (TLS 1.3)
- [DEC-012: Device Attestation Model](#dec-012-device-attestation-model)
- [DEC-013: Operational Certificates](#dec-013-operational-certificates)
- [DEC-014: Pairing Protocol](#dec-014-pairing-protocol) (SPAKE2+ / PASE)
- [DEC-015: Certificate Lifecycle](#dec-015-certificate-lifecycle)
- [DEC-018: Setup Code Reusability](#dec-018-setup-code-reusability)
- [DEC-019: Delegated Commissioning (Backend)](#dec-019-delegated-commissioning-backend)
- [DEC-040: Device Identity via Certificate Fingerprint](#dec-040-device-identity-via-certificate-fingerprint)
- [DEC-041: Bidirectional Feature Access and Topology-Based Permissions](#dec-041-bidirectional-feature-access-and-topology-based-permissions)
- [DEC-042: Pairing Request Mechanism for Deferred Commissioning](#dec-042-pairing-request-mechanism-for-deferred-commissioning)
- [DEC-047: Commissioning Security Hardening](#dec-047-commissioning-security-hardening)
- [DEC-048: Commissioning Window Duration Alignment](#dec-048-commissioning-window-duration-alignment)
- [DEC-061: Message-Gated Commissioning Locking](#dec-061-message-gated-commissioning-locking)
- [DEC-067: Single-Port ALPN-Based Commissioning Routing and Stable Certificate](#dec-067-single-port-alpn-based-commissioning-routing-and-stable-certificate)
- [DEC-068: Invalid Passcode Validation](#dec-068-invalid-passcode-validation)
- [DEC-084: SPAKE2+ Algorithm Specification (RFC 9383 + PBKDF2)](#dec-084-spake2-algorithm-specification-rfc-9383--pbkdf2)
- [DEC-076: Retain In-Session Certificate Renewal](#dec-076-retain-in-session-certificate-renewal)
- [DEC-077: Shorten Renewal Window to 7 Days](#dec-077-shorten-renewal-window-to-7-days)
- [DEC-078: Commissionable mDNS TXT Schema (canonical)](#dec-078-commissionable-mdns-txt-schema-canonical)
- [DEC-082: Two-Port Deployment as Conformant Alternative to Single-Port ALPN Routing](#dec-082-two-port-deployment-as-conformant-alternative-to-single-port-alpn-routing)

### Multi-zone
- [DEC-016: Multi-Zone Support](#dec-016-multi-zone-support)
- [DEC-017: Per-Feature Priority](#dec-017-per-feature-priority)
- [DEC-020: Zone Roles (Owner vs Admin)](#dec-020-zone-roles-owner-vs-admin)
- [DEC-021: App-EMS Admin Authorization](#dec-021-app-ems-admin-authorization)
- [DEC-023: Terminology - "Zone" instead of "Fabric"](#dec-023-terminology-zone-instead-of-fabric)
- [DEC-043: One Zone Per Zone Type Constraint](#dec-043-one-zone-per-zone-type-constraint)
- [DEC-059: Auto Re-enter Commissioning on Last Zone Removal](#dec-059-auto-re-enter-commissioning-on-last-zone-removal)
- [DEC-060: TEST Zone Type for Conformance Testing](#dec-060-test-zone-type-for-conformance-testing)

### Features (energy, electrical, status, charging, signals/tariff/plan)
- [DEC-024: Limit Resolution - Stacked Limits (Most Restrictive Wins)](#dec-024-limit-resolution-stacked-limits-most-restrictive-wins)
- [DEC-025: Terminology - "Feature" instead of "Cluster"](#dec-025-terminology-feature-instead-of-cluster)
- [DEC-026: EnergyControl Feature Design](#dec-026-energycontrol-feature-design-capability-first-forecast-optional)
- [DEC-027: Feature Separation](#dec-027-feature-separation-electrical-measurement-energycontrol)
- [DEC-028: Setpoints and V2H Phase Balancing](#dec-028-setpoints-and-v2h-phase-balancing)
- [DEC-029: Measurement Feature and EndpointType](#dec-029-measurement-feature-and-endpointtype)
- [DEC-030: Status Feature for Operating State](#dec-030-status-feature-for-operating-state)
- [DEC-031: DeviceInfo Feature](#dec-031-deviceinfo-feature-with-complete-device-structure)
- [DEC-032: ChargingSession Feature](#dec-032-chargingsession-feature-for-ev-charging)
- [DEC-033: Signals, Tariff, and Plan Features](#dec-033-signals-tariff-and-plan-features)
- [DEC-034: Explicit Control State and Optional Process Lifecycle](#dec-034-explicit-control-state-and-optional-process-lifecycle)
- [DEC-036: Charging Mode and Responsibility Model](#dec-036-charging-mode-and-responsibility-model)
- [DEC-039: State Machine Interaction Rules](#dec-039-state-machine-interaction-rules)
- [DEC-049: Contractual Limits and Override Tracking](#dec-049-lpclpp-use-case-support-contractual-limits-and-override-tracking)
- [DEC-073: Failsafe-Limit Attributes Gated on acceptsLimits](#dec-073-failsafe-limit-attributes-gated-on-acceptslimits)
- [DEC-075: Process Lifecycle Is Opt-In](#dec-075-process-lifecycle-is-opt-in)
- [DEC-079: Limit-Override Notification Mechanism](#dec-079-limit-override-notification-mechanism)

### Conformance, use cases, PICS
- [DEC-053: Endpoint Type Conformance Registry](#dec-053-endpoint-type-conformance-registry)
- [DEC-054: Endpoint-Aware PICS Format](#dec-054-endpoint-aware-pics-format)
- [DEC-055: Use Cases on the Wire](#dec-055-use-cases-on-the-wire)
- [DEC-056: Controller-Side Use Case Declarations](#dec-056-controller-side-use-case-declarations)
- [DEC-057: Integer Use Case IDs and Scenario Bitmaps](#dec-057-integer-use-case-ids-and-scenario-bitmaps)
- [DEC-058: Capability Snapshots in Protocol Logs](#dec-058-capability-snapshots-in-protocol-logs)

### Connection management & transport
- [DEC-022: IPv6-Only Network Layer](#dec-022-ipv6-only-network-layer)
- [DEC-044: Message Correlation and Request Timeouts](#dec-044-message-correlation-and-request-timeouts)
- [DEC-045: Transport-Layer Heartbeat Sufficient for Failsafe](#dec-045-transport-layer-heartbeat-sufficient-for-failsafe)
- [DEC-062: Transport-Level Connection Cap](#dec-062-transport-level-connection-cap)
- [DEC-063: Busy Rejection Response](#dec-063-busy-rejection-response)
- [DEC-064: Stale Connection Reaper](#dec-064-stale-connection-reaper)
- [DEC-065: Connection Cooldown Starts at Release](#dec-065-connection-cooldown-starts-at-release)
- [DEC-066: Close Commissioning Connection After Certificate Exchange](#dec-066-close-commissioning-connection-after-certificate-exchange)
- [DEC-081: Remove `_mashd._udp` Commissioner-Discovery Service](#dec-081-remove-_mashd_udp-commissioner-discovery-service)

---

## Decisions

### DEC-001: Protocol Naming

**Date:** 2025-01-24
**Status:** Proposed

**Context:** Need a working name for the new protocol.

**Options Evaluated:**
1. MASH (Minimal Application-layer Smart Home)
2. LEAP (Lightweight Energy Application Protocol)
3. SIMPLE (Smart Interoperable Minimal Protocol for Local Energy)

**Decision:** MASH (working name, subject to change)

**Rationale:**
- Memorable and short
- Captures the "minimal" goal
- Works as both acronym and word

**Declined Alternatives:**
- LEAP: Too generic
- SIMPLE: Overused acronym pattern

---

### DEC-002: Design Philosophy - Simplicity over Flexibility

**Date:** 2025-01-24
**Status:** Accepted

**Context:** EEBUS chose maximum flexibility (7 RFE modes, 250+ data structures), leading to 7,000+ implementation variations.

**Options Evaluated:**
1. Keep EEBUS flexibility, improve documentation
2. Moderate flexibility with stricter guidelines
3. Minimal flexibility with fixed operations

**Decision:** Minimal flexibility with fixed operations

**Rationale:**
- EEBUS proves flexibility creates incompatibility
- Matter succeeds with simpler model (Read/Write/Subscribe/Invoke)
- Easier to add features later than remove complexity

**Declined Alternatives:**
- Option 1: Doesn't solve the fundamental problem
- Option 2: Half-measures lead to the same issues

---

### DEC-003: Target Hardware Constraints

**Date:** 2025-01-24
**Status:** Accepted

**Context:** Need to define minimum hardware requirements.

**Options Evaluated:**
1. High-end only (1MB+ RAM, 32-bit CPU)
2. Mid-range (256KB-512KB RAM, 32-bit CPU)
3. Ultra-low power (64KB RAM, basic MCU)
4. Linux/RTOS only

**Decision:** Mid-range (256KB RAM target, ESP32-class devices)

**Rationale:**
- Balances capability with real-world device constraints
- ESP32 is extremely common in energy devices
- Allows meaningful protocol without extreme compromises
- Still requires thoughtful message size design

**Declined Alternatives:**
- High-end only: Excludes too many embedded devices
- Ultra-low power: Would require extreme simplification, likely separate "lite" profile
- Linux only: Excludes the embedded devices we want to support

---

### DEC-004: Initial Use Case Scope

**Date:** 2025-01-24
**Status:** Accepted

**Context:** Need to define initial use cases to shape feature design.

**Options Evaluated:**
1. EV Charging only (most common EEBUS use case)
2. Load Control / Power Limits only
3. Measurement Data only
4. All three together

**Decision:** All three together

**Rationale:**
- These three use cases are interconnected in real deployments
- Designing for one would miss patterns needed by others
- EV charging inherently needs measurement and load control
- Better to have coherent design from start

---

### DEC-005: Commissioning Model

**Date:** 2025-01-24
**Status:** Accepted

**Context:** SHIP uses mDNS discovery + complex pairing with trust levels and PINs.

**Options Evaluated:**
1. Matter-style commissioning (QR code / setup code)
2. mDNS discovery + simplified pairing
3. Support both methods

**Decision:** Matter-style commissioning (QR code / setup code)

**Rationale:**
- Proven UX in consumer devices
- Simpler implementation than SHIP's trust level negotiation
- Avoids SHIP's PIN security flaws (no nonce, brute force vulnerable)
- Widely understood by installers and users
- Can still use mDNS for discovery after pairing

**Declined Alternatives:**
- mDNS + pairing: Still requires complex trust negotiation
- Both methods: Adds implementation complexity, unclear benefit

---

### DEC-006: Serialization Format

**Date:** 2025-01-24
**Status:** Accepted

**Context:** Need to choose message serialization format for 256KB RAM target.

**Options Evaluated:**
1. JSON (156 bytes for test message, human-readable)
2. CBOR (75-108 bytes, IETF standard RFC 8949)
3. MessagePack (105 bytes, no IETF standard)
4. Protocol Buffers (~85 bytes, requires code generation)
5. Custom TLV (~70 bytes, must build ourselves)

**Decision:** CBOR with integer keys for compactness

**Rationale:**
- 52% smaller than JSON without code generation
- IETF standard (RFC 8949) - not proprietary
- Used by Matter, CoAP, Thread - proven in IoT/embedded
- Self-describing - can decode without schema (debugging)
- Streaming parse possible - low RAM usage
- COSE (CBOR security) fits well for auth tokens

**Declined Alternatives:**
- JSON: Too large for constrained devices
- MessagePack: No IETF standard, less IoT adoption
- Protobuf: Code generation adds build complexity
- Custom TLV: High implementation risk, no tooling

**Mitigation for debuggability:**
- Build CLI tool with `cbor2json` conversion
- Use diagnostic notation in docs/logs

---

### DEC-007: Device Model Hierarchy

**Date:** 2025-01-24
**Status:** Accepted

**Context:** Need to define how devices, capabilities, and data are structured.

**Options Evaluated:**
1. 2-Level: Device > Feature (simpler, but needs array indices for multi-port)
2. 3-Level: Device > Endpoint > Feature (like Matter's Device > Endpoint > Cluster)
3. Variable depth (flexible but complex parsing)

**Decision:** 3-Level hierarchy: Device > Endpoint > Feature

**Rationale:**
- Cleanly handles multi-function devices (dual-port EVSEs, hybrid inverters)
- Matter-aligned - familiar to developers
- Fixed depth = simple, predictable parsing
- Minimal overhead for single-function devices (use endpoint 1)
- Future-proof for complex device types

**Addressing Scheme:**
```
device_id / endpoint_id / feature_id / attribute_or_command
```

**Endpoint Conventions:**
- Endpoint 0: Root device info (manufacturer, model, etc.)
- Endpoint 1+: Functional endpoints (charger port, inverter section, etc.)

**Declined Alternatives:**
- 2-Level: Requires array indices (`Measurement[0]`) - less semantic clarity
- Variable depth: Complicates parsing, unclear benefit

### DEC-008: Interaction Model

**Date:** 2025-01-24
**Status:** Accepted

**Context:** SPINE has 7 RFE operation modes creating 7,000+ implementation variations.

**Options Evaluated:**
1. 7 Operations (like SPINE: replaceAll, updateAll, partial, delete, deleteAll, notify, read)
2. 5 Operations (Read, Write, Subscribe, Invoke + Partial Write)
3. 4 Operations (like Matter: Read, Write, Subscribe, Invoke)
4. 3 Operations (Read, Write, Invoke - no subscriptions)

**Decision:** 4 Operations (Read, Write, Subscribe, Invoke)

**Rationale:**
- Write replaces the entire value - no partial update complexity
- Subscribe handles notifications (no separate "notify" mode)
- Invoke for commands with parameters
- Read for current state
- Dramatically reduces implementation complexity

**Declined Alternatives:**
- Partial updates: Adds significant complexity for marginal benefit
- 3 operations (no subscribe): Forces polling, inefficient

---

### DEC-009: Connection Initiation

**Date:** 2025-01-24
**Status:** Accepted

**Context:** SHIP allows both peers to initiate connections, causing race conditions.

**Options Evaluated:**
1. Client initiates only (controller → device)
2. Either can initiate (like SHIP)
3. Server initiates (push model)

**Decision:** Client initiates only

**Rationale:**
- Deterministic - no race conditions
- Natural model: energy manager (client) connects to EVSE (server)
- Simpler implementation
- Eliminates SHIP's "double connection" problem entirely

---

### DEC-010: Multi-Controller and Pairing Model

**Date:** 2025-01-24
**Status:** Accepted

**Context:** When multiple controllers exist, need clear rules for who controls what.

**Options Evaluated:**
1. First wins, others rejected
2. Priority-based takeover
3. Shared control allowed
4. Capability-based routing with user confirmation

**Decision:** Hybrid capability-routing with priority override

**Key Principles:**
1. **Discovery**: Controllers discover devices and their capabilities
2. **Suggestion**: System suggests pairings based on capability matching
3. **User Confirmation**: User confirms pairing (required)
4. **Priority Takeover**: Higher-priority controller can request takeover
5. **User Override**: User can always override priority (e.g., disconnect grid operator)

**Priority Levels (example):**
- Level 1: Grid Operator / DSO (highest)
- Level 2: Building/Commercial Energy Manager
- Level 3: Home Energy Manager
- Level 4: User App (lowest)

**Takeover Flow:**
1. New controller requests control with its priority level
2. Device notifies current controller of takeover request
3. If higher priority: takeover succeeds (current controller notified)
4. User can physically interact with device to reject takeover

**Rationale:**
- Avoids SPINE's undefined "appropriate client"
- Enables grid override scenarios
- User always has final say
- Clear, deterministic behavior

---

## Pending Decisions

### DEC-011: Transport Layer

**Date:** 2025-01-24
**Status:** Accepted

**Context:** Need to choose transport for 256KB devices. SHIP uses WebSocket/TLS, Matter uses UDP+MRP or TCP.

**Options Evaluated:**
1. WebSocket over TLS (like SHIP) - ~55KB code
2. Raw TCP/TLS with length-prefix framing - ~42KB code
3. CoAP over DTLS - ~60KB code, UDP NAT issues
4. UDP with custom reliability (like Matter MRP)
5. QUIC - ~200KB+, too heavy

**Decision:** TCP/TLS with simple length-prefix framing

**Rationale:**
- **TCP reliability built-in** - no need to implement MRP ourselves
- **Smallest code footprint** - ~42KB vs 55KB for WebSocket
- **Minimal overhead** - 4 bytes per message vs 2-14 for WebSocket
- **Sufficient for target devices** - EVSEs, inverters are always-on, not battery
- **Simple implementation** - TLS library + trivial framing code

**Framing Format:**
```
┌────────────────────────────────────────┐
│ Length (4B, big-endian) │ CBOR Payload │
└────────────────────────────────────────┘
```

**Keep-Alive Protocol (to be defined):**
- Ping/pong messages in CBOR
- 30-second interval
- Connection closed after 3 missed pongs

**Declined Alternatives:**
- WebSocket: Extra 13KB code, HTTP upgrade overhead not needed
- CoAP/DTLS: UDP NAT issues in home networks
- UDP+MRP: Unnecessary complexity for always-on devices
- QUIC: Won't fit in 256KB RAM

---

### DEC-012: Device Attestation Model

**Date:** 2025-01-24
**Status:** Accepted

**Context:** Need to decide if devices must prove authenticity via manufacturer certificates.

**Options Evaluated:**
1. Required attestation (all devices need manufacturer CA)
2. Optional attestation (support CA if present, allow self-signed)
3. No attestation (self-signed only)

**Decision:** Optional attestation

**Rationale:**
- Supports large manufacturers with CA infrastructure
- Doesn't exclude small vendors or DIY devices
- Trust ultimately established through pairing, not certificate chain
- Pragmatic for energy device ecosystem

---

### DEC-013: Operational Certificates

**Date:** 2025-01-24
**Status:** Accepted

**Context:** After pairing, should devices get controller-issued certificates?

**Options Evaluated:**
1. Operational certs (controller issues during pairing)
2. Device cert only (keep using original)

**Decision:** Operational certificates

**Rationale:**
- Enables certificate rotation (1-year validity)
- Supports multi-controller (each controller issues own zone cert)
- Clean revocation via cert deletion
- Like Matter's NOC model

**Lifecycle:**
- Validity: 1 year
- Renewal: Auto-renewed by controller before expiry
- Revocation: Controller sends delete command to device

---

### DEC-014: Pairing Protocol

**Date:** 2025-01-24
**Status:** Accepted

**Context:** How to establish initial trust between controller and device.

**Options Evaluated:**
1. SPAKE2+ with numeric code (like Matter PASE)
2. Simple pre-shared key
3. Certificate fingerprint in QR

**Decision:** SPAKE2+ with 8-digit numeric setup code

**Rationale:**
- Proven PAKE protocol (used by Matter)
- No secrets transmitted in clear
- Resistant to offline dictionary attacks
- 8 digits provides ~27 bits entropy (sufficient for local pairing)
- Easy to type manually if needed

**Setup Code Format:**
- 8 decimal digits (00000000-99999999)
- Encoded in QR code with discriminator + vendor info
- Optional: printed on device label

---

### DEC-015: Certificate Lifecycle

**Date:** 2025-01-24
**Status:** Accepted

**Context:** How long are certificates valid and how are they managed.

**Decision:**

| Certificate | Validity | Renewal | Revocation |
|-------------|----------|---------|------------|
| Device/Attestation | 20 years | Never | N/A |
| Operational | 1 year | Auto by controller | Controller deletes from device |
| Zone CA | 99 years | N/A | Zone removal |

**Note:** Zone CA uses very long validity (99 years) because:
- Zone ID is derived from CA certificate fingerprint
- Changing CA would require re-commissioning all devices
- Expiry doesn't add security (zone removal is the revocation mechanism)
- Effectively "permanent" for practical purposes

**Revocation Flow:**
1. Controller decides to remove device
2. Controller sends "RemoveZone" command
3. Device deletes operational certificate
4. Device returns to unpaired state

---

### DEC-016: Multi-Zone Support

**Date:** 2025-01-24
**Status:** Accepted

**Context:** Real deployments have multiple controllers (SMGW, EMS, apps) needing access.

**Options Evaluated:**
1. Single zone with roles (one CA, shared)
2. Multiple zones (each controller independent)

**Decision:** Multiple zones

**Rationale:**
- Each controller (SMGW, EMS) is independent with own Zone CA
- Device holds multiple operational certs (one per zone)
- No coupling between controllers
- SMGW replacement doesn't affect EMS
- Like Matter's multi-admin model

**Implementation:**
- Device supports up to 5 zones
- Each zone has: type, CA, operational cert
- ZoneManagement feature for add/remove
- Zones can see each other's types for coordination

---

### DEC-017: Per-Feature Priority

**Date:** 2025-01-24
**Status:** Accepted

**Context:** When multiple controllers want to control the same feature, who wins?

**Options Evaluated:**
1. Zone type priority (GRID > BUILDING > HOME > USER)
2. Most restrictive wins (min of all limits)
3. Separate limits tracked per controller

**Decision:** Zone type priority

**Zone Types (priority order):**
```
GRID = 1   // DSO, SMGW, utility - external/regulatory authority
LOCAL = 2  // Residential/building EMS - local energy management
```

**Behavior:**
- Higher priority can clear/remove lower priority's settings
- Lower priority is notified of changes
- User physical override always possible (button on device)

**Important Clarification (see DEC-024):**
- For **limits** specifically, priority does NOT override
- All zone limits are tracked, most restrictive (min) wins
- This ensures safety constraints (fuses, wiring) are respected
- Priority matters for: access control, removing others' settings, notifications

---

### DEC-018: Setup Code Reusability

**Date:** 2025-01-24
**Status:** Accepted

**Context:** Can the same setup code be used for multiple zone commissionings?

**Decision:** Static setup code, reusable for all zones

**Rationale (like Matter):**
- Setup code is factory-programmed, printed on device
- Same code used for all zone commissionings
- Security provided by:
  1. Physical access to QR code required
  2. SPAKE2+ protocol prevents eavesdropping
  3. User scanning implies consent
- No benefit to rolling codes - physical access is the gate

---

### DEC-019: Delegated Commissioning (Backend)

**Date:** 2025-01-24
**Status:** Accepted

**Context:** SMGW has no UI - can't scan QR codes directly.

**Decision:** Support delegated commissioning via backend

**Flow:**
1. User scans device QR with utility app (phone)
2. App uploads setup code + device info to DSO backend
3. Backend securely forwards to SMGW
4. SMGW uses setup code to commission device via SPAKE2+
5. Device gets SMGW's operational cert (GRID zone)

**QR Code Content (sufficient for backend delegation):**
```
MASH:<version>:<discriminator>:<setupcode>:<vendorid>:<productid>
```

**Security:**
- Backend must securely store/transmit setup codes
- TLS between all components
- Setup code only valuable with network access to device

### DEC-020: Zone Roles (Owner vs Admin)

**Date:** 2025-01-24
**Status:** Accepted

**Context:** How do apps participate in commissioning without being fragile zone owners?

**Decision:** Three-tier role model

| Role | Description | Example |
|------|-------------|---------|
| **Zone Owner** | Owns Zone CA, issues certs | EMS, SMGW |
| **Zone Admin** | Authorized to commission | Phone App, Installer Tool |
| **Zone Member** | Normal participant | Devices |

**Key Principles:**
- Apps are admins, NOT owners (losing phone doesn't orphan devices)
- EMS is always zone owner (has CA)
- Apps cannot own zones - always need an EMS
- Multiple admins supported (family members, installers)

**Commissioning with Admin:**
1. App has admin token from EMS
2. App does SPAKE2+ with device (has setup code)
3. App gets CSR from device
4. App **forwards CSR to EMS** (app can't sign)
5. EMS signs, returns operational cert
6. App installs cert on device

---

### DEC-021: App-EMS Admin Authorization

**Date:** 2025-01-24
**Status:** Accepted

**Context:** How does an app become a zone admin for an EMS?

**Decision:** QR code + confirmation via EMS web UI

**Flow:**
```
User             Phone App           EMS Web UI
 │                   │                   │
 │── Open web UI ────┼──────────────────►│
 │── "Add admin" ────┼──────────────────►│
 │◄──────────────────┼── Show temp QR ───┤  (5 min expiry)
 │                   │                   │
 │── Scan QR ───────►│                   │
 │                   │── SPAKE2+ ───────►│
 │                   │                   │
 │◄──────────────────┼── "Add 'Phone'?" ─┤
 │── Confirm ────────┼──────────────────►│
 │                   │◄── Admin token ───┤
```

**Security:**
- QR is temporary (5 min expiry)
- User must confirm in EMS UI (proves access to EMS)
- App name displayed for verification

**Pre-requisite:**
- EMS must have web UI (local or cloud-based)
- User must have access to EMS UI to add admins

---

### DEC-022: IPv6-Only Network Layer

**Date:** 2025-01-24
**Status:** Accepted

**Context:** Need to decide on IP version for the network layer.

**Options Evaluated:**
1. IPv4 only (legacy)
2. Dual-stack IPv4/IPv6 (two code paths)
3. IPv6 mandatory with IPv4 fallback
4. IPv6 only (no IPv4)

**Decision:** IPv6 only - no IPv4 support

**Rationale:**
- **Simpler implementation** - one code path, not two
- **Link-local (fe80::) always works** - even without DHCP/router
- **Linux support since 1999** - kernel 2.2, over 25 years ago
- **All modern embedded platforms support it** - ESP32, etc.
- **Matter is IPv6-only** - proven approach
- **Thread ready** - IPv6-only mesh, door open for future Thread support
- **No NAT issues** - every device gets unique address
- **Multicast discovery** - native support (ff02::fb for mDNS)

**IPv6 Features Used:**
```
Link-Local (fe80::/10)
├── Always available without configuration
├── Used for commissioning
└── Works without router/DHCP

Multicast (ff02::)
├── ff02::fb - mDNS discovery
└── Efficient group communication

SLAAC
└── Auto-configuration without DHCP
```

**Declined Alternatives:**
- IPv4 only: Legacy, NAT issues, limited addresses
- Dual-stack: Doubles implementation complexity, testing burden
- IPv4 fallback: No realistic scenario where needed
  - Old routers: Even cheap routers from 5+ years ago support IPv6
  - Old networks: We target new devices, not retrofitting
  - Corporate networks: Smart home devices aren't on corporate networks

---

### DEC-024: Limit Resolution - Stacked Limits (Most Restrictive Wins)

**Date:** 2025-01-24
**Status:** Accepted

**Context:** When multiple zones set limits on the same feature, how are they resolved?

**Options Evaluated:**
1. Priority override (higher priority zone's limit wins, others ignored)
2. Most restrictive wins (min of all limits, all tracked)
3. Subscription-based (notify on removal, let zones re-apply)

**Decision:** Most restrictive wins (stacked limits)

**Critical Example - Why Priority Override Fails:**
```
SMGW (GRID, priority 1): "Grid allows 6 kW"
EMS (LOCAL, priority 2):   "House fuse is 5 kW"

Priority-based: 6 kW → exceeds fuse capacity → dangerous
Stacked limits: min(6, 5) = 5 kW → safe
```

**Lower priority controllers MUST be able to impose stricter limits** for physical safety constraints (fuses, wiring, device ratings).

**Internal vs Exposed State:**
```
Device internal state (not exposed):
  zoneLimits: map[zoneID] → limit
    Zone 1 (GRID): 6000 W
    Zone 2 (LOCAL):  5000 W

Exposed via API (per-zone scoped):
  effectiveLimit: min(all) = 5000 W    // same for all zones
  myLimit: 6000 W or 5000 W            // depends on requesting zone
  limitActive: true/false              // is my limit the effective one?
```

**API Design (simple, zone-isolated):**
```
Limit feature:
  Attributes:
    - effectiveLimit: uint32 (W)   // min(all zone limits)
    - myLimit: uint32 (W)          // this zone's limit (zone-scoped)
    - limitActive: bool            // is my limit the effective one?

  Commands:
    - SetLimit(value, duration?)
    - ClearLimit()
```

**Why NOT expose per-zone limits:**
- Each zone knows its own limit (it set it)
- Each zone can see effective limit
- Zone isolation / privacy between controllers
- Simpler API, less data in messages
- Device handles complexity internally

**What Priority IS Used For:**
| Aspect | Priority-based? |
|--------|-----------------|
| Setting limits | No - all zones can set |
| Effective limit | No - min(all) wins |
| Removing OTHER zone's limits | Yes - higher can clear lower |
| Access to other features | Yes - per DEC-017 |

**Subscription for Awareness:**
- Zones subscribe to Limit.effectiveLimit
- Get notified when effective limit changes
- Can infer: if effectiveLimit < myLimit, someone else is more restrictive
- EMS knows when constraint lifts (can adjust strategy)

**Declined Alternatives:**
- Priority override: Dangerous - ignores physical constraints
- Subscription re-apply: Extra complexity, race conditions, still doesn't solve fuse scenario

---

### DEC-025: Terminology - "Feature" instead of "Cluster"

**Date:** 2025-01-24
**Status:** Accepted

**Context:** Matter uses "cluster" for groupings of attributes and commands. Should we use the same term?

**Options Evaluated:**
1. Cluster (Matter terminology)
2. Feature (functional building block)
3. Capability
4. Service
5. Component

**Decision:** Use "Feature" as our terminology

**Rationale:**
- **Intuitive** - "device supports Limit feature" reads naturally
- **Functional focus** - describes what it does, not structure
- **Reusable** - features are building blocks composed into endpoints
- **Distinct from Matter** - avoids implying compatibility

**Terminology Mapping:**
| Matter | MASH |
|--------|------|
| Cluster | Feature |
| Cluster ID | Feature ID |

**Updated Hierarchy:**
```
Device > Endpoint > Feature > Attributes/Commands
```

**Core Features:**
| Feature | Purpose | Used By |
|---------|---------|---------|
| `Limit` | Power constraints, set/clear | EVSE, Inverter, Heat Pump |
| `Measurement` | Power, energy, voltage, current | All devices |
| `ChargingSession` | EV session state, SoC | EVSE only |
| `DeviceInfo` | Identity, firmware, capabilities | All (endpoint 0) |

**Device Composition Example:**
```
EVSE
├── Endpoint 0: DeviceInfo
└── Endpoint 1: Measurement + Limit + ChargingSession

Inverter
├── Endpoint 0: DeviceInfo
└── Endpoint 1: Measurement + Limit
```

**Declined Alternatives:**
- Cluster: Matter terminology, implies compatibility
- Capability: Too vague
- Service: Overloaded (web services, systemd, etc.)
- Component: Implies UI/frontend

---

### DEC-023: Terminology - "Zone" instead of "Fabric"

**Date:** 2025-01-24
**Status:** Accepted

**Context:** Matter uses "fabric" for trust boundaries / controller domains. Should we adopt the same term or choose our own?

**Options Evaluated:**
1. Fabric (Matter terminology)
2. Authority
3. Realm
4. Trust
5. Domain
6. Zone
7. Scope
8. Sector

**Decision:** Use "Zone" as our terminology

**Rationale:**
- **Simple and clear** - no explanation needed
- **No baggage** - unlike "domain" (DNS/AD) or "realm" (Kerberos)
- **Industrial/energy feel** - fits the smart energy context
- **Distinct from Matter** - avoids implying compatibility
- **Works naturally in context:**
  - "Device supports up to 5 zones"
  - "Add device to EMS zone"
  - "Zone owner vs zone admin"
  - "GRID zone has priority"

**Terminology Mapping:**
| Matter | MASH |
|--------|------|
| Fabric | Zone |
| Fabric ID | Zone ID |
| Fabric CA | Zone CA |
| Multi-fabric | Multi-zone |
| AddFabric | AddZone |
| RemoveFabric | RemoveZone |

**Declined Alternatives:**
- Fabric: Implies Matter compatibility, borrowed terminology
- Authority: Slightly redundant with "zone owner"
- Realm: Fantasy/Kerberos connotations
- Domain: DNS/Active Directory confusion
- Trust: Grammatically awkward in some contexts

---

### DEC-026: EnergyControl Feature Design (Capability-First, Forecast-Optional)

**Date:** 2025-01-25
**Status:** Accepted

**Context:** Need to define the Limit/EnergyControl feature that covers all EEBUS limit use cases (LPC, LPP, OPEV, OSCEV, CEVC, DBEVC, COB, OHPCF, POEN) while being simpler. Matter's DeviceEnergyManagement cluster is forecast-centric, but not all devices can provide forecasts (e.g., basic wallbox doesn't know EV's plan).

**Options Evaluated:**
1. EEBUS approach: Separate use cases for each device type (10+ specs)
2. Matter approach: Forecast-centric with PowerForecast/StateForecast required
3. Capability-first: Device announces what it CAN do, forecast optional

**Decision:** Capability-first, forecast-optional design

**Key Principle:**
```
Every device announces: "What I CAN do" (capabilities)  ← MANDATORY
Some devices announce:  "What I PLAN to do" (forecast)  ← OPTIONAL
Controller sends:       "What I WANT you to do" (limit) ← CORE FUNCTION
```

**Rationale:**
- **All devices can announce capabilities** - even the simplest EVSE knows its max power
- **Not all devices can forecast** - wallbox without ISO 15118-20 doesn't know EV plans
- **Forecasts add value when available** - smart devices can optimize better
- **Single feature covers all EEBUS use cases** - no separate LPC/LPP/OPEV/etc.
- **Matter-inspired simplicity** - feature flags, not separate protocols

**Feature Structure:**

```
EnergyControl Feature
├── MANDATORY (all controllable devices):
│   ├── deviceType: DeviceTypeEnum
│   ├── state: StateEnum
│   ├── capabilities: CapabilityStruct
│   ├── effectiveLimit: int64 (mW)
│   └── optOutState: OptOutEnum
│
├── OPTIONAL (for flexibility announcement):
│   ├── flexibility: FlexibilityStruct
│   └── forecast: ForecastStruct
│
└── COMMANDS:
    ├── SetLimit(power, duration?, cause)
    ├── ClearLimit()
    ├── Pause(duration?)  [if isPausable]
    ├── Resume()          [if isPausable]
    └── AdjustStartTime(newStart)  [if isShiftable]
```

**DeviceTypeEnum (like Matter's ESAType):**
```
EVSE              = 0x00  // EV Charger
HEAT_PUMP         = 0x01  // Space heating/cooling
WATER_HEATER      = 0x02
BATTERY           = 0x03  // Home battery storage
INVERTER          = 0x04  // Solar/hybrid inverter
FLEXIBLE_LOAD     = 0x05  // Generic controllable load
OTHER             = 0xFF
```

**StateEnum:**
```
OFFLINE           = 0x00  // Not controllable
ONLINE            = 0x01  // Normal operation
FAULT             = 0x02  // Error state
LIMITED           = 0x03  // Limit active
PAUSED            = 0x04  // Temporarily paused
FAILSAFE          = 0x05  // Communication lost, failsafe active
```

**CapabilityStruct (MANDATORY):**
```
{
  direction: enum { CONSUMPTION, PRODUCTION, BIDIRECTIONAL }

  // Power boundaries (signed: + consumption, - production)
  nominalMaxConsumption: int64 (mW)
  nominalMaxProduction: int64 (mW)     // 0 if can't produce
  currentMaxConsumption: int64 (mW)    // Currently available
  currentMaxProduction: int64 (mW)
  currentMinPower: int64 (mW)          // Minimum operating point

  // For storage devices
  energyCapacity: int64? (mWh)
  stateOfCharge: uint8? (%)

  // Feature flags
  isPausable: bool
  isShiftable: bool
  acceptsLimits: bool
  acceptsSetpoints: bool               // Can track a target, not just limit
}
```

**FlexibilityStruct (OPTIONAL):**
```
{
  // Time flexibility
  earliestStart: timestamp?
  latestEnd: timestamp?

  // Energy flexibility
  energyMin: int64? (mWh)
  energyMax: int64? (mWh)
  energyTarget: int64? (mWh)

  // Power operating range
  powerRangeMin: int64 (mW)
  powerRangeMax: int64 (mW)

  // For interruptible loads
  minRunDuration: uint32? (s)
  maxPauseDuration: uint32? (s)
}
```

**ForecastStruct (OPTIONAL):**
```
{
  forecastId: uint32
  startTime: timestamp
  endTime: timestamp
  slots: ForecastSlot[] (max 10)
}

ForecastSlot {
  duration: uint32 (s)
  nominalPower: int64 (mW)
  minPower: int64? (mW)
  maxPower: int64? (mW)
  isPausable: bool?
}
```

**LimitCauseEnum (why the limit):**
```
GRID_EMERGENCY     = 0   // DSO/SMGW - highest priority, MUST follow
GRID_OPTIMIZATION  = 1   // DSO request for grid balancing
LOCAL_PROTECTION   = 2   // Fuse protection, overload prevention
LOCAL_OPTIMIZATION = 3   // Home optimization, cost savings
USER_PREFERENCE    = 4   // User app request
```

**OptOutEnum (user override, from Matter):**
```
NO_OPT_OUT        = 0   // Accept all adjustments
LOCAL_OPT_OUT     = 1   // Reject local optimization
GRID_OPT_OUT      = 2   // Reject grid requests
OPT_OUT           = 3   // Reject all external control
```

**Commands:**
```
SetLimit(
  power: int64 (mW),        // + consumption limit, - production limit
  duration: uint32? (s),    // Optional (0 = indefinite)
  cause: LimitCauseEnum
) → { success: bool, effectiveLimit: int64 }

ClearLimit() → { success: bool }

Pause(duration: uint32? (s)) → { success: bool }
Resume() → { success: bool }

AdjustStartTime(
  requestedStart: timestamp,
  cause: LimitCauseEnum
) → { success: bool, actualStart: timestamp }
```

**EEBUS Use Case Mapping:**

| EEBUS Use Case | MASH Coverage |
|----------------|---------------|
| LPC (Limit Power Consumption) | SetLimit with CONSUMPTION direction |
| LPP (Limit Power Production) | SetLimit with PRODUCTION direction |
| OPEV (Overload Protection EV) | SetLimit with LOCAL_PROTECTION cause |
| OSCEV (Self-Consumption EV) | Flexibility + SetLimit with LOCAL_OPTIMIZATION |
| CEVC (Coordinated EV Charging) | Forecast + Flexibility |
| DBEVC (Bidirectional EV) | BIDIRECTIONAL direction + Forecast |
| COB (Control of Battery) | BIDIRECTIONAL direction + SetLimit |
| OHPCF (Heat Pump Flexibility) | isPausable + isShiftable + Pause/Resume |
| POEN (Power Envelope) | Repeated SetLimit calls (or future schedule extension) |

**Failsafe Behavior (inherited from transport):**
1. Keep-alive fails (3 missed pongs)
2. Device state → FAILSAFE
3. Device applies failsafe limit (from capabilities)
4. On reconnect: zone must re-set limit
5. Device exits FAILSAFE when valid limit received

**Declined Alternatives:**
- EEBUS approach: 10+ use cases too complex, testing nightmare
- Pure forecast approach: Not all devices can forecast (basic wallbox)
- Current-based limits (OPEV style): Always use power (W), device converts

**Resolves:** OPEN-001 (Feature Definitions) - partially

---

### DEC-027: Feature Separation (Electrical, Measurement, EnergyControl)

**Date:** 2025-01-25
**Status:** Accepted

**Context:** During EnergyControl feature design, we realized phase configuration and electrical ratings are needed both for control (EnergyControl) and for interpreting measurement data. Similarly, measurement data (power readings, SoC) is distinct from control capabilities.

**Options Evaluated:**
1. Single monolithic feature with all electrical data
2. Two features: Electrical + EnergyControl (measurements in both)
3. Three features: Electrical (config), Measurement (telemetry), EnergyControl (control)

**Decision:** Three separate features with clear responsibilities

**Feature Responsibilities:**

| Feature | Question | Examples | Update Frequency |
|---------|----------|----------|------------------|
| **Electrical** | "What is this device?" | Phase config, voltage ratings, nominal power | Read once at discovery |
| **Measurement** | "What is it doing now?" | Power readings, energy totals, SoC | Subscribe for real-time |
| **EnergyControl** | "What can I tell it to do?" | Capabilities, limits, commands | Subscribe for state changes |

**Electrical Feature (Static Configuration):**
```
- phaseCount: 1, 2, or 3
- phaseMapping: {DevicePhase → GridPhase} for coordination
- nominalVoltage, supportedDirections
- nominalMaxConsumption, nominalMaxProduction (power ratings)
- maxCurrentPerPhase (for OPEV-style limits)
- supportsAsymmetric, phaseCurrentDirections
- energyCapacity (for batteries)
```

**Measurement Feature (Telemetry):**
```
- activePower, reactivePower (total, signed)
- energyConsumed, energyProduced (cumulative)
- stateOfCharge (for batteries)
- perPhaseReadings: {phase → {power, current, voltage}}
```

**EnergyControl Feature (Control):**
```
- deviceType, deviceState (capability flags)
- isPausable, isShiftable, canForecast
- consumptionLimit, productionLimit (active limits)
- currentLimits (per-phase, for OPEV)
- forecast (optional)
- Commands: SetLimit, SetCurrentLimits, ClearLimit, Pause, Resume
```

**Rationale:**
- **Separation of concerns**: Static config vs telemetry vs control have different update patterns
- **Efficiency**: Subscribe only to what you need (measurements or control state)
- **Reusability**: Measurement feature useful without EnergyControl (e.g., pure monitoring)
- **Phase consistency**: Phase mapping defined once in Electrical, used by both Measurement and EnergyControl
- **Per-phase current limits**: Supports OPEV-style asymmetric current limits + bidirectional (EEBUS gap)

**Phase Mapping Design:**
```
Device declares in Electrical:
  phaseMapping: {A: L1, B: L2, C: L3}  // or rotated: {A: L2, B: L3, C: L1}

Controller can now:
  - Interpret per-phase measurements correctly
  - Set per-phase current limits on correct grid phases
```

**Declined Alternatives:**
- Single feature: Too large, inefficient subscriptions, mixed update frequencies
- Two features: Unclear where measurements belong, phase config duplicated

**Resolves:** OPEN-001 (Feature Definitions) - Electrical and Measurement features now defined

---

### DEC-028: Setpoints and V2H Phase Balancing

**Date:** 2025-01-25
**Status:** Accepted

**Context:** Limits ("do not exceed") are not sufficient for all control scenarios. Battery systems, solar charging, and V2H bidirectional EVs need setpoints ("please target this value"). Additionally, V2H with asymmetric phase support requires per-phase current setpoints to balance household load across phases.

**Options Evaluated:**
1. Limits only - devices figure out their own targets
2. Single setpoint command (total power only)
3. Separate setpoint commands for power and per-phase current (mirrors limit structure)

**Decision:** Symmetric command structure for limits and setpoints

**New Commands:**
| | Total Power (mW) | Per-Phase Current (mA) |
|--|------------------|------------------------|
| **Hard constraint** | SetLimit | SetCurrentLimits |
| **Target** | SetSetpoint | SetCurrentSetpoints |
| **Clear constraint** | ClearLimit | ClearCurrentLimits |
| **Clear target** | ClearSetpoint | ClearCurrentSetpoints |

**New Capabilities:**
```
acceptsLimits: bool           // accepts SetLimit
acceptsCurrentLimits: bool    // accepts SetCurrentLimits
acceptsSetpoints: bool        // accepts SetSetpoint
acceptsCurrentSetpoints: bool // accepts SetCurrentSetpoints (V2H)
```

**AsymmetricSupportEnum (replaces bool):**
```
NONE          = 0x00  // Symmetric only
CONSUMPTION   = 0x01  // Asymmetric when charging
PRODUCTION    = 0x02  // Asymmetric when discharging
BIDIRECTIONAL = 0x03  // Asymmetric both directions
```

**Resolution Difference:**
```
LIMITS:    Most restrictive wins (all zones constrain together)
SETPOINTS: Highest priority zone wins (only one controller active)
```

**V2H Use Case:**
```
House consumption: L1=20A, L2=5A, L3=12A
EMS sends: SetCurrentSetpoints({A: 10000, B: 2000, C: 5000}, PRODUCTION, PHASE_BALANCING)
V2H EV discharges asymmetrically to balance phases
Result: Net import L1=10A, L2=3A, L3=7A (balanced)
```

**Rationale:**
- Symmetric structure is easier to learn and implement
- Per-phase current setpoints enable V2H phase balancing (EEBUS gap)
- AsymmetricSupportEnum clarifies which directions support different values per phase
- Limits constrain setpoints (device can't target beyond its limit)

**Extends EEBUS:**
- DBEVC only supports total power setpoints, not per-phase
- No EEBUS use case covers asymmetric discharge for phase balancing
- MASH fills this gap with SetCurrentSetpoints

---

### DEC-029: Measurement Feature and EndpointType

**Date:** 2025-01-25
**Status:** Accepted

**Context:** Need comprehensive measurement support covering all EEBUS monitoring use cases (MPC, MGCP, EVCEM, MOI, MOB, MPS). Also need a way to identify what each endpoint represents in multi-component devices like hybrid inverters.

**Options Evaluated for Measurements:**
1. Single feature with AC-only measurements
2. Separate AcMeasurement and DcMeasurement features
3. Single Measurement feature with both AC and DC attributes

**Decision:** Single Measurement feature with comprehensive AC and DC support

**Measurement Attributes:**

| Category | Attributes | Used By |
|----------|------------|---------|
| **AC Power** | acActivePower, acReactivePower, acApparentPower, acActivePowerPerPhase | INVERTER, GRID_CONNECTION, EV_CHARGER |
| **AC Current/Voltage** | acCurrentPerPhase, acVoltagePerPhase, acVoltagePhaseToPhasePair, acFrequency, powerFactor | All AC endpoints |
| **AC Energy** | acEnergyConsumed, acEnergyProduced | All AC endpoints |
| **DC Measurements** | dcPower, dcCurrent, dcVoltage, dcEnergyIn, dcEnergyOut | PV_STRING, BATTERY |
| **Battery State** | stateOfCharge, stateOfHealth, stateOfEnergy, useableCapacity, cycleCount | BATTERY |
| **Temperature** | temperature | BATTERY, INVERTER |

**EndpointType Enum:**

```
DEVICE_ROOT       = 0x00  // Device-level info (always endpoint 0)
GRID_CONNECTION   = 0x01  // AC grid connection point (smart meter)
INVERTER          = 0x02  // Inverter AC side (grid-facing)
PV_STRING         = 0x03  // PV string / solar input (DC)
BATTERY           = 0x04  // Battery storage (DC)
EV_CHARGER        = 0x05  // EVSE / wallbox
HEAT_PUMP         = 0x06  // Heat pump
WATER_HEATER      = 0x07  // Water heater / boiler
HVAC              = 0x08  // HVAC system
APPLIANCE         = 0x09  // Generic controllable appliance
SUB_METER         = 0x0A  // Sub-meter / circuit monitor
```

**Hybrid Inverter Example:**

```
Device (hybrid-inverter)
├── Endpoint 0: type=DEVICE_ROOT
├── Endpoint 1: type=INVERTER, label="Grid Connection"
│   └── Measurement: AC power, energy, voltage, current
├── Endpoint 2: type=PV_STRING, label="Roof South"
│   └── Measurement: DC power, voltage, current, yield
├── Endpoint 3: type=PV_STRING, label="Roof West"
│   └── Measurement: DC power, voltage, current, yield
└── Endpoint 4: type=BATTERY, label="LG Chem"
    └── Measurement: DC power, SoC, SoH, temperature
```

**Topology is Implicit:**
- AC endpoints (INVERTER, GRID_CONNECTION) → connect to grid
- DC endpoints (PV_STRING, BATTERY) → internal, connect to inverter DC bus
- No explicit parent/child relationships needed (grid is never DC)

**Sign Convention (Passive/Load):**
- Positive (+) = power flowing INTO component (consumption, charging)
- Negative (-) = power flowing OUT of component (production, discharging)

**EEBUS Use Case Coverage:**

| EEBUS | MASH Coverage |
|-------|---------------|
| MPC | acActivePower, acCurrentPerPhase, acVoltagePerPhase, acFrequency, acEnergy |
| MGCP | Same as MPC on GRID_CONNECTION endpoint |
| EVCEM | acActivePower, acCurrentPerPhase, acEnergyConsumed |
| MOI | All AC power types, powerFactor, temperature |
| MOB | dcPower/Current/Voltage, stateOfCharge/Health, cycleCount, temperature |
| MPS | dcPower, dcCurrent, dcVoltage, dcEnergyOut |

**Rationale:**
- Single feature simplifies subscription (one subscription for all measurements)
- EndpointType identifies component role without complex relationships
- AC/DC distinction covers all real-world scenarios
- Comprehensive coverage of 6+ EEBUS monitoring use cases

**Resolves:** OPEN-001 (Feature Definitions) - Measurement feature now fully defined

---

### DEC-030: Status Feature for Operating State

**Date:** 2025-01-25
**Status:** Accepted

**Context:** Operating state (running, standby, fault, etc.) was initially considered part of EnergyControl. However, non-controllable devices (e.g., monitoring-only devices, smart meters) also have operating states but don't support EnergyControl.

**Options Evaluated:**
1. Keep operating state in EnergyControl (requires EnergyControl for all devices)
2. Create separate Status feature (applicable to any endpoint)
3. Put operating state in DeviceInfo (but state is per-endpoint, not per-device)

**Decision:** Separate Status feature, per-endpoint

**Status Feature Attributes:**
```
{
  1: operatingState,    // OperatingStateEnum
  2: stateDetail,       // uint32 (vendor-specific sub-state)
  3: faultCode,         // uint32 (0 = no fault)
  4: faultMessage,      // string (optional human-readable)
}
```

**OperatingStateEnum:**
```
UNKNOWN       = 0x00  // State cannot be determined
OFFLINE       = 0x01  // Not connected / powered off
STANDBY       = 0x02  // Ready but not active
STARTING      = 0x03  // Transitioning to running
RUNNING       = 0x04  // Normal operation
PAUSED        = 0x05  // Temporarily suspended
SHUTTING_DOWN = 0x06  // Transitioning to offline
FAULT         = 0x07  // Error state (check faultCode)
MAINTENANCE   = 0x08  // Service/maintenance mode
```

**Per-Endpoint Scope:**
- Each endpoint has its own Status feature instance
- Hybrid inverter: PV_STRING endpoints can be RUNNING while BATTERY is STANDBY
- EVSE: Port 1 can be RUNNING (charging) while Port 2 is STANDBY

**Relationship to Other Features:**
```
Status Feature: "What's happening?" (state observation)
  └── operatingState: RUNNING, STANDBY, FAULT, etc.

EnergyControl Feature: "What can I tell it to do?" (control)
  └── Pause/Resume commands → affect operatingState
```

**Rationale:**
- Operating state is universally applicable (monitoring-only devices need it)
- Per-endpoint allows fine-grained status for multi-component devices
- Separates observation from control (EnergyControl is optional)
- Fault reporting tied to specific endpoint (not device-global)

**Declined Alternatives:**
- State in EnergyControl: Forces controllable interface on monitoring devices
- State in DeviceInfo: DeviceInfo is device-level (endpoint 0), state is per-endpoint

**Resolves:** OPEN-001 (Feature Definitions) - Status feature now defined

---

### DEC-031: DeviceInfo Feature with Complete Device Structure

**Date:** 2025-01-25
**Status:** Accepted

**Context:** Need a feature for device identification and discovery. EEBUS uses DeviceClassification (NID use case) with multiple function types. Matter uses Basic Information cluster on endpoint 0. Key question: how to minimize round-trips while providing complete device understanding.

**Options Evaluated:**
1. Minimal DeviceInfo (identity only) + separate Descriptor feature per endpoint
2. DeviceInfo with device identity + complete endpoint/feature structure
3. DeviceInfo + UseCaseInformation (like EEBUS) for capability discovery

**Decision:** DeviceInfo with complete device structure in single read

**Device ID Format:**
```
i:<IANA_PEN>:<unique>   - For vendors with IANA Private Enterprise Number
n:<vendor>:<unique>     - For vendors without IANA PEN
```

Examples:
- `i:46925:ABC123-XYZ` (using IANA PEN 46925)
- `n:acme:WB-2024-001` (using vendor name "acme")

**DeviceInfo Attributes:**
```
Identification: deviceId, vendorName, productName, productId, serialNumber, brandName?
Versions:       softwareVersion, hardwareVersion
Structure:      endpoints[] (id, type, label?, features[])
```

**Key Design Choices:**

| Choice | Decision | Rationale |
|--------|----------|-----------|
| Single read | Yes | One request gets everything needed to understand device |
| Endpoint structure | Included | No separate discovery needed |
| Feature list | Per-endpoint | Know exactly what each endpoint supports |
| User labels | Not included | EMS manages labels, simplifies device |
| Protocol version | Not included | Handled at connection level |
| Numeric vendor ID | Not required | String-based deviceId format sufficient |

**Feature ID Registry:**
```
0x01    DeviceInfo
0x02    Status
0x03    Electrical
0x04    Measurement
0x05    EnergyControl
0x06    ChargingSession
0x07    Tariff
0x08    Signals
0x09    Plan
0x0100+ Vendor-specific
```

**EEBUS NID Coverage:**
- All mandatory NID data points covered (Device Name, Serial Number, Brand Name)
- Optional fields mapped (Device Code, Software/Hardware Revision, Vendor Name/Code)
- User-configurable fields (Label, Description) not on device - EMS responsibility

**Rationale:**
- Single read minimizes round-trips (vs EEBUS's multiple requests)
- Complete structure enables immediate understanding of device capabilities
- IANA PEN format provides globally unique IDs without central registry for all
- Manufacturer labels on endpoints helpful for multi-component devices
- Read-only simplifies device implementation

**Declined Alternatives:**
- Separate Descriptor feature: Extra round-trips, unnecessary complexity
- UseCaseInformation: Too EEBUS-specific, features list is sufficient
- Numeric vendor/product IDs: Would require central registry

**Resolves:** OPEN-001 (Feature Definitions) - DeviceInfo feature now defined

---

### DEC-032: ChargingSession Feature for EV Charging

**Date:** 2025-01-25
**Status:** Accepted

**Context:** Need a feature for EV charging session data that accommodates different protocol capabilities (IEC 61851, ISO 15118-2, ISO 15118-20) and supports V2G bidirectional charging. Must cover EEBUS use cases: EVSOC, EVCS, SMR, EVCC/EVSECC, CEVC, DBEVC.

**Key Design Decisions:**

**1. Protocol-Aware Design:**
```
IEC 61851-1:  No smart comm - only session state/energy
ISO 15118-2:  SoC, energy demands, scheduled mode
ISO 15118-20: Dynamic control, V2G discharge constraints
```
All fields nullable to accommodate capability differences.

**2. Multiple EV Identifications:**
```cbor
evIdentifications: [
  { type: RFID, value: "04E57CD2..." },
  { type: MAC_EUI48, value: "AA:BB:CC:DD:EE:FF" },
  { type: VIN, value: "WVWZZZ3CZWE123456" },
  { type: PCID, value: "PCID-VW-2024-ABC" }
]
```
EVs can be identified via multiple mechanisms simultaneously.

**3. Energy Requests as Deltas (ISO 15118-20 style):**
```
evMinEnergyRequest = (minSoC - currentSoC) * capacity
evTargetEnergyRequest = (targetSoC - currentSoC) * capacity
evMaxEnergyRequest = (100% - currentSoC) * capacity
```
Positive = needs charging, Negative = can discharge, Zero = level reached.

**4. V2G Discharge Constraints:**
```
Can discharge = (evMinDischargingRequest < 0)
              AND (evMaxDischargingRequest >= 0)
              AND (evTargetEnergyRequest <= 0 OR evDischargeBelowTargetPermitted)
```
From EEBUS DBEVC / ISO 15118-20 cycle protection rules.

**5. Demand Mode Indicator:**
```
NONE                  - IEC 61851 (no demand info)
SINGLE_DEMAND         - Basic energy request
SCHEDULED             - ISO 15118 scheduled mode (EV plans)
DYNAMIC               - ISO 15118-20 dynamic mode (CEM controls)
DYNAMIC_BIDIRECTIONAL - ISO 15118-20 with V2G
```
Tells EMS what level of optimization is possible.

**EEBUS Use Case Coverage:**

| Use Case | Mapping |
|----------|---------|
| EVSOC | evStateOfCharge, evBatteryCapacity, evMin/Target/MaxEnergyRequest |
| EVCS | sessionEnergyCharged, sessionId |
| SMR | sessionId links to Measurement data |
| EVCC/EVSECC | evIdentifications, sessionId |
| CEVC | evMin/Target/MaxEnergyRequest, evDepartureTime, evDemandMode |
| DBEVC | evMin/MaxDischargingRequest, evDischargeBelowTargetPermitted |

**Now Covered by DEC-033:**
- CEVC incentive tables → **Signals (COMBINED) + Tariff features**
- CEVC charging plan curves → **Plan feature**
- CEVC/DBEVC power schedules → **Signals (CONSTRAINT type)**

**Rationale:**
- Accommodates all EV charging protocols in single feature
- Multiple identifications reflect real-world where EVs provide several IDs
- Energy request semantics match ISO 15118-20 for consistency
- V2G constraints essential for bidirectional charging use cases
- Demand mode indicator enables protocol-appropriate optimization

**Resolves:** OPEN-001 (Feature Definitions) - ChargingSession feature now defined, completing all initial features

---

### DEC-033: Signals, Tariff, and Plan Features

**Date:** 2025-01-25
**Status:** Accepted (Updated)

**Context:** Need to support time-based pricing (ToUT), power envelopes (POEN), coordinated EV charging (CEVC), and power forecasts. These scenarios share common data patterns (time slots) but differ in purpose and data direction.

**Research Findings:**

*EEBUS:*
- IncentiveTable with Tariff, Tiers, TierBoundaries, Incentives
- IncentiveTypes: absoluteCost, relativeCost, renewableEnergyPercentage, co2Emission
- Separate Bill feature for cost component breakdown

*Matter:*
- CommodityTariff: Structure (DayPatterns, CalendarPeriods, TariffComponents, PowerThresholds)
- CommodityPrice: Current price + forecast (up to 56 slots)
- DeviceEnergyManagement: Forecast with SlotStruct, PowerAdjustRequest, ConstraintBasedForecast

*ISO 15118 insight:*
- In CEVC, the EV is the optimizer (not the EMS)
- EMS sends incentives/limits TO EVSE via MASH
- EVSE translates to SA_ScheduleTuple via ISO 15118 TO EV
- EV returns ChargingProfile via ISO 15118 TO EVSE
- EVSE translates to Plan via MASH TO EMS

*Real-world tariffs:*
- Components vary independently: energy (~31%), grid fees (~28%), taxes/levies (~41%)
- Power-based tiers common (different rate above X kW)
- Feed-in tariffs separate from consumption tariffs
- CO2 intensity and renewable percentage increasingly important

**Options Evaluated:**
1. Single Schedule feature with all fields (bidirectional)
2. Separate features: PriceSchedule, PowerSchedule, Forecast
3. Two features: Schedule (time-varying values) + Tariff (stable structure)
4. Three features: Signals (IN) + Plan (OUT) + Tariff (stable structure)

**Decision:** Three features - Signals, Tariff, and Plan

The key insight is **data direction**:
- **Signals** = data flowing IN to device (from controllers)
- **Plan** = data flowing OUT from device (to controllers)
- **Tariff** = stable structure (rarely changes)

**Tariff Feature (0x07):**
- Defines price structure separately from time-varying values
- Components: ENERGY, GRID_FEE, TAX, LEVY, CO2, CREDIT
- Power tiers for demand-based pricing
- Separate consumption and production (feed-in) tariffs
- Signals references Tariff for component IDs

**Signals Feature (0x08):**
- Time-slotted INPUT data from controllers
- Five signal types: PRICE, CONSTRAINT, TARGET, FORECAST, COMBINED
- Supports multiple concurrent signals from different sources
- Resolution rules: most restrictive for limits, highest priority for prices
- SignalSlot fields: prices, limits, targets, forecasts, environmental signals

**Plan Feature (0x09):**
- Time-slotted OUTPUT data from device
- Device's intended behavior in response to Signals
- For EVSE: reflects EV's ISO 15118 ChargingProfile
- PlanSlot fields: plannedConsumption/Production, expectedSoC
- Events notify controllers when plan changes

**Use Case Coverage:**

| Use Case | Feature(s) | Direction | Key Data |
|----------|------------|-----------|----------|
| ToUT (time-of-use tariff) | Signals (PRICE) + Tariff | IN | componentPrices per slot |
| POEN (power envelope) | Signals (CONSTRAINT) | IN | min/maxConsumption/Production |
| CEVC input | Signals (COMBINED) + Tariff | IN | prices + limits + forecast |
| CEVC output (EV plan) | Plan | OUT | plannedConsumption, expectedSoC |
| Solar forecast | Signals (FORECAST) | IN | forecastProduction, confidence |
| Load forecast | Signals (FORECAST) | IN | forecastConsumption |
| Grid CO2 forecast | Signals (FORECAST) | IN | co2Intensity |

**CEVC Flow:**
```
EMS --> SetSignal(COMBINED) --> EVSE --> SA_ScheduleTuple --> EV
                                     <-- ChargingProfile <--
    <-- Plan (via RequestPlan) <--
```

**EEBUS/Matter Coverage:**

| Source | MASH Mapping |
|--------|--------------|
| EEBUS ToUT | Signals (PRICE) + Tariff |
| EEBUS POEN | Signals (CONSTRAINT) |
| EEBUS CEVC IncentiveTable | Signals (COMBINED) + Tariff |
| EEBUS CEVC ChargingPlan | Plan |
| Matter CommodityTariff | Tariff |
| Matter CommodityPrice | Signals (PRICE) |
| Matter DeviceEnergyManagement.Forecast | Signals (FORECAST) / Plan |

**Rationale:**
- Direction-based split (IN vs OUT) clarifies data flow semantics
- Matches ISO 15118 model where EV optimizes, not EMS
- Separating structure (Tariff) from values (Signals) matches real-world contracts
- Power tiers essential for demand-based pricing (common in Europe)
- Plan feature enables EMS to integrate device intentions into overall planning

**Declined Alternatives:**
- Single bidirectional Schedule feature: Confuses input vs output semantics
- Extending EnergyControl: Conflates control with information
- Per-type schedule features: Redundant structures

**Resolves:** Open Question #7 (Scheduled Limits)

---

### DEC-034: Explicit Control State and Optional Process Lifecycle

**Date:** 2025-01-25
**Status:** Accepted

**Context:** Analyzing EEBUS OHPCF (Optimization of Self-Consumption by Heat Pump Compressor Flexibility) and LPC (Limitation of Power Consumption) revealed two design problems in EEBUS:

1. **LPC's heartbeat-based state inference is bad**: EEBUS LPC uses heartbeats and implicit state transitions - the controller infers device state (Init, Limited, Failsafe, Unlimited) rather than reading it directly. This causes race conditions, debugging difficulty, and no single source of truth.

2. **OHPCF requires optional process lifecycle management**: Heat pumps, water heaters, and similar devices have "optional" tasks (e.g., "I could run the compressor now") that controllers schedule. This needs explicit task lifecycle tracking.

**Options Evaluated:**

*For control state:*
1. Keep EEBUS implicit heartbeat model (controller infers state)
2. Report operational state only (OFFLINE/ONLINE/FAULT)
3. Report explicit control relationship state (device tells you its control status)

*For process lifecycle:*
1. Extend Pause/Resume commands with implicit state
2. Add explicit ProcessStateEnum with full lifecycle tracking
3. Separate status/control features

**Decision:**

1. **ControlStateEnum** - explicit control relationship state reported by device:
   - `AUTONOMOUS` (0x00): Not under external control
   - `CONTROLLED` (0x01): Under controller authority, no active limit
   - `LIMITED` (0x02): Active power limit being applied
   - `FAILSAFE` (0x03): Connection lost, using failsafe limits
   - `OVERRIDE` (0x04): Device overriding for safety/legal/self-protection

2. **ProcessStateEnum** - explicit optional task lifecycle:
   - `NONE` (0x00): No optional process available
   - `AVAILABLE` (0x01): Process announced, not scheduled
   - `SCHEDULED` (0x02): Start time configured, waiting
   - `RUNNING` (0x03): Process executing
   - `PAUSED` (0x04): Paused by controller
   - `COMPLETED` (0x05): Finished successfully
   - `ABORTED` (0x06): Stopped/cancelled

3. **Failsafe configuration** - device knows what to do when controller disappears:
   - `failsafeConsumptionLimit`: limit to apply in FAILSAFE
   - `failsafeProductionLimit`: limit to apply in FAILSAFE
   - `failsafeDuration`: time in FAILSAFE before transitioning to AUTONOMOUS (2-24h)

4. **OptionalProcess struct** - for announcing optional tasks:
   - Process identification (id, description)
   - Power characteristics (estimate, min, max)
   - Timing constraints (duration, minRunDuration, minPauseDuration)
   - Control constraints (isPausable, isStoppable)
   - Energy characteristics (estimate, resumeEnergyPenalty)

5. **New commands**:
   - `Stop`: Abort task completely (vs Pause which can resume)
   - `ScheduleProcess`: Schedule an optional process to start
   - `CancelProcess`: Cancel a scheduled/running process

**Rationale:**

*Why explicit state reporting:*
- Single source of truth: device state is what device reports, not inferred
- No race conditions: no ambiguity about which state applies
- Debuggable: inspect state directly, no need to trace heartbeat history
- Universal: same ControlStateEnum works for LPC, EVSE, battery, heat pump, inverter

*Why separate ControlStateEnum from OperatingStateEnum (Status feature):*
- ControlStateEnum = "Am I being externally controlled?"
- OperatingStateEnum = "Am I working correctly?"
- Orthogonal concerns: device can be RUNNING (operational) but AUTONOMOUS (not controlled)

*Why ProcessStateEnum is orthogonal to ControlStateEnum:*
- Device can be LIMITED (control state) while process is RUNNING (task lifecycle)
- Or FAILSAFE (control state) with process PAUSED (task lifecycle)
- Different concerns: connection status vs task execution

*Failsafe rationale:*
- Devices MUST know what to do when controller disappears
- Pre-configured failsafe limits ensure safety
- Duration prevents devices being stuck in FAILSAFE forever
- Applicable to ALL device types: EVSE (stop charging after X hours), battery (discharge limits), heat pump (min heating)

**EEBUS Use Case Coverage:**

| EEBUS | MASH | Improvement |
|-------|------|-------------|
| LPC heartbeat state machine | ControlStateEnum | Explicit, not inferred |
| LPC failsafe by timeout | failsafeDuration → AUTONOMOUS | Same behavior, explicit |
| LPC "limited" state | controlState = LIMITED | Direct reading |
| OHPCF SmartEnergyManagementPs | OptionalProcess + ProcessStateEnum | Cleaner model |
| OHPCF power sequence state | processState | Explicit lifecycle |
| COB failsafe state | ControlStateEnum.FAILSAFE | Unified across device types |

**Declined Alternatives:**

- Heartbeat-based inference: Fundamentally flawed - no single source of truth
- Combined state enum: Conflates control relationship with task lifecycle
- Per-device-type state handling: Redundant, same concept applies universally

---

### DEC-035: Matter-Style Capability Discovery

**Date:** 2025-01-25
**Status:** Accepted (updated by DEC-050)

**Context:** MASH has multiple features (EnergyControl, Measurement, Signals, Tariff, Plan, etc.) with many optional attributes. Controllers need to know:
- Which features a device implements
- Which optional attributes are present within each feature
- Which commands the device accepts
- Device capabilities without reading each attribute individually

**Options Evaluated:**

1. **EEBUS approach**: nodeManagementUseCaseData to advertise use cases by name
2. **Trial and error**: Controller reads attributes and handles errors for missing ones
3. **Matter-style global attributes**: FeatureMap bitmap + attribute/command lists

**Decision:** Matter-style global attributes on every endpoint.

**Global Attributes (reserved IDs 0xFFF0-0xFFFF):**

| Attribute | ID | Type | Description |
|-----------|-----|------|-------------|
| `featureMap` | 0xFFFC | bitmap32 | Supported optional features |
| `attributeList` | 0xFFFB | array[uint16] | Implemented attribute IDs |
| `acceptedCommandList` | 0xFFFA | array[uint8] | Accepted command IDs |
| `generatedCommandList` | 0xFFF9 | array[uint8] | Response command IDs |
| `eventList` | 0xFFF8 | array[uint8] | Supported event IDs |

> **Note:** `clusterRevision` (0xFFFD) was originally included here but removed by DEC-050. Protocol versioning is handled by a single `specVersion` attribute in DeviceInfo (major.minor format) and ALPN negotiation. Per-feature revisions are tracked in the spec as a version manifest per release, not on the wire.

**FeatureMap Bits:**

```
bit 0  (0x0001): CORE      - EnergyCore basics (always set)
bit 1  (0x0002): FLEX      - FlexibilityStruct support
bit 2  (0x0004): BATTERY   - Battery attributes (SoC, SoH)
bit 3  (0x0008): EMOB      - E-Mobility/EVSE
bit 4  (0x0010): SIGNALS   - Incentive signals
bit 5  (0x0020): TARIFF    - Tariff data
bit 6  (0x0040): PLAN      - Power plan
bit 7  (0x0080): PROCESS   - Optional process lifecycle (OHPCF)
bit 8  (0x0100): FORECAST  - Power forecasting
bit 9  (0x0200): ASYMMETRIC - Per-phase asymmetric control
bit 10 (0x0400): V2X       - Vehicle-to-grid/home
```

**Discovery Flow:**
1. Read endpoint list to discover device structure
2. Read `specVersion` from DeviceInfo (endpoint 0) for protocol version
3. For each endpoint, read `featureMap` for quick capability check
4. Read `attributeList` for fine-grained attribute discovery
5. Read `acceptedCommandList` to know which commands work

**Rationale:**

*Why Matter-style:*
- Self-describing: Controller knows exactly what's available without trial/error
- Version-safe: `specVersion` in DeviceInfo provides protocol version; `attributeList` handles capability discovery (see DEC-050)
- Fine-grained: `attributeList` gives exact attribute availability
- Compact: Bitmap `featureMap` is efficient for quick checks
- Predictable: No implicit assumptions about device type implications

*Why not EEBUS nodeManagementUseCaseData:*
- EEBUS is coarse-grained (use case level, not attribute level)
- Use case "support" is ambiguous (which scenarios? which functions?)
- No version negotiation per use case
- Requires out-of-band spec lookup to know what attributes exist

*Why not trial and error:*
- Wastes roundtrips on error responses
- Error handling complexity
- No way to show user what device can do before trying

**Feature-Dependent Conformance:**

| Attribute | Mandatory If | Optional If |
|-----------|-------------|-------------|
| `stateOfCharge`, `stateOfHealth` | BATTERY | - |
| `sessionEnergy`, `evseState` | EMOB | - |
| `flexibility` | - | FLEX |
| `forecast` | - | FORECAST |
| `processState`, `optionalProcess` | PROCESS | - |
| Phase setpoint attributes | ASYMMETRIC | - |

**Declined Alternatives:**

- EEBUS use case discovery: Too coarse, no version handling, requires spec lookup
- Trial and error: Inefficient, poor UX, no proactive capability display
- Capability negotiation at connection time: Adds connection complexity

---

### DEC-036: Charging Mode and Responsibility Model

**Date:** 2025-01-25
**Status:** Accepted

**Context:** OSCEV 2.0 adds PV charging mode support - letting users choose optimization strategies (PV surplus only, PV with threshold, fast charging). This raises questions about:
- Where does charging mode configuration belong?
- Who controls what between CEM and EVSE?
- How to handle EV-specific timing constraints?

**Options Evaluated:**

1. **CEM controls everything**: CEM sets mode and timing, EVSE just executes
2. **EVSE controls everything**: EVSE handles optimization based on hints from CEM
3. **Split responsibility**: CEM sets goals, EVSE implements using domain knowledge

**Decision:** Split responsibility with "CEM suggests, EVSE decides within safe bounds" pattern.

**Additions to ChargingSession Feature:**

```cbor
// CHARGING MODE (optimization strategy)
70: chargingMode,            // ChargingModeEnum: active optimization strategy
71: supportedChargingModes,  // ChargingModeEnum[]: modes EVSE supports
72: surplusThreshold,        // int64 mW?: threshold for PV_SURPLUS_THRESHOLD

// START/STOP DELAYS (CEM can override, EVSE enforces)
80: startDelay,              // uint32 s: delay before (re)starting charge
81: stopDelay,               // uint32 s: delay before pausing charge
```

**ChargingModeEnum:**
```
OFF                   = 0x00  // No optimization, charge at maximum rate
PV_SURPLUS_ONLY       = 0x01  // Only self-produced energy, no grid
PV_SURPLUS_THRESHOLD  = 0x02  // Allow grid if surplus >= surplusThreshold
PRICE_OPTIMIZED       = 0x03  // Optimize based on price signals
SCHEDULED             = 0x04  // Follow time-based schedule/plan
```

**Responsibility Model:**

| Domain | Owner | Responsibility |
|--------|-------|----------------|
| System optimization | CEM/EMS | Goals, prices, grid constraints, PV forecasts |
| EV behavior | EVSE | Protocol handling, timing, hardware limits |

- **CEM sets** charging mode and can override delays
- **EVSE validates** requests against EV/hardware constraints
- **EVSE enforces** behavior using its domain knowledge
- **EVSE reports** active mode, constraints, and deviations
- EVSE may **reject** values that would harm the EV

**Rationale:**

*Why split responsibility:*
- Each party contributes expertise from their domain
- CEM doesn't need to know EV-specific timing details
- EVSE can protect EV from harmful requests
- Similar to failsafe pattern already in EnergyControl

*Why start/stop delays:*
- Prevents EVs from stopping completely due to frequent PV-induced interruptions
- EVSE knows connected EV's requirements
- CEM can override if system needs demand, EVSE still enforces safety

**EEBUS Coverage:**

| OSCEV 2.0 Scenario | MASH Mapping |
|-------------------|--------------|
| Scenario 5 - PV Charge Mode | chargingMode, surplusThreshold |
| Scenario 6 - Start/Stop Delays | startDelay, stopDelay |

**Declined Alternatives:**

- CEM controls everything: CEM lacks EV-specific knowledge
- EVSE controls everything: Misses system-wide optimization opportunities
- Separate feature for charging mode: Overcomplicates; logically part of session

---

### DEC-036b: Dynamic Electrical Feature for Connected Devices

**Date:** 2025-01-25
**Status:** Accepted

**Context:** When an EV connects to an EVSE, the system's effective capability changes (e.g., 22kW EVSE + 7.4kW EV = 7.4kW effective max). Where should EV constraints (min/max power, min/max current) be reported?

**Options Evaluated:**

1. **ChargingSession**: Add `evMinChargingPower`, `evMaxChargingPower`, etc.
2. **EnergyControl**: Dynamic constraint fields
3. **Electrical**: Existing fields update when EV connects

**Decision:** Electrical feature is dynamic and reflects current system capability.

**Design:**

```
┌─────────────────────────────────────────────────────────────┐
│ Electrical (capability)                                     │
│   → "What CAN this endpoint do right now?"                  │
│   → Dynamic: updates when connected device changes          │
│   → nominalMaxConsumption, maxCurrentPerPhase, etc.         │
├─────────────────────────────────────────────────────────────┤
│ EnergyControl (policy)                                      │
│   → "What SHOULD this endpoint do?"                         │
│   → CEM-set limits and setpoints                            │
│   → Must be within Electrical's envelope                    │
└─────────────────────────────────────────────────────────────┘
```

**Rationale:**

*Why Electrical:*
- It's truly a capability change, not a policy change
- "What CAN this endpoint do" naturally includes connected devices
- No field duplication across features
- CEM subscribes to Electrical, sees capability changes automatically
- Clean separation: capability vs policy

*Why not ChargingSession:*
- Would duplicate min/max fields that already exist in Electrical
- CEM would have to calculate effective range itself
- Mixes session state with system capability

*Why not EnergyControl:*
- EnergyControl is for CEM-set policy, not physical capability
- Would blur the line between "what's possible" and "what's allowed"

**Example flow:**
1. EVSE reports Electrical with hardware limits (22kW, 32A)
2. EV connects with 7.4kW/16A max, 1.4kW/6A min
3. EVSE updates Electrical: `nominalMaxConsumption=7400000`, `maxCurrentPerPhase=16000`, `nominalMinPower=1400000`
4. CEM (subscribed) sees change, adjusts limits accordingly
5. EV disconnects → Electrical returns to EVSE hardware limits

---

### DEC-037: Two-Level Capability Discovery Pattern

**Date:** 2025-01-25
**Status:** Accepted

**Context:** With multiple capability flags across features (asymmetric support, V2X, bidirectional, charging modes), controllers need efficient discovery. Should all details be in featureMap bits, or distributed between featureMap and attributes?

**Options Evaluated:**

1. **Fine-grained featureMap**: Separate bits for every capability variant (ASYMMETRIC_CHARGING, ASYMMETRIC_DISCHARGING, ASYMMETRIC_BIDIRECTIONAL, etc.)
2. **Coarse-grained featureMap**: High-level bits (ASYMMETRIC) with details in attributes
3. **No featureMap**: All discovery via attribute reading

**Decision:** Two-level capability discovery with 32-bit featureMap.

**Pattern:**
- **FeatureMap** (32-bit bitmap) = high-level category check ("does it support this?")
- **Feature attributes** = detailed capability information ("how exactly?")

**FeatureMap Bits (high-level):**
```
ASYMMETRIC (0x0200) → "Supports per-phase control"
V2X (0x0400)        → "Supports bidirectional EV"
EMOB (0x0008)       → "Has EV charging"
```

**Detailed Information (in attributes):**
```
Electrical.supportsAsymmetric: NONE/CONSUMPTION/PRODUCTION/BIDIRECTIONAL
Electrical.supportedDirections: CONSUMPTION/PRODUCTION/BIDIRECTIONAL
ChargingSession.supportedChargingModes: [OFF, PV_SURPLUS_ONLY, ...]
ChargingSession.evDemandMode: DYNAMIC_BIDIRECTIONAL
```

**Discovery Flow:**
1. Read `featureMap` → quick check what categories are supported
2. Read relevant feature attributes → get specific capability details

**Example - V2H EVSE with asymmetric charging but symmetric discharging:**
- `featureMap`: CORE | EMOB | ASYMMETRIC | V2X (quick: "yes it does V2X and asymmetric")
- `Electrical.supportsAsymmetric = CONSUMPTION` (detail: "but only for charging")
- `Electrical.supportedDirections = BIDIRECTIONAL` (detail: "can charge and discharge")

**Rationale:**

*Why 32-bit:*
- Aligned with Matter (BITMAP32)
- CBOR encodes small values efficiently anyway
- Room for future expansion (currently 11 bits used)

*Why two levels:*
- FeatureMap for quick filtering ("show me all V2X capable devices")
- Attributes for accurate capability matching ("can it do asymmetric V2G?")
- Prevents combinatorial explosion of featureMap bits
- Detailed enums are more expressive than boolean bits

*Why not all details in featureMap:*
- ASYMMETRIC × V2X × directions = many combinations
- Bits are limited (32), enums are unlimited
- Easier to add new enum values than new featureMap bits

**Declined Alternatives:**

- Fine-grained featureMap: Combinatorial explosion, quickly runs out of bits
- No featureMap: Requires reading many attributes for basic filtering
- 16-bit featureMap: Works for now but limits future expansion

---

### DEC-038: Command Parameters vs Stored Attributes

**Date:** 2025-01-25
**Status:** Accepted

**Context:** Commands like `SetLimit` have parameters like `duration` that control behavior. Question: Are these stored and readable like attributes, or transient like Matter's command parameters?

**Protocol Comparison:**

| Protocol | Approach |
|----------|----------|
| **EEBUS** | All function data is stored and readable. Writing to a function persists all fields. No concept of "transient parameters." |
| **Matter** | Commands have parameters that control execution but aren't stored. `transitionTime` in LevelControl is passed at invocation but not readable as an attribute. |

**Options Evaluated:**

1. **EEBUS model**: Store all command parameters as readable attributes (e.g., `limitDuration`, `limitCause`)
2. **Matter model**: Command parameters are transient - control behavior but aren't persisted
3. **Hybrid**: Some parameters stored, some transient

**Decision:** Matter model - command parameters are transient, not stored attributes.

**Design:**

| Concept | Behavior | Example |
|---------|----------|---------|
| **Attribute** | Stored, readable, subscribable | `myConsumptionLimit`, `effectiveConsumptionLimit` |
| **Command parameter** | Transient, not readable, controls execution | `duration`, `cause` |

**After SetLimit(consumptionLimit: 5000000, duration: 60, cause: LOCAL_PROTECTION):**
- `myConsumptionLimit` = 5000000 (readable, subscribable)
- `duration` = not accessible (internal timer running)
- `cause` = not accessible (logged but not queryable)

**Implications:**

1. **No "remaining duration" attribute**: Timer is internal, not exposed
2. **Controller tracks expiry locally**: If controller needs to know, it calculates `now + duration`
3. **To change duration**: Re-send entire SetLimit command
4. **To remove timer**: Send SetLimit with `duration: 0` or omit duration
5. **null invalid for command parameters**: Use omission for defaults, not null

**Omitting Optional Parameters:**

| In command | Meaning |
|------------|---------|
| Key absent | Use default value |
| Key with value | Use this value |
| Key with `null` | Invalid - don't use null for command parameters |

**Rationale:**

*Why Matter model over EEBUS model:*
- **Simpler device implementation**: Don't need to store/manage every parameter
- **Clearer semantics**: Attributes are state, commands are actions
- **Less state to synchronize**: Transient parameters don't need notification
- **Controller owns scheduling context**: Controller set the duration, controller knows when it expires
- **Matter-aligned**: Familiar pattern for developers

*Why no remaining duration attribute:*
- Would require continuous updates or on-demand calculation
- Adds complexity for marginal benefit
- Controller can track locally (it set the duration)
- Other zones don't need to know when a limit expires - they only care about the effective limit value

**Test Implications:**
- TC-ZONE-LIMIT-6c: Verify duration is NOT readable as an attribute
- TC-ZONE-LIMIT-6a: Verify re-sending command replaces timer

**Declined Alternatives:**

- EEBUS model (all stored): Adds complexity, unclear benefit for transient control parameters
- Hybrid model: Complicates the mental model - either it's state or it's control, not both
- Adding `remainingDuration` attribute: Continuous change or on-demand calculation adds complexity

---

### DEC-039: State Machine Interaction Rules

**Date:** 2025-01-25
**Status:** Accepted

**Context:** MASH has two orthogonal state machines: ControlStateEnum (controller relationship) and ProcessStateEnum (optional task lifecycle). The testability analysis identified that interaction rules were unspecified - what happens to ProcessState when ControlState changes?

**Key Scenarios:**

| Scenario | Question |
|----------|----------|
| Connection lost during RUNNING process | Does process pause or continue? |
| Connection lost during PAUSED process | Does it stay paused or auto-resume on failsafe expiry? |
| Failsafe expires during SCHEDULED process | Does scheduled process still start? |
| Reconnection during active process | Who owns the process? |

**Decision:** Process lifecycle continues independently of ControlState transitions.

**Rules:**

1. **RUNNING processes continue during FAILSAFE**
   - Process operates under failsafe limits (more restrictive)
   - Process can complete during FAILSAFE
   - Rationale: Safer to complete (e.g., dishwasher cycle) than leave mid-operation

2. **SCHEDULED processes start as planned during FAILSAFE**
   - Device starts process at scheduledStart without controller confirmation
   - Process runs under failsafe limits
   - Rationale: User expectation is scheduled task runs

3. **PAUSED processes remain paused during FAILSAFE**
   - Device-specific behavior on failsafe expiry (PICS item)
   - Conservative devices: stay paused
   - Aggressive devices: auto-resume
   - PICS: `MASH.S.CTRL.B_PAUSED_AUTO_RESUME`

4. **Reconnection restores control, doesn't cancel**
   - Controller sees process state in subscription priming
   - Controller can interact (Pause, Cancel) but process continues by default
   - Process ownership persists across disconnection

**FAILSAFE Timing Precision:**

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| Maximum detection delay | 95 seconds | 3 missed pongs @ 30s + 5s timeout |
| Failsafe timer accuracy | +/- 1% | 24h failsafe = +/- 14 minutes |
| Failsafe limit application | Immediate | Safety-critical, no gradual ramp |
| Reconnection race window | 5 seconds | If handshake completes within 5s of timer, reconnection wins |

**Rationale:**

*Why continue processes:*
- User scheduled the task expecting it to run
- Mid-operation abort can cause problems (wet laundry, half-heated water)
- Failsafe limits provide grid safety regardless
- Controller regains control on reconnection

*Why PAUSED is device-specific:*
- Paused implies intentional stop - conservative to maintain that
- But some devices may want to resume after extended disconnect
- Make it explicit via PICS so controllers know what to expect

**Test Cases:**
- TC-INTERACTION-1 through TC-INTERACTION-15 cover all interaction scenarios
- TC-STATE-5 through TC-STATE-9 cover FAILSAFE behavior
- TC-PROCESS-* cover ProcessState transitions

**Documentation:** See `docs/testing/behavior/state-machines.md` for complete specification.

---

### DEC-040: Device Identity via Certificate Fingerprint

**Date:** 2026-01-26
**Status:** Proposed

**Context:** Need a stable, verifiable device identity for:
- Reconnection after restarts (controller matches device)
- Certificate pinning/validation
- Persistence of commissioned device relationships

**Options Evaluated:**
1. Subject Key Identifier (SKI) - 20-byte hash of public key
2. Full certificate fingerprint - SHA-256 hash of entire certificate
3. PASE-derived ID - hash of SPAKE2+ shared secret

**Decision:** Full certificate fingerprint (SHA-256)

**Rationale:**
- More collision-resistant than SKI (32 bytes vs 20 bytes)
- Covers entire certificate content, not just public key
- Standard practice for certificate pinning
- If certificate changes (renewal, rotation), identity changes appropriately
- Easy to compute: `sha256(certificate.Raw)` in Go

**Implementation:**
- Device generates/loads persistent TLS certificate on startup
- Device ID = hex-encoded SHA-256 of DER-encoded certificate
- Controller stores device's certificate fingerprint during commissioning
- On reconnection, controller verifies fingerprint matches

**Declined Alternatives:**
- SKI: Smaller but less collision-resistant, only covers public key
- PASE-derived: Different for each commissioning session, not tied to certificate

**Related:** OPEN-002 (Certificate and Session Details)

---

### DEC-041: Bidirectional Feature Access and Topology-Based Permissions

**Date:** 2026-01-27
**Status:** Accepted

**Context:** Need to support hierarchical controller scenarios where:
1. SMGW (Grid Operator) controls an EMS by setting grid power limits
2. EMS controls downstream devices (EVSE, heat pump, battery)
3. EMS can read SMGW's grid meter values to optimize within grid limits

This raised questions about:
- Can both sides of a connection expose and access features?
- What permission model governs who can access what?
- How do we handle a device in multiple controller zones?

**Options Evaluated:**

1. **Zone-Type Priority Inheritance**
   - Higher priority zones inherit all permissions of lower priority
   - Simple but too coarse-grained

2. **Feature-Declared Permissions (ACL)**
   - Each feature declares which zone types can Read/Write/Subscribe/Invoke
   - Fine-grained but verbose configuration overhead

3. **Capability Grants in Certificate**
   - Certificates include explicit capability grants
   - Maximum flexibility but complex certificate management

4. **Hybrid (Defaults + Feature Overrides)**
   - Zone-type defaults with per-feature exceptions
   - Good balance but adds permission layer complexity

5. **Topology-Based Implicit Permissions**
   - Commissioning defines access; no explicit permission model
   - Simplest approach if topology naturally enforces access patterns

**Decision:** Topology-Based Implicit Permissions (Option 5)

**Rationale:**

The key insight is that **commissioning IS authorization**. The network topology naturally enforces access control:

1. **Controllers control devices in their zone** - If you commissioned a device, you can control it (Read/Write/Subscribe/Invoke). This is implicit.

2. **Devices connect only to controllers** - Devices cannot connect to other devices. There is no device-to-device communication path.

3. **Dual-service entities** - An EMS that needs to both control devices AND be controlled runs two separate services:
   - A ControllerService (manages its own zone with downstream devices)
   - A DeviceService (can be commissioned into another controller's zone)

   These services are independent. Devices in the EMS's zone cannot see or access the EMS's device-side features - they're on different connections entirely.

4. **Multi-zone conflict resolution already exists** - When a device belongs to multiple zones (e.g., EVSE in both SMGW and EMS zones), the existing rules apply:
   - Limits: Most restrictive wins (DEC-024)
   - Setpoints: Highest priority zone wins

**Example Architecture:**

```
SMGW (Smart Meter Gateway)
├── ControllerService (GRID zone)
│   └── controls: EMS-DeviceService
│       └── Can write grid limits to EMS
│
└── DeviceService (exposes grid meter)
    └── commissioned into: EMS zone
        └── EMS can read meter values

EMS (Energy Management System)
├── ControllerService (LOCAL zone)
│   ├── controls: SMGW-DeviceService (meter) → reads grid meter
│   ├── controls: EVSE
│   └── controls: Heat Pump
│
└── DeviceService (exposes GRID_CONNECTION aggregate)
    └── commissioned into: SMGW zone
        └── SMGW can write limits, read aggregate
```

**Wire Protocol Implications:**

The wire protocol already supports bidirectional message flow (audit confirmed):
- Message framing is symmetric
- No role/direction fields in messages
- MessageID spaces are independent per side

Service layer changes needed:
- Sessions must handle all message types (Request, Response, Notification)
- Both sides can send Read/Write/Subscribe/Invoke
- No permission checks needed beyond "is this a valid commissioned connection?"

**What We DON'T Need:**

| Eliminated Complexity | Reason |
|----------------------|--------|
| Explicit permission model | Topology defines access |
| Feature-level ACLs | All features accessible to commissioned controller |
| Zone-type permission matrix | Commissioning grants full access |
| ExposeToRemote flags | Features visible on connection, no filtering |
| Capability certificates | Zone type sufficient for conflict resolution |

**What We DO Need:**

| Requirement | Status |
|-------------|--------|
| Multi-zone limit resolution | Already implemented (DEC-024) |
| Multi-zone setpoint resolution | Already implemented |
| Dual-service support | Application-level composition |
| Bidirectional message handling | Service layer refactoring needed |

**Declined Alternatives:**

- **Feature ACLs**: Unnecessary complexity when topology already constrains access
- **Certificate capabilities**: Over-engineering for current use cases
- **Zone-type permission matrix**: Adds a layer that topology already provides

**Key Principle:** The act of commissioning a device into your zone IS the authorization. No additional permission layer required.

**Related:** DEC-024 (Limit Resolution), DEC-023 (Zone Terminology)

---

### DEC-042: Pairing Request Mechanism for Deferred Commissioning

**Date:** 2026-01-27
**Status:** Accepted

**Context:** The original commissioning window duration (120 seconds) is too short for real-world installation scenarios:

1. **Energy devices take time to install** - EVSE, heat pumps, inverters require significant setup before pairing
2. **SMGW commissioning is deferred** - QR code is scanned at installation, but SMGW may not receive credentials for days or weeks
3. **Matter's approach insufficient** - Even Matter's 48-hour extended window doesn't solve the SMGW scenario

The EEBUS SHIP Pairing Service was analyzed as a reference. It uses a separate `_shippairing` mDNS service where the controller (devZ) announces a pairing request, and the device (devA) listens and responds. This inverts the traditional "device opens window, controller connects" model.

**Options Evaluated:**

1. **Longer commissioning window (Matter-style)**
   - Extend window to 48 hours for uncommissioned devices
   - Problem: Still doesn't solve weeks-long SMGW delays
   - Problem: Continuous advertising wastes resources

2. **Always-open window until first commissioning**
   - Device stays in commissioning mode indefinitely
   - Problem: No way to re-commission for additional zones
   - Problem: Security concerns (window never closes)

3. **Pairing request mechanism (EEBUS-inspired)**
   - Controller announces "I want to pair with device X"
   - Device listens, opens window when it sees matching request
   - Solves arbitrary delay scenarios
   - Unified mechanism for all commissioning scenarios

4. **Authenticated pairing request (HMAC)**
   - Add HMAC to pairing request using a separate secret
   - Problem: Low-entropy passcode enables offline brute-force if used for HMAC
   - Problem: Separate secret adds QR code complexity

**Decision:** Pairing Request Mechanism (Option 3) without authentication (Option 4 rejected)

**Rationale:**

The pairing request is a "wake up" signal, not an authentication mechanism. Security is provided by PASE:

1. **PASE authenticates commissioning** - Only controllers with the correct passcode can complete PASE
2. **Physical access required** - Passcode comes from QR code on device
3. **Opening window is not a breach** - Attacker without passcode cannot complete commissioning
4. **Equivalent to Matter security** - Matter opens windows without HMAC; PASE is the gatekeeper

**Design:**

New service type: `_mashp._udp` (pairing request)

| TXT Key | Description |
|---------|-------------|
| `D` | Target discriminator (which device should respond) |
| `ZI` | Zone ID (who is requesting) |
| `ZN` | Zone name (optional, for display) |

**Device behavior:**
- Listen for `_mashp._udp` when uncommissioned or accepting additional zones
- When matching discriminator seen: open commissioning window, start advertising `_mashc._udp`
- Default window duration: 3 hours (configurable 1-24 hours)

**Controller behavior:**
- Browse `_mashc._udp` first
- If device not found: announce `_mashp._udp` with target discriminator
- Continue until device appears or timeout

**Commissioning Flow:**

```
Standard:
1. Scan QR → discriminator + passcode
2. Browse _mashc._udp → find device
3. Connect, PASE with passcode

Deferred (SMGW scenario):
1. QR scan → data to backend → [days/weeks] → SMGW provisioned
2. SMGW browses _mashc._udp → NOT found
3. SMGW announces _mashp._udp with D=<discriminator>
4. Device sees request → opens window → advertises _mashc._udp
5. SMGW discovers device → connect, PASE with passcode
```

**Commissioning Window Duration:**

Changed default from 120 seconds to 3 hours because:
- Energy devices (EVSE, heat pumps) require significant installation time
- Pairing rarely happens immediately after power-on
- 3 hours accommodates realistic installer workflows
- Configurable range: 1-24 hours

**What We Avoided:**

| Rejected Complexity | Reason |
|--------------------|--------|
| Separate pairing secret | PASE already authenticates; adds QR complexity |
| HMAC on pairing request | Low-entropy passcode enables offline brute-force |
| Complex state machine | Simple "listen and respond" model sufficient |
| Window expiration events | Device just stops advertising when window closes |

**Related:** DEC-040 (Device Identity), [EEBUS SHIP Pairing Service Spec]

---

### DEC-043: One Zone Per Zone Type Constraint

**Date:** 2026-01-27
**Status:** Accepted

**Context:** With the simplified zone type model (GRID, LOCAL), we need to define how many zones a device can belong to.

The original design allowed up to 5 zones per device. However, this creates complexity:
- Multiple SMGWs controlling the same device (whose limits apply?)
- Multiple EMSs controlling the same device (conflicting optimization strategies)
- Complex multi-zone conflict resolution logic

**Decision:** A device can belong to at most **one zone per zone type**:
- Max 1 GRID zone (typically SMGW/grid operator)
- Max 1 LOCAL zone (typically EMS/home app)
- **Total: 2 zones maximum per device**

**Rationale:**

1. **GRID zones are regulatory** - A device should only be subject to one grid operator's control. Multiple grid authorities would create conflicting requirements.

2. **LOCAL zones optimize** - One EMS should manage local energy optimization. Multiple EMSs would have competing strategies.

3. **Simplifies conflict resolution** - With only 2 zones (one per type), the rules are clear:
   - GRID limits always apply (safety/regulatory)
   - LOCAL optimizes within GRID constraints
   - No need for complex N-way conflict resolution

4. **Matches real-world topology** - A device is typically at one grid connection point (one SMGW) and managed by one local system (one EMS).

**Enforcement:**

When a device receives a commissioning request:
1. Check if a zone of the same type already exists
2. If yes: reject commissioning (device already has a zone of this type)
3. If no: proceed with commissioning

**What This Replaces:**

| Old | New |
|-----|-----|
| MaxZones = 5 | MaxZones = 2 |
| Any combination of zones | One GRID + one LOCAL |
| Complex N-way conflict resolution | Simple two-zone priority |

**Migration:** Devices with >2 zones would need to decommission excess zones. This is unlikely in practice since the old limit was rarely used.

**Related:** DEC-024 (Limit Resolution), DEC-023 (Zone Terminology)

---

### DEC-044: Message Correlation and Request Timeouts

**Date:** 2026-01-28
**Status:** Accepted

**Context:** The protocol needs clear rules for message correlation (matching responses to requests) and handling unresponsive connections.

**Questions Addressed:**
1. How does a client correlate responses to requests?
2. What happens when MessageID wraps around (uint32 overflow)?
3. How long should a client wait for a response?

**Decisions:**

**1. Message Correlation via MessageID**

Every request includes a `messageId` (uint32). The response echoes this value:
- Request: `{1: 12345, 2: 1, 3: 1, 4: 2, 5: [...]}` (messageId=12345)
- Response: `{1: 12345, 2: 0, 3: {...}}` (same messageId)

This is an implicit ACK/NACK system:
- Response with Status=0 → ACK (success)
- Response with Status≠0 → NACK (error with reason code)
- No separate ACK message type needed

**2. MessageID Space**

| Property | Value |
|----------|-------|
| Range | 1 to 4,294,967,295 |
| Reserved | 0 (indicates notification) |
| Scope | Per-connection, per-direction |
| Overflow behavior | Wrap to 1 (skip 0) |

**Why 32-bit instead of 64-bit:**
- Target hardware is 32-bit MCUs (DEC-003)
- CBOR: 32-bit = 5 bytes, 64-bit = 9 bytes per message
- Matter uses 32-bit counters
- With 10s timeout, even 10,000 req/s only has ~100K in-flight (vs 4.3B available)

**3. Request Timeout**

| Parameter | Value |
|-----------|-------|
| Default timeout | 10 seconds |

Timeout behavior:
1. No response within 10s → connection is broken
2. Client SHOULD close and reconnect
3. Client MUST re-send pending requests after reconnection

**Rationale:**
- Devices MUST respond to every request (the protocol has no "fire and forget")
- A missing response means the connection failed, not that the device is slow
- 10 seconds allows for slow operations while maintaining responsiveness

**Comparison:**

| Protocol | Correlation | Timeout |
|----------|------------|---------|
| MASH | MessageID (uint32) | 10 seconds |
| Matter | Transaction ID (uint32) | Configurable per interaction |
| EEBUS SHIP | Complex listHeader + headerFields | Heartbeat-based |

**Related:** DEC-003 (Target Hardware), DEC-008 (Interaction Model)

---

### DEC-045: Transport-Layer Heartbeat Sufficient for Failsafe

**Date:** 2026-01-28
**Status:** Accepted

**Context:** EEBUS LPC and LPP use application-layer heartbeats (DeviceDiagnosis) with 60-second intervals, bidirectionally between Energy Guard and Controllable System. MASH has transport-layer ping/pong (30s interval, 95s max detection). Question: Does MASH need additional application-layer heartbeats for failsafe triggering?

**EEBUS LPC/LPP Heartbeat Model:**

| Parameter | Value |
|-----------|-------|
| Heartbeat interval | ≤60 seconds |
| Failsafe trigger | 120 seconds without heartbeat |
| Direction | Bidirectional (EG↔CS) |
| Purpose | Liveness + gates command processing |

EEBUS requires heartbeat before evaluating commands: "only after a Heartbeat from the Energy Guard, a following received write command within 60 seconds on the Active Power Limit SHALL be evaluated."

**EEBUS Protocol Stack:**
```
┌─────────────────────────────────┐
│ Application: LPC/LPP Heartbeat  │  ← 60s, bidirectional
├─────────────────────────────────┤
│ SHIP layer                      │
├─────────────────────────────────┤
│ WebSocket: ping/pong            │  ← transport heartbeat EXISTS
├─────────────────────────────────┤
│ TLS                             │
└─────────────────────────────────┘
```

Note: EEBUS already has transport-layer liveness via WebSocket ping/pong, yet still requires application-layer heartbeats.

**Matter Approach:**

Matter uses subscription liveness (MaxInterval + RTT) for application health, with MRP (Message Reliability Protocol) at transport layer. No feature-specific heartbeats. ICD Check-In exists only for battery-powered devices that sleep for extended periods.

**Options Evaluated:**

1. **EEBUS model**: Add bidirectional application-layer heartbeats, gate commands on heartbeat receipt
2. **Feature-specific heartbeats**: Different heartbeats for different features (as EEBUS does with LPC, LPP, emobility)
3. **Transport-only**: Rely on transport ping/pong for liveness, no application heartbeats
4. **Subscription MaxInterval**: Use existing subscription mechanism as implicit application heartbeat

**Decision:** Transport-layer heartbeat is sufficient. No application-layer heartbeats needed.

**MASH Liveness Mechanisms:**

| Layer | Mechanism | What it proves |
|-------|-----------|----------------|
| Transport | Ping/pong (30s, 95s max) | TLS connection alive |
| Application | Subscription MaxInterval | Device app processing subscriptions |
| Application | Request/Response (10s timeout) | End-to-end command processing |

**Rationale:**

1. **Command arrival IS proof of liveness.** If a controller can send a Write command, it was alive to send it. Requiring a separate heartbeat before the command adds no information.

2. **Transport heartbeat detects connection failure.** The 95s maximum detection time is acceptable for failsafe purposes. Connection loss triggers FAILSAFE state (DEC-034).

3. **Subscription MaxInterval already serves as device-side heartbeat.** Periodic reports prove the device application is processing. Controllers can set MaxInterval to get regular updates.

4. **Feature-specific heartbeats are redundant.** If the stack can send an LPC heartbeat, it can send an LPC command. Different heartbeats for different features (EEBUS pattern) multiply complexity without adding safety.

5. **EEBUS redundancy is a design flaw, not a feature.** EEBUS has WebSocket ping/pong at transport layer AND application heartbeats. This exists because LPC/LPP were designed by different working groups without clean layering.

6. **Zombie controller edge case is acceptable.** If controller app hangs but TCP stays open, device continues applying last commanded limit. This is safe because:
   - Commanded limits represent explicit controller intent
   - failsafeDuration (2-24h) provides eventual recovery
   - In practice, app crashes usually terminate TCP too

**Edge Case Documentation:**

> If a controller application hangs while the TCP connection remains open, the device will continue applying the last commanded limit until TCP timeout occurs or failsafeDuration expires. This is acceptable because commanded limits represent explicit controller intent, and failsafeDuration provides guaranteed recovery.

**Comparison:**

| Aspect | EEBUS LPC/LPP | MASH |
|--------|---------------|------|
| Transport heartbeat | WebSocket ping/pong | TLS ping/pong (30s) |
| Application heartbeat | DeviceDiagnosis (60s, bidirectional) | None required |
| Failsafe trigger | 120s without app heartbeat | Connection loss (95s max) |
| Command gating | Requires heartbeat first | Commands evaluated immediately |
| State reporting | Inferred from heartbeats | Explicit ControlStateEnum |

**Why EEBUS Requires Both Layers:**

EEBUS uses heartbeats for **semantic gating**, not just liveness: commands are only evaluated after receiving a heartbeat. This pattern was designed to detect "zombie controllers" (app crashed, connection alive). However:

- The gating doesn't prove the controller computed the right limit
- If the controller can send a heartbeat, it can send a command
- MASH's explicit state reporting (DEC-034) eliminates heartbeat-based state inference

**Declined Alternatives:**

- **EEBUS bidirectional heartbeats**: Redundant with transport layer, adds protocol complexity
- **Feature-specific heartbeats**: Multiplies heartbeat traffic without adding safety
- **Heartbeat-gated commands**: If controller can send heartbeat, it can send command - no benefit

**Related:** DEC-034 (ControlStateEnum), DEC-039 (State Machine Rules), DEC-044 (Request Timeouts)

---

### DEC-047: Commissioning Security Hardening

**Date:** 2026-01-29
**Status:** Proposed

**Context:** Security analysis of the commissioning process identified several
hardening opportunities, particularly around DoS protection during the
commissioning window when the device is most vulnerable.

**Options Evaluated:**

1. **No changes** - Accept current design as sufficient
   - Pro: Simpler, no breaking changes
   - Con: Leaves DoS vectors unaddressed

2. **Arbitrary connection limits** - e.g., max 3 commissioning connections
   - Pro: Simple to implement
   - Con: Number is arbitrary, not derived from device capabilities

3. **Zone-based connection model** - Derive limits from zone capacity (CHOSEN)
   - Pro: Logically consistent, resource-bounded, predictable
   - Con: Slightly more complex tracking

**Decision:** Implement zone-based connection model with comprehensive hardening:

**Connection Model:**
- Max operational connections = max_zones (one per paired zone)
- Max commissioning connections = 1 (single concurrent commissioning)
- Total max connections = max_zones + 1
- When zones full: reject commissioning (no advertisement, TLS rejection)

**Additional Hardening:**
- PASE attempt tracking with exponential backoff
- Nonce binding for certificate renewal
- Generic error responses to prevent information leakage
- Explicit handshake timeouts (85s overall)
- Connection cooldown (500ms between completed attempts, starts at release)

**Rationale:**
- Connection limits derived from zone capacity are logically defensible
- No wasted effort on commissioning that would fail with ZONE_FULL
- Operational connections never blocked by commissioning activity
- Single commissioning connection minimizes attack surface
- MCU devices (256KB RAM) benefit from predictable resource usage
- Protocol-level fixes ensure all implementations benefit

**Declined Alternatives:**
- Memory-hard functions (Argon2): Excluded due to MCU constraints
- Certificate pinning: Deferred to future release
- Multiple concurrent commissioning: Increases attack surface without benefit

**Related:** DEC-034 (ControlStateEnum), DEC-039 (State Machine Rules)

---

### DEC-048: Commissioning Window Duration Alignment

**Status:** Accepted
**Date:** 2026-01-29

**Context:**

MASH originally used a 3-hour default commissioning window. Matter specification 5.4.2.3.1
uses 15 minutes maximum with 3 minutes minimum. With MASH's pairing request mechanism
(`_mashp._udp`), controllers can re-trigger commissioning on demand, making long windows
unnecessary.

**Decision:**

Align commissioning window default with Matter, with MASH-specific maximum:

| Parameter | Before | After | Matter |
|-----------|--------|-------|--------|
| Default | 3 hours | 15 minutes | 15 minutes |
| Minimum | 1 hour | 3 minutes | 3 minutes |
| Maximum | 24 hours | 3 hours | 15 minutes |

**Rationale:**
1. **Pairing request provides re-trigger**: `_mashp._udp` allows controllers to request
   commissioning window re-open at any time, eliminating need for long default windows
2. **Reduced attack surface**: Shorter window = less time for PASE brute-force attempts
3. **Spectrum conservation**: Reduces mDNS advertisement pollution on shared spectrum
4. **Matter alignment**: 15 minutes is well-researched for user experience balance
5. **Professional installer support**: 3-hour maximum (vs Matter's 15 min) allows longer
   setup workflows when explicitly configured

**Why MASH allows 3-hour max (vs Matter's 15 min):**
- Professional installer scenarios may require longer setup times
- Pairing request mechanism provides re-triggering if window expires
- No harm in allowing longer windows when explicitly requested

**Consequences:**
- Existing deployments with long setup workflows should use pairing requests
- Test cases using hardcoded 3-hour assumptions need updating
- Default user experience unchanged (pairing request handles window expiry)

**Related:** DEC-047 (Commissioning Security Hardening)

---

### DEC-049: LPC/LPP Use Case Support - Contractual Limits and Override Tracking

**Status:** Accepted
**Date:** 2026-01-29

**Context:**

Supporting the EEBUS LPC (Limitation of Power Consumption) and LPP (Limitation of Power Production) use cases for German EnWG §14a compliance. Analysis revealed:

1. LPC and LPP share identical state machines (init, limited, controlled, failsafe, autonomous)
2. `failsafeDuration` is a shared data point between LPC and LPP
3. EEBUS distinguishes hardware limits (devices) from contractual limits (EMS/CEM)
4. Override reasons differ by direction (self-protection for consumption, not production)

**Options Evaluated:**

1. Add contractual limits to Electrical feature
2. Add contractual limits to EnergyControl, keep nominal in Electrical (chosen)
3. Create new GridConnection feature
4. Separate control states per direction (rejected)

**Decision:** Option 2 - Add contractual limits to EnergyControl with unified control state

**Additions to EnergyControl:**
- `contractualConsumptionMax` (attr 73): Building's max allowed consumption (EMS only)
- `contractualProductionMax` (attr 74): Building's max allowed feed-in (EMS only)
- `overrideReason` (attr 75): Why device is in OVERRIDE state
- `overrideDirection` (attr 76): Which direction triggered override
- `OverrideReasonEnum`: SELF_PROTECTION, SAFETY, LEGAL_REQUIREMENT, UNCONTROLLED_LOAD, UNCONTROLLED_PRODUCER
- `LimitRejectReasonEnum`: BELOW_MINIMUM, ABOVE_CONTRACTUAL, INVALID_VALUE, DEVICE_OVERRIDE, NOT_SUPPORTED
- Enhanced `SetLimit` response with `applied`, `rejectReason`, `controlState`

**Rationale:**

1. **Nominal vs Contractual separation:**
   - Nominal max = hardware capability → belongs in Electrical (existing)
   - Contractual max = building's contract → belongs in EnergyControl (added)
   - These are mutually exclusive per EEBUS spec

2. **Unified control state (vs separate per direction):**
   - Single `controlState` shows "most concerning" state (FAILSAFE > OVERRIDE > LIMITED > CONTROLLED > AUTONOMOUS)
   - `overrideDirection` indicates which direction triggered override
   - Simpler for controllers; MASH zone architecture manages both directions together
   - EEBUS complexity (independent state machines) not needed

3. **Shared failsafeDuration:**
   - MASH already models this correctly as single value
   - Matches EEBUS [LPC-022] = [LPP-022]

4. **TLS replaces heartbeat:**
   - EEBUS uses explicit heartbeat (120s timeout)
   - MASH uses TLS connection state - simpler, more reliable
   - Already decided in DEC-045

**LPC/LPP Data Point Mapping:**

| EEBUS | MASH | Notes |
|-------|------|-------|
| LPC-011 Active Consumption Limit | `myConsumptionLimit` | Exists |
| LPP-011 Active Production Limit | `myProductionLimit` | Exists |
| LPC-021 Failsafe Consumption Limit | `failsafeConsumptionLimit` | Exists |
| LPP-021 Failsafe Production Limit | `failsafeProductionLimit` | Exists |
| LPC/LPP-022 Failsafe Duration | `failsafeDuration` | Shared, exists |
| LPC-041 Nominal Consumption Max | `Electrical.nominalMaxConsumption` | Device only |
| LPP-041 Nominal Production Max | `Electrical.nominalMaxProduction` | Device only |
| LPC-042 Contractual Consumption Max | `contractualConsumptionMax` | **Added**, EMS only |
| LPP-042 Contractual Production Max | `contractualProductionMax` | **Added**, EMS only |

**Declined Alternatives:**

- **Separate control states per direction:** Adds complexity without benefit; MASH's zone architecture means controllers typically manage both directions together
- **Contractual in Electrical:** Electrical is for hardware characteristics, not contract terms
- **New GridConnection feature:** Over-engineering; contractual limits fit naturally in EnergyControl

**Related:** DEC-026 (EnergyControl design), DEC-045 (Failsafe via TLS)

---

### DEC-050: Protocol Versioning Strategy

**Date:** 2026-01-30
**Status:** Accepted

**Context:** OPEN-004 identified version negotiation as an unresolved gap. SPINE's lack of version negotiation causes real interoperability problems. MASH needs a clear versioning strategy that covers:
- How to negotiate the protocol version between controller and device
- How to handle differences in feature capabilities
- How to evolve the protocol without breaking existing deployments

The original design (DEC-035) adopted Matter-style `clusterRevision` as a per-feature global attribute. This works well for Matter, which has hundreds of clusters across independent domains evolving on separate timelines. MASH has 6 tightly coupled features in a single energy domain, making per-feature version tracking unnecessarily complex.

**Options Evaluated:**

1. **Per-feature clusterRevision (Matter-style):** Each feature advertises its own revision number as a global attribute (0xFFFD). Controllers check each feature's revision independently.
2. **Single protocol version only:** One version number (e.g., "1.1") covers all features. No per-feature revision on the wire.
3. **Protocol version on the wire, feature revisions in the spec:** A single protocol version is advertised on the wire. The spec internally tracks per-feature revisions and defines which feature revisions are bundled into each protocol release.

**Decision:** Option 3 - Single protocol version on the wire with per-feature revisions in the spec.

**Protocol Version Format:** `major.minor` (e.g., "1.0", "1.1", "2.0")
- **Major version:** Breaking changes (removing fields, changing semantics). Carried in ALPN string (`mash/1`, `mash/2`).
- **Minor version:** Additive, non-breaking changes (new optional fields, new enum values, new features). Carried in `specVersion` attribute of DeviceInfo.

**Version Manifest (spec-side):**

Each protocol release defines exactly which feature revisions it includes:

```
MASH 1.0:
  DeviceInfo       rev 1
  Electrical       rev 1
  Measurement      rev 1
  EnergyControl    rev 1
  Status           rev 1
  ChargingSession  rev 1
```

Future releases update specific features:

```
MASH 1.1 (example):
  DeviceInfo       rev 1    (unchanged)
  Electrical       rev 2    (added asymmetric phase config)
  Measurement      rev 1    (unchanged)
  EnergyControl    rev 2    (new control mode)
  Status           rev 1    (unchanged)
  ChargingSession  rev 1    (unchanged)
```

**Wire Protocol Changes (relative to DEC-035):**

| Attribute | Before | After |
|-----------|--------|-------|
| `clusterRevision` (0xFFFD) | Per-feature global attribute | **Removed** |
| `featureMap` (0xFFFC) | Per-feature global attribute | Unchanged |
| `attributeList` (0xFFFB) | Per-feature global attribute | Unchanged |
| `acceptedCommandList` (0xFFFA) | Per-feature global attribute | Unchanged |
| `generatedCommandList` (0xFFF9) | Per-feature global attribute | Unchanged |
| `eventList` (0xFFF8) | Per-feature global attribute | Unchanged |
| `specVersion` (DeviceInfo attr 12) | Device spec version | **Becomes the single protocol version** |

**Version Negotiation Flow:**

1. **Connection:** Controller offers ALPN strings for all major versions it supports (e.g., `["mash/2", "mash/1"]`). Device selects the highest mutually supported major version. If no overlap, connection fails.
2. **Discovery:** After connection, controller reads `specVersion` from DeviceInfo (endpoint 0) to learn the exact major.minor version.
3. **Capability check:** Controller uses `featureMap` and `attributeList` to discover the device's actual capabilities. This is more precise than version comparison and handles partial implementations.

**Compatibility Rules:**

- **Same major, higher minor on device:** Controller uses `attributeList` to discover new attributes. Unknown attributes are ignored (forward-compat rules from DEC-035 apply).
- **Same major, lower minor on device:** Controller checks `attributeList` to confirm required attributes exist. Degrades gracefully if newer attributes are absent.
- **Different major:** Handled at ALPN level. No connection if incompatible.

**Rationale:**

*Why not per-feature clusterRevision (Option 1):*
- MASH has 6 features in one domain, not hundreds across independent domains like Matter
- Features are tightly coupled (EnergyControl references Electrical's topology, Measurement depends on Electrical's config)
- Per-feature revisions create a combinatorial compatibility matrix (does EnergyControl rev 2 work with Measurement rev 1?)
- Adds a global attribute to every feature endpoint that constrained devices must serve
- `attributeList` already answers "what do you support?" more precisely than a revision number

*Why not a single version with no feature tracking (Option 2):*
- Feature revisions in the spec help spec authors reason about what changed per feature
- The version manifest makes release notes unambiguous
- Feature-level tracking aids conformance testing

*Why Option 3:*
- One number on the wire: simple for devices, simple for controllers
- No combinatorial explosion: the spec defines valid combinations
- Features still evolve independently in the spec authoring process
- `attributeList` + `featureMap` handle the actual capability discovery
- Aligns with how platform SDKs version (Android API levels, Go releases)

**Declined Alternatives:**

- **ProtocolHello message after TLS:** Adds a roundtrip to every connection. ALPN + `specVersion` in DeviceInfo achieves the same with existing mechanisms.
- **Version field in every message:** Overhead on constrained devices. Protocol version is a session property, not a per-message property.
- **Capability negotiation at connection time:** Adds connection complexity. Better to connect first, discover capabilities through standard attribute reads.

**Related:** DEC-035 (capability discovery - updated by this decision), DEC-002 (simplicity over flexibility), OPEN-004 (resolved)

---

### DEC-051: Static attributeList Immutability

**Date:** 2026-01-31
**Status:** Accepted

**Context:** `attributeList` (global attribute 0xFFFB) reports which attribute IDs a feature instance supports. The question arose whether this list can change during a connection -- for example, an EV charger adding measurement attributes when an EV plugs in, or a modular device gaining capabilities at runtime.

This has protocol-wide implications: if `attributeList` can change, controllers must handle dynamic schema changes at runtime (reallocation, null checks for disappearing attributes, re-subscription). If it's static, controllers can read it once and build a stable data model.

**Options Evaluated:**

1. **Static attributeList:** Immutable for the lifetime of a connection. Reflects hardware capabilities, not transient state. Values go `null` when data is temporarily unavailable. Hardware reconfiguration requires connection re-establishment.
2. **Dynamic attributeList:** Can change at runtime. Changes reported via subscription notifications. Controllers must handle structural changes dynamically.

**Decision:** Option 1 - Static attributeList.

**Rules:**

- `attributeList` is immutable for the lifetime of a connection
- It reflects the device's hardware capabilities, not transient runtime state
- Attributes that are supported but currently have no value report `null` (e.g., `evStateOfCharge` is `null` when no EV is plugged in, but remains in `attributeList`)
- A change in hardware configuration (e.g., modular device reconfiguration) requires the device to close and re-establish the connection
- This applies to all features, not just Measurement

**Rationale:**

- **Targets 256KB MCUs (DEC-003):** Static schemas can be allocated once. Dynamic schemas require runtime reallocation.
- **Nullable already handles "unavailable":** The interaction model distinguishes "key absent" (not in message), "key with value" (has data), and "key with null" (explicitly no value). This is sufficient for "supported but currently unavailable."
- **Simplifies subscriptions:** The set of attributes that can appear in delta notifications never changes. No re-subscription needed.
- **Avoids ambiguity:** With dynamic lists, "attribute disappeared" could mean temporarily unavailable, permanently removed, or error. With static lists and nullable values, there's one interpretation: `null` = no current value.
- **PICS conformance stays meaningful:** PICS declarations match runtime reality without conditionals.
- **Matter precedent:** Matter's `AttributeList` is expected to be stable -- transient unavailability is handled through nullable values, not structural changes.

**Declined Alternatives:**

- **Dynamic attributeList:** Requires all controllers to handle schema changes at runtime. Adds complexity to every implementation for a rare scenario (hardware reconfiguration). The nullable mechanism already covers the common cases (EV plug/unplug, sensor temporarily offline).

**Related:** DEC-035 (capability discovery), DEC-003 (target hardware constraints)

---

### DEC-052: Feature-Level Subscription Model

**Date:** 2026-01-31
**Status:** Accepted

**Context:** Use case YAML definitions listed individual attribute names in their `subscriptions` field (e.g., MPD subscribed only to `acActivePower`). Analysis revealed these lists were incomplete and sometimes incorrect (e.g., COB subscribing to `acActivePower` on a BATTERY endpoint, which only has DC measurements).

The interaction model already supports "subscribe to all" via an empty `attributeIds` array in the Subscribe request. Combined with `minInterval` for batching and delta notifications that only send changed values, feature-level subscription is practical for MASH's target domain (energy devices over TLS/TCP).

**Options Evaluated:**

1. **Fix per-attribute subscription lists:** Keep the current model, make lists complete and endpoint-type-aware in each use case YAML.
2. **Feature-level subscription (subscribe to all):** Use cases declare "subscribe to this feature" without listing individual attributes. Controller subscribes with empty `attributeIds` array. Device pushes all changed attributes.
3. **Hybrid:** Default to subscribe-all, with optional per-attribute overrides for special cases.

**Decision:** Option 2 - Feature-level subscription as default.

**Model:**

- Use case YAML declares `subscribe: all` for features that need push notifications
- Controller sends Subscribe with empty `attributeIds` array (already supported by interaction model)
- Device batches attribute changes within `minInterval` and sends a single delta notification per coalescing window
- Device only sends attributes it has (nullable attributes without values are not pushed)
- `maxInterval` ensures periodic heartbeats for freshness

**Use case YAML change:**

Before:
```yaml
- feature: Measurement
  required: true
  subscriptions:
    - acActivePower
```

After:
```yaml
- feature: Measurement
  required: true
  subscribe: all
```

**Rationale:**

- **Correct by default:** No risk of incomplete subscription lists going stale as features evolve.
- **Simpler YAML:** No need to maintain per-attribute lists that duplicate feature definitions.
- **Efficient on wire:** Delta notifications only send changed values. `minInterval` batches simultaneous changes into one message. A single feature subscription report carries all changed attributes in one CBOR payload.
- **Practical for the domain:** Energy devices over TLS/TCP can handle a few hundred bytes per report. The devices are inverters and EVSEs, not coin-cell sensors.
- **Device controls cadence:** Device decides per-attribute reporting behavior within the `minInterval`/`maxInterval` bounds. Slowly-changing attributes (like `stateOfHealth`) are only sent on change or at `maxInterval` heartbeats.

**Declined Alternatives:**

- **Per-attribute subscription lists (Option 1):** High maintenance burden. Lists were already incorrect. Endpoint-type-dependent requirements make per-use-case lists complex.
- **Hybrid (Option 3):** Adds complexity without clear benefit. The empty-array-means-all mechanism is clean and sufficient.

**Related:** DEC-051 (static attributeList), DEC-029 (Measurement feature)

---

### DEC-053: Endpoint Type Conformance Registry

**Date:** 2026-01-31
**Status:** Accepted

**Context:** Feature YAML files define the superset of all possible attributes a feature can have. But not every attribute applies to every endpoint type -- a PV_STRING has no AC measurements, a GRID_CONNECTION has no battery SoC. With DEC-052 adopting feature-level subscriptions, the question becomes: how does a device developer know which attributes are mandatory for their endpoint type? And how is this testable?

The existing `mandatory` field in feature YAML is too coarse: `acActivePower` was marked `mandatory: true` in Measurement, but it's meaningless for DC-only endpoints (PV_STRING, BATTERY).

**Options Evaluated:**

1. **FeatureMap-gated conformance (Matter-style):** Use existing featureMap bits to gate attributes (e.g., `[BATTERY]` makes `stateOfCharge` mandatory). Proven pattern but the bits weren't designed for this granularity.
2. **Endpoint type conformance registry:** Per-endpoint-type mandatory/recommended attribute lists in a dedicated registry file. Most explicit and readable.
3. **Per-feature flags:** Each feature gets its own capability flags (Measurement gets AC, DC, BATT). Clean grouping but adds a second layer of feature flags.
4. **Keep simple, PICS only:** No formalization. Conformance lives in PICS files and human documentation. Simple but not testable from spec alone.

**Decision:** Option 2 - Endpoint type conformance registry with two-layer model.

**Two-layer conformance model:**

| Layer | Defines | Location |
|-------|---------|----------|
| **Feature YAML** | The superset of all possible attributes, types, access, units | `docs/features/<feature>/1.0.yaml` |
| **Endpoint type registry** | Per-endpoint-type mandatory/recommended attributes per feature | `docs/features/endpoint-conformance.yaml` |

**Conformance levels:**

- **mandatory:** Must be in `attributeList`. PICS validation fails without it. Value may be `null` per DEC-051.
- **recommended:** Should be in `attributeList` if hardware supports it. PICS validation warns if missing.
- **optional:** Everything else in the feature YAML. Device's choice.

**Example (Measurement feature):**

```yaml
GRID_CONNECTION:
  Measurement:
    mandatory: [acActivePower]
    recommended: [acCurrentPerPhase, acVoltagePerPhase, acFrequency,
                  acEnergyConsumed, acEnergyProduced]

BATTERY:
  Measurement:
    mandatory: [dcPower, stateOfCharge]
    recommended: [dcVoltage, stateOfHealth, temperature,
                  dcEnergyIn, dcEnergyOut]

PV_STRING:
  Measurement:
    mandatory: [dcPower]
    recommended: [dcVoltage, dcCurrent, dcEnergyOut]
```

**Feature YAML changes:**

- The `mandatory` flag in feature YAML now means "mandatory whenever the feature is present on ANY endpoint type" (universal minimum).
- For Measurement, `acActivePower` loses its `mandatory: true` since it's not applicable to DC-only endpoints. The endpoint type registry handles per-type requirements.

**Rationale:**

- **Single source of truth per concern:** Feature YAML defines what's possible. Endpoint type registry defines what's expected.
- **Readable:** A device developer building a BATTERY looks at one place and sees exactly what's required.
- **Testable:** PICS validator and mash-test can verify conformance against the registry.
- **Decoupled:** Feature definitions remain endpoint-agnostic. New endpoint types can be added to the registry without changing feature YAML.
- **Not over-engineered:** Two files, clear relationship, no new runtime mechanisms.

**Declined Alternatives:**

- **FeatureMap gating (Option 1):** featureMap bits are coarse (BATTERY applies cross-feature) and weren't designed for per-attribute conformance. Would require new bits or overloading existing ones.
- **Per-feature flags (Option 3):** Adds conceptual complexity. A second layer of capability flags makes the protocol harder to learn.
- **PICS only (Option 4):** Not testable from spec alone. Conformance knowledge lives in scattered docs rather than a machine-readable registry.

**Related:** DEC-051 (static attributeList), DEC-052 (feature-level subscription), DEC-035 (capability discovery), DEC-029 (Measurement feature)

---

### DEC-054: Endpoint-Aware PICS Format

**Date:** 2026-01-31
**Status:** Accepted

**Context:** The original PICS code format used device-level feature codes (`MASH.S.CTRL.A01=1`), which is ambiguous for multi-endpoint devices. A hybrid inverter with INVERTER on endpoint 1 and BATTERY on endpoint 2 cannot express per-endpoint conformance. The endpoint type conformance registry (DEC-053) further requires knowing which endpoint type each feature belongs to.

**Options Evaluated:**

1. **Keep device-level codes:** Application features remain at device level. Conformance validation must infer endpoint mapping from device type. Simple but unable to express multi-endpoint devices.
2. **Endpoint-level codes with endpoint type declarations:** PICS codes include an endpoint identifier (`MASH.S.E01.CTRL.A01=1`), with endpoint type declarations (`MASH.S.E01=INVERTER`). Enables per-endpoint conformance validation.
3. **Separate PICS files per endpoint:** One PICS file per endpoint. Clean separation but complicates tooling and loses the single-document overview.

**Decision:** Option 2 - Endpoint-level codes with endpoint type declarations.

**Format:**

```
MASH.<Side>[.E<hex><hex>][.<Feature>][.<Type><ID>][.<Qualifier>]
```

- Endpoint IDs are two hex digits: `E01`, `E02`, ..., `EFF`
- Endpoint type declarations: `MASH.S.E01=INVERTER`
- Application feature codes are endpoint-scoped: `MASH.S.E01.CTRL.A01=1`
- Transport/pairing codes remain device-level: `MASH.S.TRANS=1`, `MASH.S.COMM=1`
- `MASH.S.ENDPOINTS` is derived from the count of `MASH.S.E<xx>=<type>` declarations

**Application features (must be endpoint-scoped):**
ELEC, MEAS, CTRL, STAT, INFO, CHRG, SIG, TAR, PLAN

**Pairing/connection features (remain device-level):**
TRANS, COMM, CERT, ZONE, CONN, FAILSAFE, SUB, DURATION, DISC

**Example (multi-endpoint device):**

```
MASH.S=1

# Endpoint declarations
MASH.S.E01=INVERTER
MASH.S.E02=BATTERY

# Endpoint 1: Inverter with Measurement + EnergyControl
MASH.S.E01.MEAS.A01=1       # acActivePower
MASH.S.E01.CTRL.A01=1       # deviceType
MASH.S.E01.CTRL.A02=1       # controlState

# Endpoint 2: Battery with Measurement + EnergyControl
MASH.S.E02.MEAS.A28=1       # dcPower
MASH.S.E02.MEAS.A32=1       # stateOfCharge
MASH.S.E02.CTRL.A01=1       # deviceType
MASH.S.E02.CTRL.A02=1       # controlState
```

**Rationale:**

- **Precise per-endpoint conformance:** Enables EPT-001 rule to validate mandatory attributes per endpoint type against DEC-053 registry.
- **Multi-endpoint expressible:** A hybrid inverter/battery can declare both endpoint types with distinct feature sets.
- **Backward compatible in practice:** Transport/pairing codes are unchanged. The new endpoint segment only affects application features.
- **Consistent with device model:** MASH uses Device > Endpoint > Feature hierarchy. PICS codes now mirror this structure.

**Declined Alternatives:**

- **Device-level codes (Option 1):** Cannot express multi-endpoint devices. Conformance validation would require guessing endpoint mappings.
- **Separate files (Option 3):** Overcomplicates tooling. Most devices have 1-2 endpoints; a single file with clear endpoint prefixes is sufficient.

**Impact:**

- All PICS test data files updated to endpoint format
- Parser updated to extract endpoint ID from codes
- All validation rules (MAN, DEP, CMD, EPT) iterate per endpoint
- PICS `Code` struct gains `EndpointID` field; `PICS` struct gains `Endpoints` map

**Related:** DEC-053 (endpoint type conformance registry), DEC-051 (static attributeList), DEC-052 (feature-level subscriptions)

---

### DEC-055: Use Cases on the Wire

**Date:** 2026-01-31
**Status:** Accepted

**Context:** MASH treats use cases as controller-side inference: the controller probes features, builds a capability profile, and locally matches against a use case registry. This has three problems: (1) no business logic testing -- conformance tests verify protocol behavior ("SetLimit returns success") but not use case behavior ("failsafe engages on heartbeat timeout"), (2) no explicit contract -- a device never declares its behavioral commitments, and (3) unnecessary probing cost (~34 Read round-trips to discover what could be a single attribute).

Matter solves this with `DeviceTypeList` on every endpoint (explicit declaration) plus a Device Library Specification (behavioral requirements). EEBUS solves it with `NodeManagement.UseCaseInformation` plus per-use-case test specifications. EEBUS's core weakness is independent release of use cases and test specs, creating a combinatorial compatibility matrix.

**Design Constraints:**

- **Bundled releases:** Everything ships together under `specVersion`. Use case versions do not float independently. This is the primary lesson from EEBUS.
- **REST API versioning model:** Use case contracts must be forward-compatible within a major version. Breaking changes require a new major version (a new contract).
- **Controller version spread:** Controllers talk to devices on different specVersions simultaneously. A controller must handle LPC 1.0 devices and LPC 1.1 devices without branching on specVersion.

**Options Evaluated:**

1. **Use case names only (no version on wire):** Device declares names + endpointId. `specVersion` implicitly determines the use case contract version. Simplest wire format.
2. **Use case names with major.minor version:** Device declares name + major + minor + endpointId. Version follows REST API compatibility rules: minor = forward-compatible, major = new contract.
3. **No wire changes (tests only):** Keep use cases as controller-side inference. Add behavioral test suites selected via PICS files.

**Decision:** Option 2 -- Use case names with major.minor version on the wire.

**Wire Format:**

New `useCases` attribute (id 21) on DeviceInfo (endpoint 0):

```
UseCaseDecl struct (CBOR integer keys):
  1: endpointId  uint8    which endpoint implements this use case
  2: name        string   e.g. "LPC"
  3: major       uint8    contract version (breaking changes)
  4: minor       uint8    contract version (compatible refinements)
```

Example:
```cbor
useCases: [
  {1: 1, 2: "LPC", 3: 1, 4: 0},
  {1: 1, 2: "EVC", 3: 1, 4: 0}
]
```

**Versioning Rules:**

- **Minor increment:** Forward-compatible changes only. Tighter timing, new optional behaviors, clarifications, additional optional attributes. Any controller handling major X works with any minor within that major.
- **Major increment:** Breaking changes to the behavioral contract. Different state machine structure, removed mandatory behavior, changed semantics. A device may declare both the old and new major version during a transition period. Old controllers match the old major; new controllers prefer the new one.
- **Bundling:** Each `specVersion` release defines which use case versions are available. A device on specVersion 1.0 declares use case versions from the 1.0 manifest. Use case versions never float independently.

**Use Case YAML Changes:**

```yaml
name: LPC
fullName: Limit Power Consumption
specVersion: "1.0"
major: 1
minor: 0
```

**Rationale:**

*Why not names only (Option 1):* A controller talking to devices on different specVersions needs to know which behavioral contract applies. Without the version on the wire, the controller must maintain a specVersion-to-use-case-version mapping table and branch accordingly. With the version on the wire, the contract is self-describing. The minor version also enables precise test selection without ambiguity.

*Why not tests only (Option 3):* Does not solve the explicit contract problem (device never declares intent) or the probing cost problem (~34 reads). Test selection depends on external PICS files rather than self-describing devices.

*Why major.minor matches protocol versioning:* Consistency. The protocol already uses major.minor for specVersion with identical semantics (major = breaking, minor = additive). Using the same model for use cases makes the versioning story uniform across the protocol.

*Why REST API model:* Controllers in the real world handle devices on different versions simultaneously, just like API servers handle clients on different versions. Forward compatibility within a major version means controllers don't need per-minor branching logic. Breaking changes get a new major, which the device can declare alongside the old one for graceful migration.

**Declined Alternatives:**

- **Per-use-case revision without major/minor (single integer):** No way to distinguish compatible refinements from breaking changes. Controllers must treat every revision bump as potentially breaking.
- **Version in the name string ("LPC/v1"):** Unusual for a binary protocol. Structured fields are more consistent with the rest of the wire format and enable numeric comparison.
- **Independent use case releases:** The EEBUS path. Creates combinatorial compatibility matrix. Rejected by design constraint.

**Impact:**

- DeviceInfo gains `useCases` attribute (id 21, mandatory, readOnly)
- Use case YAML files gain `major` and `minor` fields
- `mash-ucgen` generates registry with version information
- `mash-test` reads `useCases` from device to select test suites (version-aware)
- `protocol-versions.yaml` version manifest expands to include use case versions per release
- DEC-050 (feature versioning) is unaffected -- features continue using `attributeList` for capability discovery

**Related:** DEC-050 (protocol versioning), DEC-035 (capability discovery), DEC-053 (endpoint type conformance)

---

### DEC-056: Controller-Side Use Case Declarations

**Date:** 2026-01-31
**Status:** Accepted

**Context:** DEC-055 added device-side `useCases` declarations to DeviceInfo so that devices explicitly declare which use cases they support. Controllers are also MASH devices with DeviceInfo. They should declare which use cases they support as a client.

**Decision:** Controllers populate `useCases` with `EndpointID: 0` to indicate client-side support. Same `UseCaseDecl` wire format. Version comes from the use case registry.

Example for a CEM supporting all 11 use cases:
```cbor
useCases: [
  {1: 0, 2: 0x01, 3: 1, 4: 0, 5: 0x07},  // LPC, scenarios BASE+MEAS+OVERRIDE
  {1: 0, 2: 0x02, 3: 1, 4: 0, 5: 0x07},  // LPP
  {1: 0, 2: 0x03, 3: 1, 4: 0, 5: 0x07},  // MPD
  {1: 0, 2: 0x04, 3: 1, 4: 0, 5: 0x3F},  // EVC (6 scenarios)
  {1: 0, 2: 0x05, 3: 1, 4: 0, 5: 0x07},  // COB
  ...
]
```

**Rationale:**

- **Symmetric:** Both sides of the protocol are explicit. Devices declare what they implement; controllers declare what they can control.
- **Testable:** Enables controller conformance testing. A test harness can read the controller's `useCases` to determine which test suites to run.
- **Discoverable:** A device can read the controller's capabilities during connection setup, enabling smarter negotiation.
- **No new types:** Reuses `UseCaseDecl` exactly as defined in DEC-055. No wire format changes.

**Declined Alternatives:**

- **Separate attribute for controller capabilities:** Adds unnecessary complexity. The `EndpointID: 0` convention naturally distinguishes client-side from server-side declarations.
- **Implicit from feature probing:** The same problem DEC-055 solved for devices applies to controllers -- inference is fragile and costly.

**Impact:**

- `EvaluateController()` function added to `pkg/usecase/evaluate.go`
- CEM example populates `useCases` in its DeviceInfo
- No changes to wire format, ucgen generator, or discovery code

**Related:** DEC-055 (use cases on the wire)

---

### DEC-057: Integer Use Case IDs and Scenario Bitmaps

**Date:** 2026-02-01
**Status:** Accepted

**Context:** DEC-055 introduced use case declarations on the wire using string names (e.g., `"LPC"`). While functional, this was inconsistent with the rest of MASH where all identifiers (feature types, endpoint types, command IDs) are integers. Additionally, use cases had no way to express which optional scenarios a device supports -- everything was a flat list of features with required/optional flags.

**Options Evaluated:**
1. **Keep string names, add separate scenario field** -- Minimal change, but string names waste wire bytes and are inconsistent.
2. **Integer IDs only** -- Compact and consistent, but doesn't address the optional scenario problem.
3. **Integer IDs + scenario bitmap** (chosen) -- Compact wire format, consistent with MASH conventions, and introduces atomic scenario contracts inspired by Matter's FeatureMap.

**Decision:** Replace string use case names with `uint16` IDs on the wire and add a `uint32` scenario bitmap to `UseCaseDecl`. Each use case defines named scenarios (BASE at bit 0, plus optional scenarios at bits 1+). Declaring a scenario is atomic -- all features in a scenario are present or none are.

Wire format change:
```cbor
// Before (DEC-055):
{1: endpointId, 2: "LPC",  3: 1, 4: 0}

// After (DEC-057):
{1: endpointId, 2: 0x01, 3: 1, 4: 0, 5: 0x03}
//                 ^^^^                  ^^^^
//              uint16 ID          scenario bitmap (BASE + MEASUREMENT)
```

Scenario structure (example LPC):
- Bit 0 (BASE): EnergyControl(limits) + Electrical(maxConsumption) -- always required
- Bit 1 (MEASUREMENT): Measurement feature -- optional
- Bit 2 (OVERRIDE): Override tracking -- optional

**Rationale:**

- **Consistency:** All other MASH identifiers use integers. String names were the only exception.
- **Compactness:** `uint16` is 2-3 bytes vs 3-5+ bytes for a string name. At 11 use cases per controller, this saves 30+ bytes per declaration set.
- **Matter-inspired:** The scenario bitmap follows the same pattern as Matter's FeatureMap -- a single uint32 tells you exactly which optional capabilities are present.
- **Atomic contracts:** No ambiguity about partial scenarios. If bit 1 is set, all features in scenario 1 must be present. This eliminates the O/M/C complexity that plagued EEBUS.
- **Testable:** PICS validation can check per-scenario: BASE features produce errors if missing; non-BASE features produce errors only if the scenario is declared.

**Impact:**

- `UseCaseDecl` wire format: key 2 changes from string to uint16, key 5 added (uint32 scenarios)
- `UseCaseDef` restructured: `Features []FeatureRequirement` replaced by `Scenarios []ScenarioDef`
- Code generator (`mash-ucgen`): emits scenario-structured registry with `IDToName`/`NameToID` maps
- Matcher: evaluates per-scenario independently; BASE must match for use case to match
- PICS: generates per-scenario codes (`MASH.S.UC.LPC.S00`, `MASH.S.UC.LPC.S01`, etc.)
- All 11 use case YAMLs restructured with `scenarios:` sections
- Breaking wire change -- justified since protocol is pre-1.0

**Declined Alternatives:**

- **String names with scenario field:** Inconsistent with integer-based ID convention used everywhere else in MASH. More bytes on the wire for no benefit.
- **Flat feature list with per-feature optionality flags:** This is what EEBUS does (O/M/C per use case element). It creates combinatorial explosion in testing and ambiguous partial support states.
- **Per-scenario separate declarations:** One `UseCaseDecl` per scenario would be cleaner in some ways but wastes wire space and complicates deduplication.

**Related:** DEC-055 (use cases on the wire), DEC-056 (controller-side declarations), DEC-035 (Matter-style capability discovery)

---

### DEC-058: Capability Snapshots in Protocol Logs

**Date:** 2026-02-02
**Status:** Proposed

**Context:** On long-lived TLS/TCP connections, device capability metadata -- endpoints, features, use cases, scenarios, featureMap, attributeList -- is exchanged once at connection establishment and never repeated. If protocol logs are rotated, truncated, or begin recording mid-session, the initial discovery data is lost. A log analyst can no longer determine what the remote device supports, making the remaining log data difficult to interpret.

This is a known pain point from EEBUS/SPINE, where `NodeManagementDetailedDiscovery` is exchanged once and then relied upon for the life of the connection. Matter mitigates this incidentally through UDP-forced re-subscriptions (priming reports every MaxInterval), but this is a transport artifact, not a deliberate observability mechanism. MASH's reliable TCP transport means connections can persist for days or weeks without re-exchanging capabilities.

The problem affects both sides of the connection and is compounded by the fact that third-party devices control their own logging -- we cannot mandate log retention policies.

**Options Evaluated:**
1. **Protocol-level metadata snapshot message** -- New control message type emitted periodically on the wire.
2. **Enhanced subscription heartbeats** -- Include global attributes (featureMap, attributeList) in every heartbeat notification.
3. **Capability inference from partial logs** -- Offline tool that reconstructs capabilities from protocol traffic patterns.
4. **Capability snapshots in protocol logs** (chosen) -- Periodic log events containing complete device model snapshots, with a configurable hybrid trigger policy.

**Decision:** Add a logging-layer capability snapshot mechanism. The application configures a `SnapshotPolicy` on the stack with three parameters: `MaxInterval` (time trigger), `MaxMessages` (message count trigger), and `MinMessages` (floor for the time trigger). A snapshot is emitted when either `MaxMessages` is reached or `MaxInterval` elapses with at least `MinMessages` logged since the last snapshot. An initial snapshot always fires at session establishment.

Each snapshot contains the complete device model of both the local device and the connected remote peer (endpoints, features, featureMap, attributeList, commandList, use cases, scenarios).

```go
type SnapshotPolicy struct {
    MaxInterval time.Duration  // Time trigger (fires only if MinMessages met)
    MaxMessages int            // Message count trigger (fires unconditionally)
    MinMessages int            // Floor for time trigger (prevents snapshot spam on idle connections)
}

// Default: 30 min / 1000 messages / 50 message floor
```

The hybrid trigger with MinMessages floor ensures:
- Busy connections (high log growth, high rotation risk): frequent snapshots proportional to traffic
- Normal connections: periodic snapshots at MaxInterval
- Near-idle connections (few messages, low rotation risk): time trigger suppressed, snapshots only at MaxMessages
- Idle connections: initial snapshot only

**Rationale:**

- **Logging-layer, not protocol-layer:** The problem is about log observability, not protocol correctness. No wire overhead, no spec change required, no impact on third-party implementations.
- **Hybrid trigger:** Pure time-based triggers waste log space on idle connections. Pure count-based triggers miss idle connections entirely. The hybrid with MinMessages floor adapts the snapshot frequency to the actual forensic risk.
- **Both sides included:** Each snapshot contains local + remote capabilities. On the controller side, remote data is cached after initial discovery reads (free due to DEC-051 immutability). On the device side, remote data is populated from commissioning certificate info and any exposed controller model.
- **Negligible overhead:** A typical snapshot is 400-600 bytes CBOR. With default policy, busy connections produce ~22 KB/hour of snapshot data; quiet connections produce near zero.

**Impact:**

- New `SnapshotPolicy` struct in `pkg/service/types.go`, added to `DeviceConfig` and `ControllerConfig`
- New `snapshotTracker` in `pkg/service/snapshot_tracker.go` embedded in ZoneSession and DeviceSession
- New `CategorySnapshot` and `CapabilitySnapshotEvent` types in `pkg/log/event.go`
- Controller reads full remote device capabilities after commissioning/reconnection (extends existing `checkDeviceVersion` pattern)
- `mash-log` tool extended: snapshot display in `view`, snapshot statistics in `stats`, `--annotate` flag for human-readable ID resolution

**Declined Alternatives:**

- **Protocol-level metadata snapshot message (option 1):** Requires a spec change affecting all implementations. The problem is about log forensics, not protocol behavior. Wire overhead is unnecessary when the data is already in memory locally.
- **Enhanced subscription heartbeats (option 2):** Per-feature only -- no single message captures the full device model. Doesn't include endpoint structure or use case declarations. The current implementation already includes global attributes in subscribe-all heartbeats (DEC-052).
- **Capability inference from partial logs (option 3):** Best-effort only. Cannot discover endpoints/features with no traffic. Cannot reconstruct use case declarations or device identity. High implementation complexity for uncertain results. May be valuable as a supplementary tool but not as the primary solution.
- **Sidecar index file:** A `.mlog.idx` file that stores the latest snapshot per connection. Useful optimization for log rotation scenarios but not needed as the primary mechanism.

**Related:** DEC-051 (attributeList immutability), DEC-052 (feature-level subscriptions), DEC-055 (use cases on the wire), DEC-035 (capability discovery)

**Design Document:** [docs/design/capability-snapshots.md](design/capability-snapshots.md)

---

### DEC-059: Auto Re-enter Commissioning on Last Zone Removal

**Date:** 2026-02-02
**Status:** Proposed

**Context:** When a device's last zone is removed, the device must become commissionable again. Without this, a device that loses all controllers is permanently stuck in operational mode with no way to recommission. This also impacts test infrastructure: the test harness needs to run hundreds of spec tests that cycle between commissioned and uncommissioned states.

**Decision:** When `RemoveZone` reduces zone count to 0, the device automatically calls `EnterCommissioningMode()`. This is mandatory behavior for all MASH devices. The test harness sends `RemoveZone` before disconnecting on backward precondition transitions (commissioned -> uncommissioned) to trigger this behavior.

**Rationale:**

- A device with no zones is useless -- it cannot be controlled
- Manual intervention to re-enter commissioning mode defeats automation
- Consistent with Matter's behavior after fabric removal
- Simplifies test infrastructure (no device restart needed between tests)
- The zone ID for RemoveZone can be derived from the PASE shared secret using the same `SHA-256(secret)[:8]` derivation both sides already use

**Declined Alternatives:**

- **Timer-based auto-recommission:** Device waits N seconds after last zone disconnect before entering commissioning. Adds unnecessary delay and complexity. Immediate re-entry is simpler and correct.
- **Require explicit API call:** Forces test infrastructure or management apps to restart devices between test runs. Unnecessarily complex for the common case.

**Related:** DEC-047 (commissioning security), DEC-043 (zone type constraints)

---

### DEC-060: TEST Zone Type for Conformance Testing

**Date:** 2026-02-03
**Status:** Accepted

**Context:** The conformance test harness needs to connect to a device alongside real controllers (GRID, LOCAL) to read feature data and send test commands. Without a dedicated zone type, the test harness would consume one of the device's two production zone slots. Additionally, the device was hardcoding `ZoneTypeLocal` for all incoming zones instead of extracting the zone type from the Zone CA certificate's OrganizationalUnit field.

**Decision:**

1. Add `ZoneTypeTest` (value 3, priority 3/lowest) as a new zone type.
2. TEST zones are pure observers: excluded from limit resolution (most-restrictive-wins) and setpoint resolution (highest-priority-wins).
3. TEST zones are only accepted when `DeviceConfig.TestMode == true`.
4. `MaxZones` bumped from 2 to 3 (GRID + LOCAL + TEST). In production, `DefaultDeviceConfig()` keeps `MaxZones: 2`; when `TestMode` is enabled, the constructor bumps it to 3.
5. Extract zone type from the Zone CA certificate's `OrganizationalUnit[0]` field via new `ExtractZoneTypeFromCert()` function. This fixes the existing bug where all zones were treated as LOCAL regardless of the certificate content.

**Rationale:**

- A dedicated zone type prevents test infrastructure from consuming production zone slots
- Observer-only semantics ensure test zones cannot interfere with limit/setpoint resolution
- Gating behind `TestMode` prevents TEST zones in production deployments
- Extracting zone type from the certificate is necessary regardless (pre-existing bug)
- Priority 3 (lowest) means TEST zones never win in any resolution algorithm

**Declined Alternatives:**

- **Use LOCAL zone for testing:** Would consume the device's only LOCAL slot, preventing multi-zone conformance testing.
- **Add a separate test connection type outside the zone model:** More complex, would require a parallel connection management path.
- **Always allow TEST zones:** Security concern -- production devices should not accept observer connections without explicit opt-in.

**Related:** DEC-043 (one zone per type), DEC-047 (commissioning security), DEC-059 (auto recommission)

---

### DEC-061: Message-Gated Commissioning Locking

**Date:** 2026-02-04
**Status:** Accepted

**Context:** The DEC-047 commissioning lock (`commissioningConnActive`) is acquired at TLS accept time -- before any PASE message arrives. An idle TLS connection that never sends a PASERequest holds the lock for the full handshake timeout (85s), blocking all commissioning. Matter acquires the commissioning lock only when the first PASE message is received.

**Options Evaluated:**
1. Keep lock at TLS accept, reduce handshake timeout
2. Move lock to first PASE message receipt (Matter-inspired)
3. Add a pre-lock connection counter (`MaxPreLockConnections`)

**Decision:** Move lock acquisition to first PASE message receipt. Split `PASEServerSession.Handshake` into two phases:

1. **`WaitForPASERequest(ctx, conn)`** -- reads the first message (must be PASERequest), no lock held, governed by a 5-second first-message timeout.
2. **`CompleteHandshake(ctx, conn, req)`** -- processes the remaining PASE exchange with the commissioning lock held, governed by the existing 85-second handshake timeout.

The original `Handshake()` method is preserved as a wrapper calling both methods for backward compatibility.

**Rationale:**
- Idle TLS connections can no longer starve commissioning (5s timeout vs 85s)
- The commissioning lock scope is reduced from TLS-accept-to-cert-exchange to PASERequest-to-cert-exchange
- Two simultaneous TLS connections are both accepted; the second is rejected at PASERequest (lock check), not at TLS
- Aligns with Matter's approach of deferring resource commitment until the first application-layer message

**Declined Alternatives:**
- **Reduce handshake timeout:** Only reduces the window, doesn't eliminate the problem. 85s is needed for legitimate slow handshakes (backoff delays, slow MCU crypto).
- **MaxPreLockConnections counter:** Adds complexity. Can be added later if MCU resource exhaustion from idle TLS connections is observed.

**Related:** DEC-047 (commissioning security hardening)

---

### DEC-062: Transport-Level Connection Cap

**Date:** 2026-02-04
**Status:** Accepted

**Context:**
The spec (DEC-047) defines a max_zones+1 connection cap, but enforcement was only at the PASE level (`acceptCommissioningConnection`). Flood connections that never send a PASE message bypassed this check entirely, because the PASE-level guard (DEC-061) only fires after the first application-layer message. This allowed a flood of raw TLS connections to exhaust device resources.

**Decision:**
Add an `atomic.Int32` counter (`activeConns`) to `DeviceService` and enforce the total connection cap at TCP accept, before the TLS handshake:

1. **Increment** in `acceptLoop` after `Accept()`, before spawning the connection handler goroutine
2. **Cap check** in `acceptLoop`: if `activeConns >= MaxZones + 1`, close the raw TCP connection immediately (before TLS handshake) and continue
3. **Decrement** via `defer` at the top of `handleConnection` (covers all exit paths: TLS failure, PASE failure, normal close)
4. **No TOCTOU race**: the check and increment both happen in `acceptLoop`, which is a single goroutine

**Rationale:**
- Earliest possible rejection point -- protects MCU memory from TLS handshake overhead
- Matches Matter's approach (`CHIP_CONFIG_MAX_ACTIVE_TCP_CONNECTIONS=4`, rejection at TCP accept)
- `atomic.Int32` is lock-free, suitable for constrained environments
- The counter naturally includes all connection types (commissioning and operational), so a flood of any type is bounded

**Declined Alternatives:**
- **Per-type counters (separate commissioning vs operational):** Adds complexity without benefit. The total cap already covers the worst case. Per-type tracking remains at the application layer (DEC-047).
- **Semaphore/channel-based limiting:** Heavier than an atomic counter and not needed since `acceptLoop` is single-goroutine.

**Related:** DEC-047 (commissioning security hardening), DEC-061 (message-gated commissioning locking)

---

### DEC-063: Busy Rejection Response

**Date:** 2026-02-04
**Status:** Accepted

**Context:**
When a commissioning connection is rejected (due to PASE lock contention, connection cooldown, or full zone slots), the device silently closes the TCP connection. The controller has no way to distinguish a busy device from a network failure, and no hint about when to retry.

**Decision:**
Send a `CommissioningError` message (MsgType 255) with error code 5 (`DEVICE_BUSY`) and a `RetryAfter` field before closing the connection:

1. **New error code**: `ErrCodeBusy = 5` (between CertInstallFailed=4 and ZoneTypeExists=10)
2. **New CBOR field**: `RetryAfter uint32` at key 4 with `omitempty` -- retry hint in milliseconds
3. **Exported helper**: `WriteCommissioningError(conn, code, message, retryAfterMs)` wraps the unexported `writeMessage` function
4. **RetryAfter values**:
   - Commissioning in progress: HandshakeTimeout ms (wait for current handshake)
   - Cooldown active: remaining cooldown ms
   - Zones full: 0 (no point retrying until decommission)

**Rationale:**
- CBOR ignores unknown keys, so adding key 4 is backward compatible with older implementations
- `omitempty` ensures RetryAfter is not serialized for non-busy errors, keeping messages compact
- Error code 5 fits the existing numbering gap (4=CertInstallFailed, 10=ZoneTypeExists)
- The exported `WriteCommissioningError` wrapper avoids exposing the internal `writeMessage` function

**Related:** DEC-047 (security hardening), DEC-061 (message-gated locking), DEC-062 (connection cap)

---

### DEC-064: Stale Connection Reaper

**Date:** 2026-02-04
**Status:** Accepted

**Context:**
Pre-operational connections (TLS established, but no PASE or cert exchange completed) could remain open indefinitely. While DEC-061's `PASEFirstMessageTimeout` handles the case where no PASERequest arrives, and `HandshakeTimeout` bounds the PASE+cert exchange, there is no catch-all for connections that are accepted but stall at any point before becoming operational. On resource-constrained devices, these zombie connections consume memory and connection slots.

**Decision:**
Add a periodic background goroutine (the "reaper") that force-closes pre-operational connections exceeding a staleness threshold:

1. **connTracker**: `map[net.Conn]time.Time` guarded by a mutex, tracking when each connection was accepted
2. **Add**: register connections in `acceptLoop` after `activeConns.Add(1)`
3. **Remove**: deregister connections explicitly before entering `runZoneMessageLoop` (not at close), ensuring operational connections are never visible to the reaper
4. **Safety net**: `defer connTracker.Remove(conn)` in `handleConnection` for connections that fail before reaching the message loop
5. **Reaper goroutine**: `runStaleConnectionReaper()` runs on a ticker, calling `connTracker.CloseStale(StaleConnectionTimeout)`. Exits cleanly on `ctx.Done()`
6. **Config**: `StaleConnectionTimeout` (default 90s), `ReaperInterval` (default 10s). Set `StaleConnectionTimeout=0` to disable

**Rationale:**
- Default 90s > HandshakeTimeout 85s: per-phase timeouts (PASEFirstMessageTimeout, HandshakeTimeout) fire first for well-behaved connections. The reaper is a safety net for edge cases
- Remove-before-message-loop (not remove-at-close) prevents the reaper from seeing operational connections, avoiding false positives
- `map[net.Conn]time.Time` is sufficient for MaxZones+1 = 3 connections; no need for a more complex data structure
- Ticker-based approach is simple and deterministic; no per-connection timers needed

**Declined Alternatives:**
- **Per-connection timers**: More precise but creates N timers for N connections. Not worth the complexity for <= 3 connections.
- **Reap on accept only**: Would require scanning on each accept. Periodic reaping is simpler and catches stale connections even when no new connections arrive.

**Related:** DEC-047 (security hardening), DEC-061 (message-gated locking), DEC-062 (connection cap), DEC-063 (busy response)

---

### DEC-065: Connection Cooldown Starts at Release

**Date:** 2026-02-04
**Status:** Accepted

**Context:**
The DEC-047 connection cooldown (500ms) was originally set when a commissioning connection was **accepted** (`acceptCommissioningConnection`). This blocked legitimate follow-up attempts: after multiple rapid PASE attempts (e.g., backoff testing), the cooldown timer was set on each acceptance, so the next attempt -- even with the correct setup code -- was rejected before PASE could proceed.

**Decision:**
Move the cooldown timestamp from connection acceptance to connection **release** (`releaseCommissioningConnection`):

1. `acceptCommissioningConnection()` no longer sets `lastCommissioningAttempt`
2. `releaseCommissioningConnection()` sets `lastCommissioningAttempt = time.Now()` when the connection finishes (success or failure)
3. The cooldown check in `acceptCommissioningConnection()` is unchanged -- it still compares `time.Since(lastCommissioningAttempt)` against `ConnectionCooldown`

**Effect:**
- The cooldown window starts **after** a commissioning attempt completes, not when it begins
- A long-running PASE exchange does not extend the cooldown into the next attempt's acceptance window
- Rapid sequential attempts after a completed attempt are still blocked (the 500ms gap is enforced between release and next acceptance)
- DEC-062 (transport-level cap) and DEC-047 (PASE backoff) continue to protect against connection floods and brute-force independently

**Rationale:**
- Cooldown-at-acceptance conflicted with backoff testing: 5 rapid PASE failures left the cooldown active for the subsequent legitimate commission step
- Cooldown-at-release preserves the anti-rapid-reconnection intent while allowing legitimate sequential commissioning (e.g., commission, exit, re-enter, commission second zone)
- Aligns with the principle that cooldown prevents rapid **re-attempts**, not rapid **in-progress** connections (the single-connection lock already prevents concurrent commissioning)

**Related:** DEC-047 (security hardening), DEC-061 (message-gated locking)

---

### DEC-066: Close Commissioning Connection After Certificate Exchange

**Date:** 2026-02-05
**Status:** Accepted

**Context:**
The original MASH implementation kept the TLS connection open from PASE through certificate exchange and into operational messaging. This created several issues:

1. **Trust boundary ambiguity:** The same TLS session used both commissioning (temporary) and operational (permanent) certificates
2. **Multi-zone complexity:** Device could only present one TLS certificate; multi-zone required workarounds
3. **Session state confusion:** Test harness had difficulty tracking whether connection was in commissioning or operational state
4. **Stale connection detection:** When tests failed mid-flow, the device kept zones as "connected" even though the controller was gone

**Decision:**
The device MUST close the commissioning TLS connection after sending CertInstallAck (message type 33). The controller then initiates a fresh TLS connection using operational certificates:

```
PASE Connection (temporary):
  1. TLS handshake (temporary/self-signed certs)
  2. SPAKE2+ exchange (setup code authentication)
  3. Certificate exchange (CertRequest → CSR → Install → Ack)
  4. Device closes connection

Operational Connection (persistent):
  5. Controller initiates new TLS connection
  6. Mutual authentication with operational certificates
  7. Device matches certificate issuer to known Zone CA
  8. Operational message loop begins
```

**Rationale:**
- **Clean separation:** Commissioning credentials never touch operational traffic
- **Matter alignment:** Similar to Matter's PASE → CASE transition model
- **Simpler multi-zone:** Each zone has its own TLS connection with zone-specific certificate
- **Better failure detection:** Fresh TLS handshake forces certificate validation; stale sessions don't accumulate

**Implementation notes:**
- Device closes connection immediately after sending CertInstallAck
- Device waits up to 30 seconds for operational reconnect before removing zone
- Controller must handle the closed connection and initiate reconnect
- Test harness defaults to `transition_to_operational: true` behavior

**Related:** DEC-047 (commissioning security), security.md Section 4.5

---

### DEC-067: Single-Port ALPN-Based Commissioning Routing and Stable Certificate

**Date:** 2026-02-11
**Status:** Accepted (recommended pattern; see DEC-082 for the conformant two-port alternative)

> **Cross-reference (2026-04-25, DEC-082):** Single-port ALPN routing as described here remains the canonical pattern for implementations whose TLS library exposes a `GetConfigForClient`-equivalent primitive (Go, partially Rust). DEC-082 formalizes a conformant two-port alternative for languages without that primitive (Python stdlib `ssl`, C/mbedTLS, Zig). Both patterns produce identical wire behavior and inherit the same security properties. Implementations choose based on their TLS library's ergonomics; mDNS SRV `port` is authoritative per service.

**Context:**
The original implementation used a single TLS listener for both commissioning and operational connections. Routing was determined post-handshake by checking whether the peer presented a client certificate:

- No client cert → commissioning handler (PASE flow)
- Client cert present → operational handler (zone reconnect)

This created several problems:

1. **Certificate leakage:** After commissioning a first zone, the device presents its operational certificate (signed by Zone CA) to all new connections -- including commissioning attempts for a second zone. The commissioner uses `InsecureSkipVerify: true`, but the operational cert is still exposed to an unauthenticated peer.
2. **TLS config coupling:** The shared listener uses `ClientAuth: RequestClientCert` (request but don't require) as a routing workaround, preventing strict mutual TLS enforcement on operational connections.
3. **Wasteful cert regeneration:** The device regenerates a fresh ECDSA P-256 key pair and self-signed certificate on every commissioning window open. On resource-constrained devices (ESP32 at 240 MHz), P-256 key generation takes 200-500ms per invocation -- unnecessary overhead since security comes from PASE, not the commissioning certificate.

**Decision:**
Two changes:

**1. ALPN-based routing on a single port.**
The device uses a single TCP port with ALPN protocol negotiation to distinguish commissioning from operational connections:

| Aspect | Value |
|--------|-------|
| Port | 8443 (single port for both) |
| Commissioning ALPN | `mash-comm/1` |
| Operational ALPN | `mash/1` |
| Routing mechanism | `GetConfigForClient` inspects `ClientHelloInfo.SupportedProtos` |
| Commissioning gate | `atomic.Bool` checked in callback; rejects `mash-comm/1` when window closed |
| Listener lifecycle | Start when first needed (commissioning OR zones), stop when both inactive |
| mDNS | `_mash-comm._tcp` and `_mash._tcp` both advertise port 8443 |

**2. Stable commissioning certificate.**
The device SHOULD generate its self-signed commissioning certificate once and reuse it indefinitely:

- **Reference implementation (Go):** Generate at first startup, cache in memory for the process lifetime.
- **Embedded devices (ESP32):** Generate at factory provisioning, store in NVS alongside discriminator and setup code.
- **No regeneration required:** The commissioner uses `InsecureSkipVerify: true` and all authentication comes from PASE (SPAKE2+). Neither the key material, the CN, nor the validity period of the commissioning certificate is security-relevant. The discriminator is discovered via mDNS TXT records, not from the certificate CN. The cert only needs to exist so the TLS 1.3 handshake can complete.

**Rationale:**
- **Security isolation:** Commissioning connections get NoClientCert + self-signed cert. Operational connections get RequireAndVerifyClientCert + zone CA cert. Certificate leakage is prevented because `GetConfigForClient` returns different `tls.Config` instances before the handshake completes.
- **Strict mutual TLS:** Operational connections enforce `RequireAndVerifyClientCert`, rejecting any connection without a valid Zone CA-signed certificate at the TLS level.
- **Resource efficiency:** Eliminating per-window key generation removes a 200-500ms latency spike on ESP32 for every commissioning window open. In test mode with dozens of commission/decommission cycles, this adds up significantly.
- **Simpler architecture:** One listener, one accept loop, one handler with ALPN-based dispatch. No post-handshake routing heuristic needed.
- **TLS-level rejection:** When the commissioning window is closed, `GetConfigForClient` returns an error for `mash-comm/1` connections, causing the TLS handshake to fail at the TLS layer.
- **Embedded-friendly:** Fewer sockets, simpler lifecycle. Especially beneficial on resource-constrained MCUs.

**Certificate Properties:**
- Self-signed ECDSA P-256 certificate
- CN: `MASH-<discriminator>` (informational only; authentication via PASE)
- Validity: 20 years (arbitrarily long since it's not security-critical)
- Key material: Generated once, never changes

**Declined Alternatives:**
- **Separate commissioning and operational ports:** Initially declined here as adding ~150 lines of dual-listener infrastructure for no security benefit over ALPN routing in Go. Subsequently formalized as a conformant alternative pattern in **DEC-082** for implementations whose TLS library lacks a `GetConfigForClient`-equivalent primitive (Python stdlib `ssl`, C/mbedTLS, Zig). The "no security benefit" assessment still holds — both patterns produce identical security guarantees — but the "added infrastructure" assessment was Go-specific; in non-Go languages, two-port deployment is the *simpler* path.
- **Single port with SNI-based routing:** TLS SNI could distinguish commissioning vs operational, but adds complexity without solving the cert-leakage issue.
- **Post-handshake routing (pre-DEC-067):** The original approach. Leaked operational certs and couldn't enforce strict mutual TLS.
- **No TLS for commissioning (Matter approach):** Matter runs PASE over raw TCP/UDP. This would require implementing a custom framing and encryption layer. MASH's TLS-based approach is simpler for implementors at the cost of requiring a commissioning certificate.

**Implementation notes:**
- `commissioningOpen atomic.Bool` gates commissioning at the TLS layer.
- `ensureListenerStarted()` / `stopListener()` manage the single listener lifecycle.
- `handleIncomingConnection()` routes based on negotiated ALPN protocol.
- `NewCommissioningTLSConfig()` returns `mash-comm/1` ALPN (used by controllers dialing commissioning).
- `MajorFromALPN()` handles both `mash/N` and `mash-comm/N` prefixes.

**Related:** DEC-047 (commissioning security), DEC-061 (message-gated locking), DEC-066 (close commissioning connection after cert exchange), DEC-082 (two-port deployment alternative)

---

### DEC-068: Invalid Passcode Validation

**Date:** 2026-02-14
**Status:** Accepted

**Context:**
Matter specification 5.1.7.1 defines 12 prohibited setup codes that devices MUST reject due to low entropy. MASH previously accepted all values 0-99999999 with no weak-value checking. Additionally, many tests and defaults used "12345678" as the standard test setup code, which is on the prohibited list.

**Decision:**
1. Valid setup code range narrowed to **1-99999998** (excludes 0 and 99999999 as boundary values).
2. **10 explicit codes** rejected: all repeated-digit codes (11111111 through 88888888), plus 12345678 and 87654321.
3. `Validate()` rejects invalid codes -- device-side provisioning gatekeeper.
4. `ParseSetupCode()` stays permissive -- test harness needs to parse any code for negative testing.
5. `GenerateSetupCode()` retries up to 10 times if the generated code is invalid.
6. Standard test passcode changed from "12345678" to "20202021" (Matter's standard test code).
7. `DeviceConfig.Validate()` strengthened to call `commissioning.ParseSetupCode().Validate()`.

**Rationale:**
- Aligns with Matter specification for prohibited codes
- Separating parse (permissive) from validate (strict) lets the test harness send intentionally invalid codes for conformance testing
- 20202021 is Matter's standard test passcode, providing consistency with the broader ecosystem
- Retry-based generation (max 10 attempts) matches Matter's approach

**Implementation notes:**
- `pkg/commissioning/setupcode.go`: `SetupCodeMin`, `SetupCodeMax`, `InvalidSetupCodes`, updated `Validate()` and `GenerateSetupCode()`
- `pkg/service/types.go`: `DeviceConfig.Validate()` now calls `commissioning.ParseSetupCode().Validate()`
- PICS item: `MASH.S.COMM.INVALID_PASSCODES`
- Conformance tests: TC-PASE-006 (repeated-digit), TC-PASE-007 (sequential codes)

**Related:** DEC-047 (commissioning security), Matter specification 5.1.7.1

---

### DEC-069: Max Message Size 8 KB

**Date:** 2026-04-23
**Status:** Accepted

**Context:**
`DefaultMaxMessageSize` was 65536 bytes (64 KB), chosen as a safe upper bound early in design. DEC-003 targets 256 KB RAM MCUs (ESP32-class). A single in-flight 64 KB buffer is 25% of total RAM, which leaves little room for TLS, stack, and application state. Analysis (`kiss-analysis.html` §MSG) found no protocol message in MASH that approaches 8 KB: typical messages are <2 KB, `attributeList` is ~300 bytes for the largest feature, and use-case declarations fit comfortably. The 64 KB ceiling was spending headroom the protocol does not need.

**Decision:**
Lower `DefaultMaxMessageSize` from 65536 to 8192 bytes (8 KB). The `NewFramerWithMaxSize` / `NewFrameWriterWithMaxSize` / `NewFrameReaderWithMaxSize` opt-in remains available for callers with a legitimate reason to allow larger frames (e.g., specific conformance tests exercising negative bounds).

**Rationale:**
- 8 KB fits the 256 KB MCU budget with substantial headroom (~3% of RAM per buffer, vs. 25%).
- No current protocol payload needs more than 8 KB; every feature's `attributeList`, use-case list, and notification batch fits well under.
- Lowering the default makes it a forcing function: new features that want to exceed must surface the cost (DEC-072 codifies attributeList size bounds as a consequence).
- Opt-in callers (`WithMaxMessageSize`-shaped factories) keep escape hatches for test harnesses and negative-bound conformance checks.

**Implementation notes:**
- `pkg/transport/framing.go`: `DefaultMaxMessageSize = 8192`.
- `pkg/commissioning/session.go`: sanity check at `readMessage` now references `transport.DefaultMaxMessageSize` instead of a hard-coded 65536.
- `pkg/transport/framing_test.go`: `TestFraming_NewDefaultIs8K`, `TestFraming_AcceptAt8192Bytes`, `TestFraming_RejectAt8193Bytes` enforce the boundary.
- `internal/testharness/runner/connection_handlers_test.go`: `TestHandleSendRawFrame_MaxSizePayload` now uses `transport.DefaultMaxMessageSize` so the boundary test tracks the constant.
- Spec updates: `docs/transport.md` §4.1 + §9 (max subscription batch reduced from 10 KB to 4 KB to stay strictly under new cap), `docs/testing/behavior/message-framing.md`, `docs/testing/behavior/connection-state-machine.md`, PICS code `MASH.S.TRANS.MAX_MSG_SIZE` in pairing registry + all example .pics files, `mash-go/ARCHITECTURE-STACK.md`.

**Rollback trigger:** If ≥3 protocol payloads legitimately need >8 KB, reconsider 16 KB as the compromise — do not silently return to 64 KB.

**Related:** DEC-003 (256 KB MCU target). Follow-up: DEC-072 (planned — attributeList size bound; `kiss-implementation-plan.md` §L#3) will codify that feature attributeList responses must fit in a single message.

---

### DEC-070: Attribute Range Validation via YAML `min` / `max`

**Date:** 2026-04-23
**Status:** Accepted

**Context:**
Feature attributes declared in YAML had no schema-level way to express a physical range (e.g., `nominalFrequency` is either ~50 Hz or ~60 Hz — writing `0` or `250` is meaningless and likely a bug). The reference implementation's attribute store *did* have runtime range-checking plumbing (`pkg/model/attribute.go` `checkRange`) that honors `MinValue` / `MaxValue` in `AttributeMetadata`, and the generator *did* support `min:` / `max:` in YAML (`phaseCount` was the only attribute using them). The plumbing was idle because authors never populated the YAMLs.

**Decision:**
Numeric attributes whose physical or semantic range is well-defined SHOULD declare `min:` and/or `max:` in the feature YAML. The generator propagates these to `AttributeMetadata.MinValue` / `MaxValue`; the attribute store enforces on every `SetValue` / `SetValueInternal` call and returns `ErrAttributeOutOfRange` for violations. External wire writes that violate a range surface as the corresponding wire status.

Seed annotations landed in this DEC:
 - `Electrical.nominalVoltage`: `min: 85, max: 600` (V) — typical low- and low-kV grid envelope
 - `Electrical.nominalFrequency`: `min: 45, max: 65` (Hz) — 50/60 Hz grids with headroom
 - `EnergyControl.failsafeDuration`: `min: 7200, max: 86400` (s) — 2h floor, 24h cap (already described in the attribute's prose)
 - (Pre-existing: `Electrical.phaseCount`: `min: 1, max: 3`)

Expanded rollout (same commit series):
 - `ChargingSession.evStateOfCharge`: `min: 0, max: 100` (%)
 - `ChargingSession.evMinStateOfCharge`: `min: 0, max: 100` (%)
 - `ChargingSession.evTargetStateOfCharge`: `min: 0, max: 100` (%)
 - `Measurement.acFrequency`: `min: 45000, max: 65000` (mHz) — matches Electrical.nominalFrequency envelope at millihertz resolution

**Rationale:**
- Treats range as a property of the attribute, not a property of the caller. A single device-side bug that sets nominalFrequency to 0 is caught immediately instead of propagating through the EMS's forecasting code.
- Leverages existing plumbing — the DEC costs a YAML annotation and a generator regen, not new code.
- Scales incrementally. Add `min`/`max` where bugs have cost us (or would cost us); don't retrofit the entire attribute catalog in one pass. §H's generator refactor is a natural checkpoint to sweep the remaining numeric attributes.
- Avoids adding a "lint all attributes" step with false positives on attributes whose range is genuinely open-ended (instantaneous power, session energy, etc.).

**Rollout (not part of this DEC, but recorded so the next pass knows where to start):**
 1. ~~ChargingSession: `evStateOfCharge`, `evMinStateOfCharge`, `evTargetStateOfCharge` (0..100 %)~~ — landed in the expanded rollout above.
 2. EnergyControl failsafe limits — bounded by nameplate when device is fully commissioned (needs per-device evaluation, not a static range).
 3. ~~Measurement AC quantities have physics-plausible ranges (e.g., frequency deviation) — worth at most ±20 Hz from nominal.~~ — `acFrequency` landed in the expanded rollout; voltage/current ranges still pending per-endpoint evaluation.

**Implementation notes:**
- YAML key: `min:` / `max:`. Generator emits `MinValue: <typedLit>, MaxValue: <typedLit>` into `AttributeMetadata`.
- Runtime path: `pkg/model/attribute.go` `validateValue` → `checkRange`.
- Regression test: `TestAttributeRangeValidation` in `pkg/features/features_test.go` asserts the three seed annotations reject out-of-range writes.
- No change to the wire envelope; errors surface through the existing `ErrAttributeOutOfRange` → status mapping.

**Related:** DEC-051 (static attributeList — ranges are part of the static schema), DEC-072 (attributeList size bound — range metadata does not leave the device).

---

### DEC-074: featureMap Bit Semantics

**Date:** 2026-04-23
**Status:** Accepted (supersedes DEC-035's ambiguous framing)

**Context:**
DEC-035 introduced a Matter-inspired `featureMap` bitmap for capability discovery. Two downstream implementations conflated orthogonal ideas:
 1. Operational mDNS TXT carried an `FM` key echoing the feature-map hex — but mDNS packets are constrained, the key duplicated information present on the wire (`featureMap` attribute 0xFFFC, per feature), and bit semantics differ per feature so a single device-level "FM" hex number is lossy.
 2. The reference implementation's inspector rendered bits as symbolic names (e.g. `0x000B (CORE|FLEX|EMOB)`), which readers interpreted as "this device has an EMOB feature." That's wrong: EMOB is a capability variant of the Electrical/ChargingSession features, not a feature that exists alongside them. Bits encode **variants within** a feature, not feature presence.

**Decision:**
1. `featureMap` bits encode **capability variants within a feature**. Same feature type (e.g. Electrical) can carry different bits on different devices: EVSE → `CORE|FLEX|EMOB|V2X`, battery inverter → `CORE|FLEX|BATTERY`.
2. Each bit maps to a PICS conformance point (F00–F1F). Auto-PICS enumerates bits as individual conformance items — this is the primary downstream consumer.
3. **Feature presence** is discovered via the endpoint's `featureList` and the per-feature existence check, NOT by inspecting featureMap bits.
4. Operational mDNS TXT **MUST NOT** advertise a featureMap / FM key. Capability discovery requires a wire read of `featureMap` (0xFFFC) per feature.
5. Display tools render `featureMap` as raw hex (`0x0003`) without symbolic bit-name decoration. Symbolic names remain in per-feature specs where semantics are scoped to that feature.

**Rationale:**
- Matter's featureMap is scoped to a cluster (feature), not a device. DEC-035's cross-feature unification was a premature flattening.
- mDNS TXT budget is scarce; duplicated data is pure cost.
- Symbolic rendering looked helpful but actively misled readers about topology ("device has EMOB" vs "Electrical feature supports EMOB variant").
- Bits are still load-bearing: PICS derives them, conformance tests gate on them, per-feature attributes cross-reference them. Dropping bits would regress conformance. We drop the *advertisement* and *cross-feature symbolic rendering*, not the bits themselves.

**Implementation notes:**
- Removed: `pkg/discovery/types.go` `TXTKeyFeatureMap` constant, `OperationalInfo.FeatureMap`, `OperationalService.FeatureMap`; `pkg/discovery/txtrecord.go` encode/decode FM blocks; copy-through lines in `pkg/discovery/browser.go` and `pkg/discovery/mdns.go`.
- Removed: `internal/inspect/formatter.go` `FormatFeatureMap` function + its test; call site in `internal/inspect/inspector.go` now renders `Capabilities: 0x%04X` inline.
- Removed: `EndpointInfo` field 5 (`featureMap: uint32`) from `docs/features/device-info.md` — the same cross-feature aggregation rejected for mDNS TXT has no per-endpoint definition either.
- Preserved: `pkg/model/feature.go` `Feature.FeatureMap()` method; per-feature featureMap attribute (0xFFFC) as wire source of truth; `internal/testharness/runner/auto_pics.go` bit enumeration (load-bearing for PICS auto-discovery); feature YAMLs declaring bits.
- Test: `TestOperationalTXT_OmitsFeatureMap` (pkg/discovery) regression-guards the TXT omission; `TestInspector_FeatureMapHex` (internal/inspect) regression-guards the hex-only rendering.
- docs/features/README.md updated with corrected semantics.

**Related:** supersedes DEC-035's device-level framing; pairs with DEC-072 (planned) for attributeList capability discovery path.

---

### DEC-075: Process Lifecycle Is Opt-In

**Date:** 2026-04-23
**Status:** Accepted (clarifies DEC-034)

**Context:**
DEC-034 introduced ProcessStateEnum and the Pause/Resume/Stop/AdjustStartTime commands to cover EEBUS OHPCF-style "optional process" flexibility. The decision text called the process lifecycle "optional" but the enforcement surface was never spelled out. Readers interpreting the spec assumed a conformant EnergyControl had to participate in the whole lifecycle, including emitting meaningful `processState` transitions.

Inspection of real device types exposed the gap: a grid-tied EVSE that only understands "limit me to N watts" has no notion of AVAILABLE → SCHEDULED → RUNNING. Requiring it to expose the lifecycle would force spurious state reporting and add controller-side branches that never fire in practice. Worse, the 15 TC-INTERACTION conformance tests conflated "device implements EnergyControl" with "device implements the full process lifecycle", producing false negatives on simple-but-conformant devices.

**Decision:**
1. The process lifecycle — `processState` attribute plus `Pause` / `Resume` / `Stop` / `AdjustStartTime` commands — is **opt-in** at the per-feature level.
2. Devices that do not participate leave:
   - `isPausable = false` (id 14)
   - `isShiftable = false` (id 15)
   - `isStoppable = false` (id 16)
   - `processState = NONE` (id 80, already the default)
3. Controllers MUST check the corresponding capability flag before issuing a lifecycle command. Devices MAY respond to an unsolicited lifecycle command with `wire.StatusUnsupported`.
4. Conformance test suites MUST include profiles for EnergyControl **with** and **without** the process lifecycle. A test asserting lifecycle behavior must only apply to devices whose capability flag is true.
5. The capability flags are authoritative. `processState = NONE` alone is NOT a reliable opt-out signal (NONE is also a valid transient state for a lifecycle-participating device between tasks).

**Rationale:**
- Matches the shape of real hardware: EVSEs, grid inverters, sub-meters have no "optional process"; heat pumps and water heaters do.
- Keeps the single EnergyControl feature covering every load-control case in the MASH catalog without fragmenting into "EnergyControl-basic" vs "EnergyControl-lifecycle" variants.
- Makes the opt-out signal a local, cheap attribute read — no external profile lookup, no subscription, no handshake.
- Aligns with EEBUS OHPCF's actual usage: OHPCF is one of ~10 use cases, not the universal shape.

**Implementation notes:**
- `pkg/features/energy_control_gen.go` already defaults `isPausable`, `isShiftable`, `isStoppable` to `false` and `processState` to `NONE`. The generated `handlePause` / `handleResume` / `handleStop` guards against a nil handler without panicking. This DEC ratifies that design rather than changing code.
- Regression guard: `TestEnergyControl_ProcessLifecycleOptIn` in `pkg/features/features_test.go` locks in: default flag values, processState default, and non-panic behavior of handlePause/handleResume/handleStop when no handler is registered.
- Spec: `docs/features/energy-control.md` ProcessStateEnum section now has an explicit "ProcessStateEnum is Opt-In" subsection with the capability-flag table.
- Follow-up when §E service split lands: wire the unregistered-handler path to return `wire.StatusUnsupported` (code 10) at the dispatcher instead of `{success: false}`. Currently cleaner to address during the service restructure than in this DEC's scope.

**Related:** clarifies DEC-034 (ControlStateEnum + ProcessStateEnum). Follow-up for dispatcher: §E (pkg/service split).

---

### DEC-071: Subscription Notification Ordering Across Connections

**Date:** 2026-04-23
**Status:** Accepted

**Context:**
A write arrives on one connection; a subscriber on a different connection is configured to observe the attribute that the write mutates. The spec did not say whether the subscriber's notification lands before the writer's response or after. Both orderings are physically realizable on a concurrent server. Without a rule:
 - Writer A can issue a follow-on command assuming "the fleet has observed the change" while subscriber B hasn't received the notification yet.
 - Conformance tests that race a write against a subscription produce nondeterministic pass/fail.

**Decision:**
The device MUST deliver a notification caused by an acknowledged write on the subscriber's stream **before** sending the write response on the writer's stream. Notifications from an acknowledged write cannot "lag" the response.

**Rationale:**
- Gives both sides (writer and subscriber) a consistent view without requiring additional handshakes or sequence numbers.
- Matches the reference implementation's current dispatcher flow (notification fan-out happens inside the write's commit step, before the response is sent). This DEC ratifies that rather than changing behavior.
- Keeps the guarantee narrow — only notifications *caused by* the acknowledged write are ordered. Unrelated notifications follow §8.3 (in-order per subscriber, no cross-subscriber ordering).
- Delivery failure on a subscriber's stream does NOT block the write response; subscription semantics continue to handle missed notifications via reconnect/priming report.

**Implementation notes:**
- Spec text lives in `docs/transport.md §8.4 Write-Then-Notify Ordering`.
- Reference implementation: notification fan-out in `pkg/service/session_subscription_tracker.go` executes synchronously before the write handler returns.
- Testing: a multi-connection YAML test (writer zone + subscriber zone) is feasible with the existing harness; deferred alongside §E's integration-coverage strengthening (E-1), where the test fits naturally with other cross-cutting invariants.

**Related:** transport.md §8.3 (in-order per subscriber), DEC-052 (feature-level subscription).

---

### DEC-072: AttributeList Must Fit in a Single Message

**Date:** 2026-04-23
**Status:** Accepted

**Context:**
DEC-069 lowered the transport frame cap from 64 KB to 8 KB. A feature's `attributeList` (0xFFFB) is a CBOR array of uint16 IDs; on very wide features this could in principle approach the cap. The protocol has no chunking mechanism — a response that cannot fit in one frame has no compliant on-wire representation.

**Decision:**
An `attributeList` read response MUST fit in a single transport frame (≤ 8 KB). Feature designers keep the attribute count within budget. A feature whose natural attribute set would exceed the cap MUST be split across endpoints (multiple feature instances, each with a distinct attribute subset) rather than serialized through chunking.

Practical numbers:
- Observed: largest feature (EnergyControl) has <100 attributes; encoded `attributeList` is well under 1 KB.
- Safe cap: ~3000 attribute IDs per feature (2 bytes per ID + CBOR overhead, well under 8 KB).
- Reference implementation regression guard: `TestAttributeList_FitsInSingleMessage` in `pkg/features/features_test.go` encodes every built-in feature's `AttributeList()` as CBOR and asserts `len(encoded) ≤ 6144 bytes` (leaving 2 KB for Response envelope overhead).

**Rationale:**
- Avoids introducing chunking — a complexity premium with no real demand.
- Makes the per-feature attribute-count limit concrete and self-checkable.
- Aligns with the broader KISS posture: when a feature would exceed the cap, the design signal ("split endpoints") is the correct answer rather than a new protocol mechanism.

**Related:** DEC-069 (8 KB frame cap), DEC-051 (static attributeList).

---

### DEC-073: Failsafe-Limit Attributes Gated on acceptsLimits

**Date:** 2026-04-23
**Status:** Accepted

**Context:**
EnergyControl exposes `failsafeConsumptionLimit`, `failsafeProductionLimit`, and `failsafeDuration` as ReadWrite attributes so a controller can configure the fallback policy at commissioning time. Read-only endpoint types — `SUB_METER`, `PV_STRING`, `GRID_CONNECTION` — don't accept active limits, but the spec didn't say what should happen when a controller *tries* to write a failsafe-limit attribute on such an endpoint. Behavior was unspecified.

**Decision:**
Failsafe-limit attributes are writable only on endpoints that expose EnergyControl with `acceptsLimits = true`. On any other endpoint — including read-only endpoint types and any EnergyControl instance with `acceptsLimits = false` — a write to a failsafe-limit attribute MUST be rejected with `wire.StatusUnsupported` (status code 10). Read access remains unaffected.

**Rationale:**
- Mirrors the shape already established in DEC-075 for process-lifecycle commands: capability-flag attributes gate both commands *and* attribute writes that are semantically meaningless without the capability.
- Keeps the feature schema stable — no need for per-endpoint-type attribute pruning — while making the enforcement surface explicit.
- Read-only endpoint types (grid connection, PV string, sub-meter) shouldn't be controlled by a failsafe policy; they have no knob to turn when a controller disappears.

**Implementation notes:**
- Spec text: `docs/multi-zone.md §7 Failsafe Behavior` now includes the applicability paragraph.
- Enforcement surface: the reference implementation currently accepts these writes unconditionally (attributes are declared ReadWrite in the generated feature). Wiring the `acceptsLimits=false` rejection at the dispatcher level is deferred to §E alongside the similar DEC-075 follow-up for process-lifecycle commands — both want the same "capability-flag gate → StatusUnsupported" plumbing.

**Related:** DEC-026 (EnergyControl / acceptsLimits), DEC-034 (failsafe semantics), DEC-075 (process-lifecycle opt-in uses the same shape).

---

### DEC-076: Retain In-Session Certificate Renewal

**Date:** 2026-04-23
**Status:** Accepted (re-examines DEC-015)

**Context:**
The v3 KISS analysis flagged MASH's in-session certificate renewal subprotocol (MsgTypes 30–33, 814 LOC production + 1,203 LOC tests) as a candidate for simplification: could reconnect-based renewal do the same job with less code? DEC-076 is the explicit re-examination of that question. The full comparison lives in `docs/design/cert-renewal-comparison.md`.

**Decision:**
Keep the current in-session renewal design. Do not switch to a reconnect-based alternative.

**Rationale:**
1. **LOC savings are partial and mostly illusory.** Deleting the 814 LOC of renewal code costs ~300–400 LOC of new proactive-reconnect orchestration. Net savings: ~400 LOC — within a ~30 KLOC service package.
2. **Zero-downtime matters for energy control.** In-session renewal is invisible to EMS operations. Reconnect-based renewal introduces a ~200–1000 ms disconnect window in which `SetLimit` writes race against the rotation, which is a new operational fragility the spec currently doesn't have.
3. **PASE reuse is a tax.** Reconnect-based renewal is only simpler if the re-auth channel skips PASE. Any variant that re-runs PASE turns the setup code into a long-lived secret, weakening the one-shot commissioning posture (DEC-068).
4. **Asymmetric reversibility.** Switching away from in-session renewal is easy (delete 814 LOC). Switching back is hard (reintroduce MsgType allocations, state machine, nonce binding). The door stays open to re-evaluate; the door doesn't stay open to go back. So the bias is toward keeping it.
5. **The sub-protocol is the short-circuited reconnect.** MsgTypes 31/32 (CSR, cert install) would exist in either flow. The extra cost of in-session is MsgTypes 30 (request) and 33 (ack) plus the state-machine bookkeeping — not a large gain to unwind.

**Declined alternatives:**
 - **Full reconnect with PASE reuse.** Extends setup-code lifetime; weakens commissioning's one-shot posture.
 - **Full reconnect without PASE (TLS-only re-auth via operational cert).** Requires new ALPN routing for "accept operational cert, want CSR" — new complexity roughly matching what it deletes.
 - **Pre-derive renewal secret from session.** Equivalent in spirit to the current nonce-binding (DEC-047); just reshuffles code rather than removing it.

**Follow-ups (not part of this DEC):**
 - ~~Consider shortening the 30-day renewal initiation window to 7 days~~ — landed as DEC-077.
 - When §E (pkg/service split) lands, relocate the renewal files into the appropriate sub-package for locality.

**Related:** DEC-015 (original operational cert lifecycle), DEC-047 (commissioning security; nonce binding), DEC-066 (close commissioning connection after cert exchange), DEC-067 (single-port ALPN routing). Full evidence: `docs/design/cert-renewal-comparison.md`.

---

### DEC-077: Shorten Renewal Window to 7 Days

**Date:** 2026-04-23
**Status:** Accepted (follows W7 watchlist from `kiss-implementation-plan.md`)

**Context:**
DEC-015 established a 30-day renewal window — the controller initiates cert renewal when the device's operational certificate has 30 days or less remaining on its 1-year validity. The window was inherited from "reasonable default for industrial equipment" thinking rather than derived from MASH's specific threat model. W7 of the KISS implementation plan flagged it for re-examination after DEC-076 kept in-session renewal.

The window serves two competing pressures:
 - **Longer** (30 days): more slack for controllers to encounter a device that's been offline, cycle it back through renewal before any cert expires. Resilience to intermittent connectivity.
 - **Shorter** (7 days): a compromised device whose operational cert is stolen by an adversary has to be renewed AND a second usable cert has to coexist (both signed by the Zone CA, both valid) for the window's duration. Narrowing the window narrows the abuse period in which a stolen cert still has a validly-issued partner.

**Decision:**
Narrow `RenewalWindow` from 30 days to 7 days across both `pkg/cert/types.go` and `pkg/service/renewal_tracker.go`. Operational cert validity (`OperationalCertValidity`) remains 1 year. Grace period (`GracePeriod`) remains 7 days.

**Rationale:**
- MASH devices are IPv6-LAN-attached; intermittent multi-week disconnects are not a realistic operational mode (the device is offline in that case, and post-reconnection will refresh within a week).
- The 30-day "slack" was spending security budget on a resilience problem we don't have.
- 7 days aligns the renewal-initiation window with the grace period — the whole "expires-soon" window on both sides of nominal expiry is exactly 14 days. Easy to reason about.
- In-session renewal (DEC-076) has zero downtime; a 7-day window is more than enough for a controller to reach every connected device once during normal operation.
- If intermittent-disconnect evidence ever surfaces (e.g., a device class that parks offline for weeks), the constant is a single-line change.

**Implementation notes:**
- `pkg/cert/types.go`: `RenewalWindow = 7 * 24 * time.Hour`.
- `pkg/service/renewal_tracker.go`: same constant (intentional duplication — two packages, one value; merging the duplication is a follow-up for §E).
- Tests updated: `TestRenewalTracker_NeedsRenewal` now uses 5 days / 30 days split (was 25 / 100); `TestRenewalTracker_NeedsRenewalIndividual` boundary case is 7 days (was 30); other test cases adjusted to bracket the new window.
- `docs/security.md §3.1` + `§3.2` now say "7 days before expiry" with DEC-077 cross-reference.

**Related:** DEC-015 (cert lifecycle), DEC-076 (keep in-session renewal); closes Watchlist W7 in `kiss-implementation-plan.md`.

---

### DEC-078: Commissionable mDNS TXT Schema (canonical)

**Date:** 2026-04-24
**Status:** Accepted

**Context:**
Two candidate schemas for the `_mash-comm._tcp` TXT record existed in the documentation:

- `docs/discovery.md §2.2` listed `D`, `VP`, `CM`, `DT` (4 keys)
- `docs/testing/behavior/discovery.md §3.2` listed `D`, `cat`, `serial`, `brand`, `model`, `DN` (6 keys)

These are different models, not variants — they share only the `D` key. An implementer reading only one doc ships an incompatible advertisement. The reference implementation at `mash-go/pkg/discovery/txtrecord.go` implements the 6-key schema; all discovery conformance tests validate against it.

**Options Evaluated:**
1. Adopt the 4-key schema (`D, VP, CM, DT`) — would require rewriting the reference implementation and breaking conformance tests.
2. Adopt the 6-key schema (`D, cat, serial, brand, model, DN`) — matches the implementation and behavior spec.
3. Merge — cherry-pick fields from each.

**Decision:**
Adopt the 6-key schema from `docs/testing/behavior/discovery.md §3.2` as canonical. Update `docs/discovery.md §2.2` to match.

**Rationale:**
- **Implementation fidelity.** `mash-go/pkg/discovery/txtrecord.go` already ships the 6-key schema; all discovery conformance tests validate against it. Option 1 would break existing tooling.
- **Standards alignment.** The `cat` field uses EEBUS SHIP v1.1.0 category codes (1 = GCPH, 2 = EMS, 3 = e-mobility, …), integrating MASH with an established energy-domain taxonomy.
- **Expressiveness.** `cat` supports multi-category devices (e.g., `cat=2,5` for a hybrid EMS / inverter); single-string `DT` cannot.
- **UI enablement.** `serial`, `brand`, `model` support label-match pairing fallback when a QR is unavailable; `DT` alone cannot.
- **Simplicity.** Service presence already signals commissioning mode; a separate `CM` flag adds nothing.

**Declined Alternatives:**
- Option 1: would break implementation and conformance tests; `DT`/`CM` are also less expressive than the 6-key set.
- Option 3: the 4-key set's fields are redundant with or subsumed by the 6-key set; no merge is necessary.

**Implementation:** No code changes required; the reference already matches the canonical schema.

**Related:** DEC-067 (single-port ALPN routing), DEC-074 (featureMap removed from mDNS TXT).

---

### DEC-079: Limit-Override Notification Mechanism

**Date:** 2026-04-24
**Status:** Accepted

**Context:**
When a lower-priority zone's limit is superseded by a higher-priority zone's more-restrictive limit (per DEC-024, "most restrictive wins"), the affected controller needs to know its limit isn't the active constraint. `docs/multi-zone.md §5.1` stated the controller "is notified" without defining a mechanism.

DEC-024 implied the awareness channel was subscription-based ("Can infer: if effectiveLimit < myLimit, someone else is more restrictive"), but `multi-zone.md` did not make this explicit. The reference implementation exposes only the effective-value subscription path; no synchronous status code, dedicated notification, or `lastOverridingZone` attribute exists.

**Options Evaluated:**
1. **Synchronous Write response status** — add an `OVERRIDDEN` status code returned from Write when the committed effective value is stricter than the written value.
2. **Dedicated async notification** — a new wire primitive specifically for override events.
3. **Implicit via subscription divergence** — controllers subscribe to `effectiveConsumptionLimit` / `effectiveProductionLimit` and compare the effective value to their own `myConsumptionLimit` / `myProductionLimit`. Divergence = override.
4. **Dedicated attribute** (e.g., `lastOverridingZone`) — subscribed to like any attribute.
5. **Extend ControlStateEnum** — add a zone-override variant distinct from the existing safety / legal OVERRIDE state.

**Decision:**
Adopt Option 3: **implicit via subscription divergence.** Formalize the mechanism in `docs/multi-zone.md §5.1`.

**Mechanism:**
- Controllers subscribe to `effectiveConsumptionLimit` / `effectiveProductionLimit` as normal attributes.
- Device recomputes the effective value on any zone's limit change (min across zones).
- Subscription notification fires on effective-value change per normal coalescing (`minInterval`) / heartbeat (`maxInterval`) rules.
- A controller detects override by comparing its own `myLimit` to the observed `effectiveLimit`: `myLimit > effectiveLimit ⇒ another zone imposed a stricter constraint`.

**Rationale:**
- **Zone isolation.** The device does not track or expose *which* zone overrode — only the effective value matters operationally. Divergence inference preserves this isolation.
- **Reuses existing wire surface.** No new status codes, attributes, or notifications; the subscription path already carries this information.
- **Symmetry with existing design discipline** (DEC-042, DEC-045): MASH's stance is "devices report state directly; controllers infer — no heartbeat-based inference." Divergence inference is the analogous pattern for override awareness.
- **Cost of alternatives.** Options 1 (new status), 2 (new primitive), 4 (new attribute), 5 (enum change) all add protocol surface for information the subscription already conveys.

**Limitations:**
- Detection latency is bounded by subscription `maxInterval` (heartbeat), not instantaneous.
- Controller cannot identify *which* zone imposed the override — only that some zone did.
- Controllers that do not subscribe to `effective*` attributes will not detect override.

**Declined Alternatives:**
- Option 1: the override value isn't known at Write time in the general case — a higher-priority zone's *later* write can override an earlier accepted one.
- Options 2 / 4 / 5: add wire or model surface for a signal already derivable from subscription state.

**Implementation:** No changes required; the reference implementation already exposes this divergence pattern.

**Related:** DEC-024 (Limit Resolution — Most Restrictive Wins), DEC-042 (Explicit state, no inferred heartbeat), DEC-052 (Feature-Level Subscription).

---

### DEC-080: Remove Events from the Protocol Surface

**Date:** 2026-04-24
**Status:** Accepted (supersedes the `eventList` reservation in DEC-035)

**Context:**
The interaction model (`docs/interaction-model.md §9`) documented a full event subsystem — timestamped, monotonically-numbered, append-only records delivered via Subscribe. The global-attribute slot `eventList` (0xFFF8) was reserved under DEC-035 (2025-01-25) to advertise supported event IDs per feature.

Fifteen months later:
- No feature YAML at `docs/features/*/1.0.yaml` defines any events.
- The code generator at `mash-go/cmd/mash-featgen/` has no event support.
- `SubscribePayload` in `mash-go/pkg/wire/message.go` has no `eventIds` field.
- No reference-implementation code path serializes events on the wire.
- The `emitEvent()` calls in `mash-go/pkg/service/` are Go in-process observability callbacks, unrelated to wire events.

The spec described a capability the protocol did not implement; `eventList` claimed attribute slot 0xFFF8 without purpose. The plausible event use cases turn out to be already covered by MASH's attribute-subscription model:

| Event use case one might expect | MASH's existing mechanism |
|---------------------------------|---------------------------|
| Fault reporting | `Status` feature attributes + subscription |
| State transitions | `controlState` / `processState` subscription (DEC-034, DEC-042) |
| Cert rotation | Dedicated MsgTypes 30–33 (DEC-076) |
| Session lifecycle | `ChargingSession` attributes + subscription |

**Options Evaluated:**
1. **Remove entirely** — delete `interaction-model.md §9`; free the 0xFFF8 slot; strip `eventList` from docs.
2. **Activate with a concrete use** — define events for a specific feature (e.g., `Status` faults).
3. **Keep reserved, reframe prose** — mark §9 explicitly as "Reserved for Future Use."

**Decision:**
Adopt **Option 1: remove events from the protocol surface.**

**Actions:**
- Delete `docs/interaction-model.md §9`.
- Remove the `eventList` (0xFFF8) row from `docs/discovery.md §4.1`.
- Strip `0xFFF8` from all example `attributeList` values.
- No code changes — the reference implementation already has no event path to remove.

**Rationale:**
- **YAGNI validated by evidence.** Fifteen months with zero consuming features is a strong signal that events are not currently needed. If a need had existed, some feature author would have reached for them.
- **State-convergence model already covers the use cases.** MASH's attribute-subscription discipline (subscribe to state, infer events from state changes) is load-bearing for exactly the situations where another protocol would use events. Adding an event primitive on top would duplicate mechanism.
- **Honesty to readers.** The "Reserved for Future Use" framing is documentation debt: readers of a large spec should not have to learn which sections are live and which are aspirational.
- **Re-adding is cheap.** If a future feature genuinely needs events, a new DEC can (a) re-claim 0xFFF8 (still within the reserved 0xFFF0–0xFFFF range), (b) re-specify the wire format (≈ monotonic counter + timestamp + id + payload; trivial), (c) extend the feature YAML and code generator, (d) add `eventIds` to `SubscribePayload`. The design work is hours, not days.

**Supersession:**
This DEC supersedes the `eventList` reservation made in DEC-035. DEC-035's reservations of `featureMap`, `attributeList`, `acceptedCommandList`, and `generatedCommandList` remain in effect.

**Declined Alternatives:**
- **Option 2 (activate now):** no concrete driver requirement. Picking events to define without an operational need is speculative design.
- **Option 3 (keep reserved):** preserves misleading prose and claims a slot without purpose. "Reserved for future use" that never arrives becomes permanent spec debt.

**Follow-up:** If a feature ever needs events, write a new DEC that re-designs the subsystem with the concrete use case in mind.

**Related:** DEC-035 (Global Attributes — partially superseded), DEC-042 (explicit state over inferred), DEC-052 (Feature-Level Subscription).

---

### DEC-081: Remove `_mashd._udp` Commissioner-Discovery Service

**Date:** 2026-04-24
**Status:** Accepted

**Context:**
The discovery layer defined four mDNS service types:

- `_mash-comm._tcp` — commissionable devices (device → controller)
- `_mash._tcp` — operational devices (device → controller)
- `_mashp._udp` — pairing request (controller → device)
- `_mashd._udp` — commissioner/controller announcement

`_mashd._udp` was specified (`docs/discovery.md §2.3` — removed in this change; `docs/testing/behavior/discovery.md §2.3`) as an affordance for "device-initiated pairing": a device with a display would browse `_mashd._udp`, show the user a list of controllers ("Home Energy", "Office EMS"), and "initiate commissioning" with the selected one.

**The architectural problem:**
In MASH the device is a pure TLS server. Every commissioning and operational connection is initiated by the controller (commissioner-side of PASE; client-side of TLS). The device cannot open a socket outward. Therefore:

1. After the user "picks Home Energy" on the device's display, the device's only action is to open its commissioning window and advertise `_mash-comm._tcp`.
2. Any controller that holds the setup code can now complete PASE — there is no field in `_mash-comm._tcp` TXT that restricts the window to a specific zone.
3. The device's "choice" therefore has no protocol-enforceable effect; authentication is keyed on the setup code (QR-derived), not on the controller's zone identity.

Matter reaches the same conclusion and has no equivalent service type: controller-initiated commissioning plus passcode-gated PASE makes "device browses for controllers" unnecessary. MASH inherits this asymmetry but had retained the extra service type for UX reasons.

**Evidence of vestigial status in the reference implementation:**
- `pkg/service/` never calls `discovery.AddCommissionerZone`, `UpdateCommissionerZone`, `RemoveCommissionerZone`, or `AdvertiseCommissioner`. No controller process advertises `_mashd._udp`.
- `pkg/service/` never calls `BrowseCommissioners`. No device process consumes it.
- All references to `_mashd._udp` in the Go tree are inside `pkg/discovery/` itself (plumbing), the test harness (`internal/testharness/runner/`) for conformance-test scaffolding, and mock stubs in test files.
- The `TC-MASHD-*` conformance suite exists only to validate the scaffolding — no higher-level test depends on `_mashd._udp` to exercise a protocol flow.

**Options Evaluated:**
1. **Remove entirely** — delete `_mashd._udp` from the service-type registry, remove supporting Go code, remove TC-MASHD conformance cases.
2. **Keep as an informational/optional service** — rewrite the docs to drop the "device picks controller" flow and retain the service type for installer-tool topology browsing.
3. **Retain as specified** — accept that the flow described in the spec is aspirational.

**Decision:**
Adopt **Option 1: remove `_mashd._udp` from the protocol surface.**

**Actions:**
- Delete `§2.3 Commissioner Discovery` from `docs/discovery.md` and `docs/testing/behavior/discovery.md`.
- Remove the `_mashd._udp` row from every service-type table (`docs/discovery.md §2.1`, `docs/protocol-overview.md`, `docs/stack-architecture.md`, `docs/protocol-comparison.md`, `docs/testing/behavior/connection-establishment.md`, `mash-go/ARCHITECTURE-STACK.md`).
- Delete the `TC-MASHD-*` conformance suite (`docs/testing/README.md`, `docs/testing/behavior/discovery.md §4.1`, `mash-go/testdata/cases/discovery-behavior-tests.yaml`).
- Remove `MASH.S.DISC.SVC_COMMISSIONER` from `mash-go/testdata/pics/protocol-common.yaml`.
- Remove Go API: `ServiceTypeCommissioner`, `CommissionerService`, `CommissionerInfo`, `ServiceCommissioner`, `TXTKeyZoneName`, `TXTKeyDeviceCount`, `BrowseCommissioners`, `AdvertiseCommissioner`/`UpdateCommissioner`/`StopCommissioner`, `AddCommissionerZone`/`UpdateCommissionerZone`/`RemoveCommissionerZone`, `EncodeCommissionerTXT`/`DecodeCommissionerTXT`, `ToCommissionerService`/`entryToCommissioner`.
- Regenerate `pkg/discovery/mocks/` (mockery).
- Remove test-harness scaffolding: `ActionBrowseCommissioners`, `handleBrowseCommissioners`, the commissioner branches in `mdns_observer.go` / `discovery_handlers.go`.
- Update `mash-deepdive.html` mDNS service-type table.

**Rationale:**
- **The device's "pick" is not enforceable.** In a pure server-device model, "device browses for controller" cannot drive a protocol state change that distinguishes the picked controller from any other passcode-holding controller. Documenting it as such misleads readers.
- **Every legitimate trigger for opening a commissioning window already exists without `_mashd._udp`:** user action (button, QR scan, UI), controller-initiated `_mashp._udp`, and factory-default (DEC-059 — window open when no zones).
- **One fewer service type in constrained implementations.** ESP32-class devices implement mDNS from scratch; every service removed saves flash, RAM, and an advertise/browse path.
- **Convergence with Matter.** Matter uses the same "passcode + controller-initiated PASE" model and has no commissioner-announce service; removing `_mashd._udp` aligns MASH with a proven simpler model.
- **Re-adding is cheap.** If a future use case (e.g., zero-touch onboarding with cryptographic controller identity) needs it, a new DEC can reinstate the service type.

**Supersession:**
None. `_mashd._udp` was not a formally decided service type in an earlier DEC — it existed from the initial discovery design. This DEC formalizes its removal.

**Declined Alternatives:**
- **Option 2 (keep as informational):** preserves a service type whose only protocol-layer purpose is "help installer tools draw topology diagrams." That is a tooling concern, not a protocol concern, and can be re-introduced later if justified. Keeping it now perpetuates the misleading "device picks controller" pattern in existing docs.
- **Option 3 (retain as specified):** explicitly rejected — the flow as specified does not work, per the architectural argument above.

**Follow-up:** If a future feature requires device-side awareness of controllers (e.g., zero-touch provisioning with controller-side cryptographic identity), a new DEC should re-introduce the service type with a concrete protocol-state-change semantics, not just a UX affordance.

**Related:** DEC-048 (commissioning window duration), DEC-059 (auto re-enter commissioning on last zone removal), DEC-067 (ALPN-routed single port), DEC-078 (commissionable TXT schema).

---

### DEC-082: Two-Port Deployment as Conformant Alternative to Single-Port ALPN Routing

**Date:** 2026-04-25
**Status:** Accepted

**Context:**
DEC-067 specifies single-port ALPN-based routing using `GetConfigForClient` to return different `tls.Config` instances for commissioning (`mash-comm/1`) and operational (`mash/1`) connections. This is clean in Go because `tls.Config.GetConfigForClient` is a first-class primitive: it receives the parsed `ClientHelloInfo` (including ALPN list) before cert and `ClientAuth` decisions are locked, and can return an entirely different config.

While porting MASH to Python (mash-py), this assumption surfaced as a concrete portability burden: Python's stdlib `ssl` module has no equivalent callback. `SSLContext.sni_callback` only fires on SNI, and `set_alpn_protocols` only chooses among offered protocols — neither can swap the entire TLS context based on the offered ALPN list. The same gap exists in C with mbedTLS, in Zig's `std.crypto.tls`, and to a lesser extent in Rust's rustls (where it is doable but awkward).

The portability options for non-Go implementations are:

1. Implement a pre-TLS dispatcher: read the ClientHello bytes, parse the ALPN extension, then drive the TLS handshake from `MemoryBIO` objects. ~400 LOC of TLS-protocol code per language, adds a bug-prone security-sensitive code path.
2. Deploy two listeners on different ports — one per ALPN — each with its own TLS config. Trivial in any language; small wire-layout deviation (mDNS records advertise different ports per service).
3. Spec out the first option as required in non-Go languages, leaving the burden permanent.

This decision formalizes option 2 as conformant.

**Decision:**
Two-port deployment is conformant alongside single-port. Single-port (DEC-067) remains the recommended pattern for implementations with dynamic-TLS-config primitives; two-port is the canonical pattern for implementations without.

**Two-Port Deployment Pattern:**
- **Commissioning listener.** Separate TCP port (e.g., 8444). TLS config: self-signed stable cert (per DEC-067), `ClientAuth = NoClientCert`, ALPN `mash-comm/1` exclusively.
- **Operational listener.** Separate TCP port (default 8443). TLS config: Zone CA-signed cert per zone (selected via SNI per multi-zone semantics), `ClientAuth = RequireAndVerifyClientCert`, ALPN `mash/1` exclusively.
- **mDNS SRV records** advertise the actual port for each service (already canonical — the SRV `port` field is authoritative per service instance).
- **Commissioning window closure** is enforced by stopping the commissioning listener entirely (or rejecting connections at accept time), equivalent in effect to single-port's `GetConfigForClient` rejection.

**Single-Port Deployment Pattern (per DEC-067):**
Unchanged. Implementations with `GetConfigForClient`-equivalents may serve both ALPN values on a single TCP port (default 8443). All DEC-067 properties (security isolation, strict mTLS, embedded-friendliness) hold.

**Conformance Equivalence:**
Both deployments produce identical wire behavior. Once a TLS connection is established, every CBOR message, every PASE message, every cert exchange, and every operational interaction is byte-identical regardless of deployment shape. Conformance tests (`mash-test`) discover the device via mDNS and connect to whatever port the SRV record advertises; no test changes are required.

**Rationale:**
- **Multi-language portability.** Removes the implicit assumption that the implementer's TLS library exposes a `GetConfigForClient`-equivalent primitive. C, Zig, Rust, and Python implementations may now use the deployment shape that best matches their TLS library without adding bespoke ClientHello-parsing code.
- **Wire compatibility preserved.** No new message types, no new protocol states, no behavior change visible to peers. The deviation is purely in listener layout, observable only via mDNS port advertisements.
- **Security model preserved.** Two-port deployments achieve identical security guarantees: separate listeners with independent TLS configs prevent operational cert leakage to commissioning peers (the original DEC-067 concern) by the same mechanism — different `tls.Config` objects, just bound to different sockets instead of selected by callback.
- **Resource cost is small and bounded.** Two listeners cost an additional TCP socket and a small per-listener accept-loop overhead (~50-200 KB on full Python; smaller on embedded). Embedded implementations preferring the single-socket pattern can still use single-port.
- **mDNS already supports it.** SRV records carry per-service port — no new TXT keys, no new service types.
- **Reversible.** A single-port implementation never observes anything different about a two-port peer (it dials the port advertised by mDNS); a two-port implementation never observes anything different about a single-port peer. The two patterns interoperate without coordination.

**Implementations:**
- **mash-go (canonical reference):** continues to use single-port deployment (DEC-067 unchanged).
- **mash-py (Python reference):** uses two-port deployment due to the absence of `GetConfigForClient` in Python's `ssl` module. See `mash-py-tdd-plan.md` Decision A.
- **Future C/Zig/Rust ports:** may choose either pattern based on TLS library ergonomics.

**Implementation notes:**
- The mDNS SRV `port` field is authoritative per service. Implementations MUST consult it rather than assuming a default.
- The default port 8443 remains the recommended port for the operational service (`_mash._tcp`). The commissioning service (`_mash-comm._tcp`) has no canonical default port in two-port deployments — implementations choose a free port and advertise it via SRV.
- Single-port implementations advertise both `_mash._tcp` and `_mash-comm._tcp` SRV records pointing at the same port (8443). Two-port implementations advertise different ports per service. Browsers see a difference only in port number.
- The connection cap (DEC-064: max_zones + 1) is per-device, not per-listener. Two-port deployments enforce it across both listeners.
- DEC-061 (message-gated commissioning locking) applies regardless of deployment shape: in both patterns, the commissioning lock fires on first PASERequest, not on TCP accept.

**Declined Alternatives:**
- **"Mandate single-port for all implementations."** Rejected. Forces ~400 LOC of TLS-protocol parsing in every non-Go language, which is bug-prone and adds a security-sensitive code path duplicated across the ecosystem.
- **"Mandate two-port for all implementations."** Rejected. Go's single-port pattern is genuinely simpler given `GetConfigForClient`, and forcing two listeners would regress the existing reference implementation for no benefit.
- **"Drop TLS during commissioning (Matter style)."** Rejected. PASE-derived AES-CCM framing would replace TLS during commissioning, but this adds bespoke AEAD-channel implementation cost across all languages — worse for portability than the problem it solves. Custom-crypto framing is harder to get right than standard TLS configuration.
- **"Implementation note in transport.md, no spec change."** Rejected. A note alone leaves two-port as a deviation from the spec letter; this DEC makes both patterns first-class conformant.

**Related:** DEC-067 (single-port routing — alternative deployment, fully preserved), DEC-061 (message-gated commissioning locking — applies to both patterns), DEC-064 (connection cap — per-device, both patterns), DEC-078 (mDNS TXT schema — SRV port already authoritative)

---

### DEC-083: CBOR Tag 77 Wrapper for Control Messages

**Date:** 2026-04-25
**Status:** Accepted

**Context:**
Control messages (Ping/Pong/Close) and application Responses share a CBOR map shape `{1: small-int, 2: small-int}` with no other keys. Specifically, a Response of the form `{1: messageId, 2: status}` with no payload (key 3 absent) is byte-identical to a ControlMessage of the form `{1: type, 2: sequence}` whenever `messageId` falls in the range {1, 2, 3} — the same range as `ControlMessageType` (Ping=1, Pong=2, Close=3). The receiver has no structural way to distinguish them.

A `peek_message_type` classifier resolves the tie heuristically (mash-go's `pkg/wire/codec.go:230-238` routes ambiguous frames to CONTROL). Audit of mash-go's five peek consumers (2026-04-25) found that none consult session state as a tiebreaker — every dispatcher routes purely on peek's verdict:

| Site | Action when peek misclassifies a Response as CONTROL |
|---|---|
| `pkg/transport/server.go:367-379` | msgID=1 sends spurious Pong; msgID=2 drops; **msgID=3 closes the connection** |
| `pkg/service/device_service_zone.go:248-274` | Same — **msgID=3 tears down the zone connection** |
| `pkg/service/device_session.go:106-160` | No CONTROL case in switch, no default — **silently dropped** |
| `pkg/service/zone_session.go:121-133` | `default: ignore` — **silently dropped** |

The msgID=3 case is the most severe: every session that uses sequential messageIds reaches msgID=3 on the third request. A no-payload error response with msgID=3 (legitimate per current spec) gets decoded as `ControlMessage{Type: Close}` and the entire connection is torn down with no diagnostic.

Today's mash-go is "lucky" because all production `sendErrorResponse` paths attach a non-empty `ErrorPayload` (key 3 present, no collision). But:
- `protocol_handler.go:677` returns `Response{Status: Success, Payload: result}` for Invoke; if `result` is nil, the encoded response has no key 3.
- The wire format permits the bug regardless of mash-go's current handlers — a non-Go implementer constructing `Response{MessageID: 2, Status: Success}` with no payload produces bug-triggering bytes today.

The ambiguity surfaced during the mash-py port (`mash-py-spec-issues.md` Issue #2). Resolving it by spec text alone ("peek is a hint, session state is authoritative") would require ~1 day of dispatcher-rewrite work in four mash-go call sites and equivalent work in every future port to thread an in-flight messageId tracker through the receive path.

**Decision:**
Wrap every Control message in **CBOR tag 77** (`0xD8 0x4D`). The presence of the tag at the start of a frame is the unambiguous signal that the frame is a control message; absence of the tag means the frame is an application-layer message (Request/Response/Notification). Peek classification reduces to "first byte is `0xD8` and second is `0x4D` → CONTROL".

**Wire form before:**
```
{1: type, 2: sequence?}      # Control map at top level
A2 01 01 02 01               # Ping(seq=1) — 5 bytes
A1 01 03                     # Close       — 3 bytes
```

**Wire form after:**
```
77({1: type, 2: sequence?})  # Control map wrapped in CBOR tag 77
D8 4D A2 01 01 02 01         # Ping(seq=1) — 7 bytes (+2)
D8 4D A1 01 03               # Close       — 5 bytes (+2)
```

**Tag number rationale:**
- Tag 77 ('M' in ASCII = MASH) is in the IANA CBOR Tag Registry's "Specification Required" range and (per the registry as of 2026-04-25) currently unassigned.
- 1-byte minor argument form (`0xD8 0x4D`) keeps the overhead at exactly 2 bytes per control message regardless of message type.
- Implementations MUST treat the exact tag value as load-bearing — no other tag value indicates a control message; receivers MUST reject any other tag at the top level of a frame as a protocol error.
- IANA registration of tag 77 for MASH control messages SHOULD be filed before 1.0 finalization. Until registered, no other CBOR-using protocol is known to use this tag value.

**Cost:**
- **Wire overhead.** +2 bytes per control message. At a 30-second Ping interval (per `transport.md` §6.1), the steady-state overhead is ~5.7 bytes/minute per connection — far below noise.
- **Spec change.** `transport.md` §6.2/6.3 updated to show the tagged wire form; `interaction-model.md` §3 updated to document control as the only tagged top-level frame type. No other protocol semantics change.
- **Implementation change.** ~30 lines in `pkg/wire/codec.go` (encode/decode/peek), mirrored in `mash_py/wire/codec.py` and `peek.py`. The peek heuristic that previously disambiguated control from response collapses into a single tag check, *reducing* total LOC. All saved control-message vectors and any hand-crafted control-message hex literals in tests need to be regenerated.
- **Inter-version compatibility.** This is a wire-format breaking change. A pre-DEC-083 peer sending a tagless Ping to a post-DEC-083 receiver would be misclassified as a Request and rejected by `Request.Validate`. Conversely, a post-DEC-083 peer sending a tagged Ping to a pre-DEC-083 receiver would fail to decode. Because MASH is pre-1.0 with no external deployments yet, the change lands in lockstep across mash-go and mash-py; future implementers see only the tagged form.

**Rationale:**
- **Eliminates the ambiguity at the wire level.** A spec-text fix would leave a permanent cognitive tax on every reader and every implementer; a wire-level fix removes it once. Future implementations have no opportunity to recreate the bug.
- **Cheap and reversible at this stage.** mash-go is the only existing implementation, mash-py is mid-port (Phase 1), and no production deployments depend on the tagless wire form. The window for a wire-format change is open and won't be cheaper later.
- **Eliminates the heuristic peek logic.** mash-go's peek classifier carries ~80 lines of "control vs response" disambiguation (codec.go:181-272). Post-DEC-083, the equivalent logic is a 2-byte prefix check. Less code, less attack surface, no edge cases.
- **Aligned with how Notification was already disambiguated.** Notifications use the sentinel `messageId == 0` to signal their type — a structural marker, not a heuristic. Control messages now follow the same pattern (tag instead of sentinel because field 1's encoding is more constrained than messageId's).

**Declined Alternatives:**
- **Option A: Document peek as a hint, require dispatchers to consult session state.** Rejected post-audit. None of mash-go's dispatchers consult session state today; the documentation would mismatch the implementation. Even with implementation updates, the cost is ~1 day per language and the cognitive tax is permanent.
- **Option B: Reorder peek so `status > 4` wins over the control check.** Rejected as a partial fix. Status values 0-4 (`SUCCESS`, `INVALID_ENDPOINT`, `INVALID_FEATURE`, `INVALID_ATTRIBUTE`, `INVALID_COMMAND`) plus msgID ∈ {1,2,3} plus no-payload would still misclassify. The worst case (msgID=3 + Status=Success + no payload → connection close) is not covered.
- **Option C: Forbid messageIds in {1, 2, 3}.** Rejected. Wastes 3/2³² of the messageId space, requires every existing test corpus to be rewritten, and adds a "magic number" rule with no structural justification.
- **Option E: Always include key 3 in Response (carrying CBOR null when no payload).** Rejected. +1 byte per Response in the common case, where pings are far rarer than responses; the cost falls in the wrong place.
- **Option G: Use a sentinel field-1 value for control messages (parallel to Notification's `messageId=0`).** Rejected. The candidate sentinel range is already small (`messageId` is uint32 and any specific value is a wasted slot). Tag-based disambiguation is cleaner because tags are by-design out-of-band relative to the value space.

**Implementations:**
- **mash-go:** `pkg/wire/codec.go` updated to wrap encode/unwrap decode in `cbor.Tag{Number: 77, Content: ...}`; `PeekMessageType` updated to recognize `D8 4D` as the unique CONTROL marker.
- **mash-py:** `mash_py/wire/codec.py` and `peek.py` updated to use `cbor2.CBORTag(77, ...)`; the heuristic in `peek.py` is replaced by the tag prefix check.
- **mash-py-spec-issues.md Issue #2** marked Resolved by DEC-083.

**Implementation notes:**
- The tag MUST be at the top of the frame (i.e., `data[0:2] == 0xD8 0x4D`). Implementations MUST NOT accept a control message buried under nested CBOR structure.
- The strict-decode pre-pass (mash-py `_strict.py`) traverses tags transparently — no change required there. Any tag at the top level passes through; only the codec's tag-number check enforces the 77-only contract.
- Decoders MUST reject a frame that begins with the tag-77 prefix but whose wrapped value is not a CBOR map matching the ControlMessage shape (`{1: type, 2: sequence?}`).
- Encoders MUST NOT emit any tag other than 77 at the top level of a frame. Tagged sub-values inside payloads (e.g., a Time tag inside an attribute value) remain permitted.

**Related:** DEC-069 (8 KB max message — tag overhead well within the cap), DEC-082 (two-port deployment — tag wrapping applies identically to both deployment shapes), `mash-py-spec-issues.md` Issue #2 (resolved by this DEC).

---

### DEC-084: SPAKE2+ Algorithm Specification (RFC 9383 + PBKDF2)

**Date:** 2026-04-26
**Status:** Accepted

**Context:**
DEC-014 ratified SPAKE2+ as the pairing protocol but specified only the high-level shape (8-digit setup code, P-256 curve). The cryptographic primitives — M / N derivation, HKDF chains, transcript composition, confirmation MAC layout, point validation — were left to `docs/testing/behavior/commissioning-pase.md §2`. That section was written prescriptively but never reviewed against a working implementation; the Go reference (`mash-go/pkg/commissioning/spake2plus.go`) was written 4 days later and silently chose different primitives in eight places without amending the spec or filing a DEC.

The mash-py port (Phase 7, 2026-04-26) was the first independent reading. It surfaced that:

- The spec is **unimplementable as written**. `K_shared` is invoked but never defined. `M = G * H(label)` is annotated "with hash-to-curve per RFC 9380" — but RFC 9380 produces points and `G * H(label)` requires `H` to produce a scalar; the formula is type-mismatched. `PASE_X` declares `bytes[32]` for compressed P-256 points, but compressed P-256 is 33 bytes (uncompressed is 65); neither is 32.
- The Go implementation has **exploitable security weaknesses** that the spec doesn't catch. Most importantly, neither artifact uses a Memory-Hard KDF before HKDF — verifier theft makes the 27-bit setup code brute-forceable in seconds on a GPU. Go's source contains a comment admitting this gap (`spake2plus.go:89`). RFC 9383 §4 *requires* an MHF for low-entropy passwords. The Go transcript also lacks RFC 9383 §3.3 length prefixes (canonicalization attacks); point validation rejects off-curve points but not the identity element (degenerate-point attacks).
- The eight specific findings are catalogued in `mash-py-spec-issues.md` Issue #5.

`mash-py-tdd-plan.md` Phase 7 was restructured into 7A → 7B → 7C → 7D in response. This decision is the output of 7A.

**Decision:**
Replace the SPAKE2+ specification in `commissioning-pase.md §2` and `§4.2` with the algorithm defined below. The new spec:

- **Standardizes** on RFC 9383 + Matter conventions where they exist (fixed M/N, length-prefixed transcript, KcA/KcB confirmation MAC keys).
- **Adds** a Memory-Hard KDF (PBKDF2-HMAC-SHA256, 100,000 iterations) before any HKDF chain. This is the security-critical addition.
- **Preserves** the spec's MASH-specific HKDF info-string scheme for K_main / K_confirm / K_encrypt (the one place the original spec was clearer than RFC 9383's terse split).
- **Defines** every previously-undefined primitive (`K_shared`, transcript composition, identity strings, point validation rules).

**Algorithmic specification.** The full text lives in `commissioning-pase.md §2.2 – §2.5` (rewritten in this DEC). Summary:

| Primitive | Specification |
|---|---|
| Curve | P-256 (`secp256r1`), per DEC-014 |
| Hash | SHA-256 |
| KDF | HKDF-SHA256 (RFC 5869) |
| MAC | HMAC-SHA256 (RFC 4231) |
| Memory-Hard KDF | PBKDF2-HMAC-SHA256 (RFC 8018), **100,000 iterations** |
| **M, N points** | RFC 9383 §4 fixed P-256 constants verbatim. M.x = `886e2f97ace46e55ba9dd7242579f2993b64e16ef3dcab95afd497333d8fa12f`, M.y = `5ff355163e43ce224e0b0e65ff02ac8e5c7be09419c785e0ca547d55a12e2d20`; N.x = `d8bbd6c639c62937b04d997f38c3770719c629d7014d49a24b4f98baa1292b49`, N.y = `07d60aa6bfade45008a636337f5168c64d9bd36034808cd564490b1e656edbe7`. |
| **Salt** | Derived: `salt = SHA-256("MASH PBKDF2 salt v1" \|\| discriminator (2 bytes BE) \|\| setup_code (UTF-8))[:16]`. Both sides compute independently from QR-code data; no exchange required. |
| **w0, w1** | `mhf_out = PBKDF2-HMAC-SHA256(password=setup_code_utf8, salt, iterations=100000, dkLen=80)`. Then `w0 = mhf_out[0:40] mod n`, `w1 = mhf_out[40:80] mod n`. (40-byte slices reduced mod the P-256 curve order `n` — the Matter pattern, gives near-uniform distribution per RFC 9383 §3.2.) |
| **L** (verifier point) | `L = w1 · G`. The device stores `(w0, L)` and discards `w1`. |
| **Identity strings** | Protocol constants: ControllerIdentity = `b"mash-controller"`, DeviceIdentity = `b"mash-device"`. No per-session nonces (session entropy comes from the random ephemeral scalars `x` and `y`). |
| **Public values** | `pA = x · G + w0 · M` (controller), `pB = y · G + w0 · N` (device). Encoded SEC1 uncompressed (`0x04 \|\| X \|\| Y`, 65 bytes). |
| **Shared values** | Controller computes `Z = x · (pB - w0 · N)` and `V = w1 · (pB - w0 · N)`. Device computes `Z = y · (pA - w0 · M)` and `V = y · L`. |
| **Transcript TT** | Per RFC 9383 §3.3 length-prefixed concatenation. Each field prefixed by 8-byte little-endian length: `len(A)‖A‖len(B)‖B‖len(M)‖M‖len(N)‖N‖len(pA)‖pA‖len(pB)‖pB‖len(Z)‖Z‖len(V)‖V‖len(w0)‖w0`. Points are 65-byte uncompressed; `w0` is the 32-byte big-endian scalar. |
| **K_shared** | `K_shared = SHA-256(TT)`. (32 bytes.) |
| **Session-key chain** | Three derivations from K_shared per the original spec, with MASH-specific HKDF info strings: <br>`K_main    = HKDF-SHA256(salt="", IKM=K_shared, info="MASH PASE Session Key",  L=32)` <br>`K_confirm = HKDF-SHA256(salt="", IKM=K_shared, info="MASH PASE Confirmation", L=32)` <br>`K_encrypt = HKDF-SHA256(salt="", IKM=K_main,   info="MASH PASE Encryption",   L=32)` |
| **Confirmation MAC keys** | RFC 9383 §3.4: `KcA‖KcB = HKDF-SHA256(salt="", IKM=K_confirm, info="ConfirmationKeys", L=32)`. KcA = first 16 bytes (controller key); KcB = second 16 bytes (device key). |
| **Confirmation MACs** | `confirmA = HMAC-SHA256(KcA, pB)` (controller→device); `confirmB = HMAC-SHA256(KcB, pA)` (device→controller). Each side MACs the *peer's* public point with its *own* key. |
| **Point validation** | All received points (pA, pB) MUST be: (1) parseable as SEC1 uncompressed, (2) on the P-256 curve, (3) **not the point at infinity (identity element)**. Failure of any check is a fatal protocol error — abort with `ErrCodeAuthFailed`. |

**Wire format updates** (`commissioning-pase.md §4.2`):

- `PASERequest`: `pA` is 65 bytes (was incorrectly typed `bytes[32]`).
- `PASEResponse`: `pB` is 65 bytes (was `bytes[32]`).
- `PASEConfirm`: `confirmA` is 32 bytes (HMAC-SHA256 output).
- `PASEComplete`: `confirmB` is 32 bytes; status field unchanged.

PASE message types and CBOR layout otherwise unchanged from current Go implementation (`mash-go/pkg/commissioning/messages.go`). The `PBKDFParam` exchange that Matter uses is **not** added in v1 — the salt-derivation rule above eliminates the need for it.

**Verifier storage on device:**
The device persists `(discriminator, salt, w0, L)` after factory provisioning and SHOULD discard the setup code. Storage size: 16 + 16 + 32 + 65 = 129 bytes per device. The setup code remains on the printed label (the only place it is needed for re-pairing).

**Threat model and limits:**
- **Online attack (controller→device with wrong setup code):** Bounded by DEC-047 backoff and DEC-064 connection cap. ~10 attempts per minute steady-state; 10⁸ codes ≈ 19 years. Online brute-force is infeasible.
- **Offline attack on stolen verifier:** Attacker who extracts `(salt, w0, L)` from device NVS can brute-force the 8-digit setup code by recomputing `mhf_out → w0', L'` for each candidate. PBKDF2 100K-HMAC-SHA256 is ~10⁹ ops/sec on a high-end GPU; 10⁸ codes / 10⁹ ops/sec / 100K iterations ≈ 10⁴ seconds = ~3 hours per device. **Verifier theft is treated as full device compromise** — the device must be re-paired with a new setup code. Mitigations: NVS read protection (ESP32 flash encryption), tamper detection. Argon2id may be specified as an optional MHF in v2 for hardware that can spare 64KB+ memory; PBKDF2 is the v1 floor.
- **Active man-in-the-middle:** SPAKE2+ guarantees that an MITM cannot derive K_shared without knowing both setup codes; identity-element rejection prevents degenerate-point attacks; length-prefixed transcript prevents canonicalization attacks.

**Sub-decisions and rationale:**

- **PBKDF2 vs Argon2id.** PBKDF2-HMAC-SHA256 chosen for v1. Argon2id has stronger GPU resistance via memory-hardness, but its 64KB minimum memory is too tight on a 256KB MCU shared with Wi-Fi/TLS stacks. PBKDF2 100K is FIPS-approved, runs in ~2 seconds on ESP32, and is what Matter uses. Argon2id is a candidate for v2 via PBKDFParameter negotiation.
- **100,000 iterations as protocol constant.** Single fixed value avoids the negotiation complexity of Matter's PBKDFParameter exchange. ESP32 budget: PBKDF2 ~2s + ECC ~1s ≈ 3s, within the §5.3 constrained-device budget of 5s typical / 10s max. v2 may add per-device negotiation.
- **Salt derivation, not exchange.** `SHA-256("MASH PBKDF2 salt v1" || discriminator || setup_code)[:16]` gives a per-device salt without an extra round trip. The discriminator (12 bits) + setup code combine to give a unique salt per (discriminator, setup_code) pair. Salt secrecy is not required — only uniqueness.
- **Protocol-constant identity strings.** `b"mash-controller"` and `b"mash-device"` instead of TLS-derived material (couples crypto to TLS layer) or random nonces (would require PBKDFParameter exchange). Session entropy comes from the random ephemeral `x` and `y` scalars; identity strings only need to differ between roles.
- **Three-key chain (K_main / K_confirm / K_encrypt).** Preserved from original spec — clearer separation of concerns than RFC 9383's single K-then-split. K_encrypt derives from K_main (not K_shared) so that K_main can be exposed to other layers without revealing K_encrypt.
- **RFC 9383 KcA/KcB confirmation MAC keys.** Two distinct MAC keys (KcA for controller, KcB for device) prevent reflection attacks where the controller's confirmation MAC could be replayed as the device's. Standard mutual authentication.
- **Point validation.** Identity-element rejection is mandatory because forgetting it makes the protocol vulnerable to small-subgroup-style attacks (an attacker who can submit the identity point forces predictable Z and V).

**Cost:**

- **Wire change.** `PASERequest`/`PASEResponse` `bytes[32]` → 65 bytes. +33 bytes per message × 2 messages = +66 bytes per PASE handshake. Negligible.
- **Spec rewrite.** `commissioning-pase.md §2` (~50 lines → ~150 lines), `§4.2` (~30 lines, no length change).
- **Reference implementation rewrite.** `mash-go/pkg/commissioning/spake2plus.go` substantively rewritten in 7C; `mash-py/mash_py/commissioning/spake2plus.py` written in 7D. Conformance vectors (7B) ensure byte-identical results.
- **Verifier storage migration.** Existing `(w0, L)` verifiers regenerate on first commissioning post-7C deploy. No production deployments yet, so no migration required.
- **No PBKDFParameter exchange.** Saves one round trip per commissioning vs Matter; saves spec complexity.

**Inter-version compatibility:**
This is a wire-format breaking change. Pre-DEC-084 PASE peers cannot interoperate with post-DEC-084 peers — the verifier `(w0, L)` derivation is different, the wire size of pA/pB is different, the transcript layout is different. Because MASH is pre-1.0 with no external deployments, the change lands in lockstep across mash-go and mash-py (Phases 7C and 7D); future implementers see only the post-DEC-084 form.

**Declined alternatives:**

- **A. "Patch incrementally; no DEC."** Rejected. Leaves the no-MHF security gap as an unaddressed implicit assumption. Each subsequent implementation would have to make the same uncoordinated discovery and choices.
- **B. "Match Go silently; defer spec rewrite."** Rejected. Defeats the spec-portability validation that mash-py exists to perform. Ships SPAKE2+ with known security holes (no MHF, missing identity-element check, no transcript canonicalization) into a 20-year deployment lifecycle.
- **C. "Adopt Matter wholesale (PBKDFParameter exchange, full Matter PASE wire format)."** Rejected. PBKDFParameter exchange adds protocol complexity for a feature (per-device iteration negotiation) that v1 doesn't need; full Matter alignment would require renumbering MsgTypes and adding several Matter-specific fields, breaking more than necessary.
- **D. "Pull a vetted SPAKE2+ library (Cloudflare circl, CircuitMatter)."** Rejected for v1. The implementation independence is part of the project's value (cross-language portability requires that each language has its own implementation, and the algorithm being small enough to hand-roll is a portability check). Revisitable if hand-rolled implementations continue to surface security issues.
- **E. "Argon2id with low memory (m=8KB) instead of PBKDF2."** Rejected for v1. At low memory parameterizations, Argon2id's GPU advantage shrinks substantially while still requiring an extra dependency on every implementer. PBKDF2 is the cleaner v1 floor; Argon2id ladder is preserved as a v2 option.
- **F. "Random per-device salt stored in NVS, distributed via QR code."** Rejected. Inflates the QR code from ~30 chars to ~60 chars (16 hex bytes added). The derived salt achieves the same per-device uniqueness without QR code bloat.

**Implementations:**

- **mash-go:** updated in Phase 7C (`mash-py-tdd-plan.md`).
- **mash-py:** new implementation in Phase 7D.
- **Conformance vectors:** generated by a third independent Python script `tools/spake2plus_oracle/` in Phase 7B; both mash-go and mash-py validate against these vectors.

**Security review status:**
This DEC has been reviewed by `mash-py` Phase 7's analysis (which surfaced the original gaps) plus a structured threat model (above). External cryptographic review by a security professional is recommended before MASH 1.0 finalization, particularly for: the 100K iteration count under projected 2030-era GPU performance, the Argon2id-as-v2-option path, and the verifier-theft mitigations.

**Affected files (post-7A):**
- `docs/decision-log.md` — this entry.
- `docs/testing/behavior/commissioning-pase.md` — `§2.2` (M/N), `§2.3` (w0/w1 + MHF), `§2.4` (session keys), new `§2.5` (implementation requirements), `§4.2` (PASE wire formats — fix `bytes[32]`).
- `docs/security.md` — `§4.4` cross-references this DEC.
- `mash-py-spec-issues.md` — Issue #5 marked Resolved.
- (Phase 7B) `tools/spake2plus_oracle/`, `mash-go/testdata/spake2plus_vectors.json`.
- (Phase 7C) `mash-go/pkg/commissioning/spake2plus.go` and call sites.
- (Phase 7D) `mash-py/mash_py/commissioning/`.

**Related:** DEC-014 (SPAKE2+ adopted — high-level decision; this DEC fills in the algorithmic detail), DEC-018 (setup code reusability — strengthens the case for identity binding and per-device salt), DEC-040 (device identity via cert fingerprint — cert-derived identity considered and rejected here for layering reasons), DEC-047 (commissioning hardening — bounds online attack surface), DEC-064 (connection cap — bounds online attack surface), DEC-068 (invalid passcode validation — complementary entropy floor), `mash-py-spec-issues.md` Issue #5 (resolved by this DEC).

---

### DEC-085: Commissioning Cert-Install Uses Dedicated MsgTypes 10–13/20, Not the Renewal Envelope

**Date:** 2026-04-26
**Status:** Accepted

**Context:**
After PASE completes on the commissioning ALPN (`mash-comm/1`), the controller delivers an operational certificate signed by the Zone CA. `docs/message-types.md §3` (marked "Authoritative inventory / Status: Normative") assigns this step its own dedicated MsgTypes:

| MsgType | Name | Direction |
|---|---|---|
| 10 | `MsgCSRRequest` | Controller → Device |
| 11 | `MsgCSRResponse` | Device → Controller |
| 12 | `MsgCertInstall` | Controller → Device |
| 13 | `MsgCertInstallResponse` | Device → Controller |
| 20 | `MsgCommissioningComplete` | Device → Controller |

`docs/message-types.md §1` reserves MsgTypes 30–33 for in-session **certificate renewal** on the operational ALPN (`mash/1`) and explicitly forbids cross-plane usage: *"senders must NOT send commissioning MsgTypes on `mash/1` or vice versa; receivers MUST close the connection on cross-plane frames."*

mash-py Phase 9 (CSR & Certificate Install) surfaced that three downstream specification documents and the mash-go reference implementation had drifted from `message-types.md`:

- `docs/security.md §4.3` flow diagram and `§4.5` line 250 ("After the device sends `CertInstallAck`, **message type 33**, …") describe the renewal envelope (30–33) being reused on commissioning.
- `docs/stack-architecture.md §8.4 Phase 3` shows `CertRenewalRequest / CertRenewalCSR / CertRenewalInstall / CertRenewalAck` driving the post-PASE step.
- `docs/testing/behavior/commissioning-pase.md §4.3` shows yet a third form with **string** type identifiers (`"csr_req"`, `"cert_install"`, `"cert_ack"`) that predates the integer-MsgType regime entirely.
- `mash-go/pkg/service/device_service_commissioning.go::performCertExchange` and `pkg/service/controller_renewal.go::IssueInitialCertSync` drive `CertRenewalRequest / CSR / Install / Ack` (MsgTypes 30–33) on `mash-comm/1`. The 10/11/12/13/20 constants exist in `pkg/commissioning/messages.go` and are routed by `pkg/commissioning/protocol.go` but were never wired into the production cert-exchange path — dead constants kept alive only by `messages_test.go` and `protocol_test.go`.

The full divergence catalogue is in `mash-py-spec-issues.md` Issue #7.

**Decision:**
`message-types.md` is the canonical source. Commissioning cert install uses dedicated MsgTypes **10, 11, 12, 13, 20** on `mash-comm/1`. Renewal stays on **30, 31, 32, 33** on `mash/1`. The two namespaces are disjoint by integer range and routed by ALPN; cross-plane frames are a fatal protocol error.

**Rationale:**
The decisive argument is the namespace-separation invariant the protocol depends on for both single-port (DEC-067) and two-port (DEC-082) deployments. The dispatcher routing model — *"pick the namespace from ALPN, then decode by MsgType"* — only works if MsgType ranges are disjoint per plane. If 30–33 is dual-purpose, dispatchers must branch on connection state to disambiguate, which:

1. **Defeats single-port deployment.** A controller and device sharing one TCP port and routing by ALPN cannot detect a misdirected frame from MsgType alone if the same range is valid on both planes. The runtime check the spec requires ("MUST close the connection on cross-plane frames") becomes unimplementable.
2. **Re-introduces the stateful coupling explicit MsgTypes were meant to eliminate.** Disjoint ranges let the receiver dispatch `(ALPN, MsgType) → handler` with no other state. A dual-purpose envelope makes the handler ask "am I commissioning or renewing?" — exactly the implicit-state pattern the wire protocol was designed against.
3. **Breaks forward-compatibility.** Reserved gaps (5–9, 14–19, 21–29, 34–254) give commissioning room to grow only if 30–33 stays renewal-only. Dual-purpose 30–33 squats in commissioning's reserved space and forces future commissioning extensions to negotiate around renewal.

The two state machines also differ in ways the envelope difference makes explicit:

- **Trust context.** Commissioning cert install runs on a PASE-authenticated channel where the device has no ZoneID yet (the Zone CA is being delivered for the first time, in `CertInstall (12)`). Renewal runs on an mTLS-authenticated channel where the device already has a ZoneID and is renewing that same identity.
- **Anti-replay binding.** Renewal carries `NonceHash = SHA-256(nonce)[:16]` in `CertRenewalCSR (31)` per DEC-047 because nothing else binds the CSR to the renewal request. Commissioning doesn't need it — the SPAKE2+ shared secret already authenticates the channel — so `CSRResponse (11)` doesn't carry it.
- **Field set.** `CertInstall (12)` carries `zoneType + zonePriority` (the device must learn what kind of zone it just got). `CertRenewalInstall (32)` carries `sequence` (continuity of the existing zone's cert chain). Squashing these into one envelope loses information at type level.
- **Terminator.** Commissioning ends with `CommissioningComplete (20)`, after which the device closes the connection and the controller reconnects on `mash/1`. Renewal has no terminator; the operational connection persists.

**Alternatives considered:**

- **A. Patch `message-types.md` to allow 30–33 on `mash-comm/1`; collapse the two flows into one envelope.** Rejected. Breaks the namespace-separation invariant single-port deployment depends on (DEC-067), removes the 5-message commissioning flow's explicit terminator, and loses field-level distinctions (ZoneType, ZonePriority vs. Sequence) that exist for good reasons.
- **B. Keep status quo (mash-go's renewal-on-commissioning shortcut), update `message-types.md` to match.** Rejected. Same namespace breakage as A, plus it adopts an undocumented shortcut as canonical and abandons three downstream specs that mention 10–13/20 (`message-types.md §3`, plus the Phase 9 plan in `mash-py-tdd-plan.md`).
- **C. Implement `message-types.md` strictly; document the gap and add an adapter in mash-py decoding both envelopes.** Rejected. The wire shapes, field sets, and message counts differ structurally — an adapter would mean mash-py emits `CertRenewalRequest (30)` on `mash-comm/1`, exactly the cross-plane frame mash-go's own routing rule says receivers must reject. The fix has to be on both sides at once.

**Implementations:**

Lockstep update across:

- **mash-go.** Refactor `pkg/service/device_service_commissioning.go::performCertExchange` to drive 10/11/12/13/20. Move zone-type/zone-slot validation from "ZoneCA arrives in CertRenewalRequest" to "ZoneCA arrives in CertInstall (12)". Device emits `CertInstallResponse (13)` then `CommissioningComplete (20)` then closes the commissioning connection. Move controller-side cert-install flow out of `pkg/service/controller_renewal.go::IssueInitialCertSync` (it is not renewal) into a dedicated `pkg/service/controller_cert_install.go` (or similar). Add a regression test that asserts a frame with `MsgType ∈ {30,31,32,33}` on `mash-comm/1` is rejected, and a frame with `MsgType ∈ {10,11,12,13,20}` on `mash/1` is rejected.
- **mash-py.** Phase 9 lands spec-strict per the Phase 9 plan: `CSRRequest`/`CSRResponse`/`CertInstall`/`CertInstallResponse`/`CommissioningComplete` in `mash_py/commissioning/messages.py`, controller flow in `mash_py/commissioning/cert_exchange.py`, Zone CA logic in `mash_py/commissioning/cert_store.py`. Live interop test against the post-fix mash-go device closes the Phase 9 acceptance gate.
- **mash-test.** Existing commissioning conformance cases regenerate their wire vectors. New cases cover the cross-plane rejection invariant.
- **Spec docs.** `docs/security.md §4.3 / §4.5`, `docs/stack-architecture.md §8.4 Phase 3`, and `docs/testing/behavior/commissioning-pase.md §4.3` updated to use 10/11/12/13/20 and integer-keyed CBOR maps. ATTESTATION wire shape in `commissioning-pase.md §4.3` marked "RESERVED — wire format not yet specified" (no implementation in any reference).

**Affected files:**
- `docs/decision-log.md` — this entry.
- `docs/security.md` — `§4.3` flow diagram, `§4.5` line 250.
- `docs/stack-architecture.md` — `§8.4 Phase 3` mermaid diagram.
- `docs/testing/behavior/commissioning-pase.md` — `§4.3` wire formats.
- `mash-go/pkg/service/device_service_commissioning.go`, `pkg/service/controller_renewal.go` (split), and any `pkg/service/*commission*_test.go` that touched the envelope.
- `mash-go/testdata/cases/commissioning-*.yaml` (vector regeneration as needed).
- `mash-py/mash_py/commissioning/` — Phase 9 implementation files.
- `mash-py-spec-issues.md` — Issue #7 marked Resolved when this DEC's fix has shipped on both implementations.

**Related:** DEC-067 (single-port ALPN routing — the namespace-separation invariant this DEC defends), DEC-076 (in-session renewal — defines the renewal envelope on `mash/1`), DEC-077 (renewal window), DEC-082 (two-port deployment — also depends on namespace separation), DEC-084 (SPAKE2+ algorithm — this DEC governs the immediately-following step), `mash-py-spec-issues.md` Issue #7 (resolution being landed).

---

## Open Questions (To Be Addressed)

### OPEN-001: Feature Definitions (RESOLVED)

**Context:** Need to define specific features for our three initial use cases.

**Status: ALL COMPLETE**
- [x] Electrical feature - **DEC-027**
- [x] Measurement feature - **DEC-027**, **DEC-029** (comprehensive AC/DC)
- [x] EnergyControl/Limit feature - **DEC-026**, **DEC-027**, **DEC-028**
- [x] EndpointType enum - **DEC-029**
- [x] Status feature - **DEC-030** (per-endpoint operating state)
- [x] DeviceInfo feature - **DEC-031** (identity + complete device structure)
- [x] ChargingSession feature - **DEC-032** (EV session, demands, V2G)
- [x] Signals feature - **DEC-033** (time-slotted input: ToUT, POEN, forecasts)
- [x] Tariff feature - **DEC-033** (price structure, components, power tiers)
- [x] Plan feature - **DEC-033** (time-slotted output: device's intended behavior)

---

### OPEN-002: Certificate and Session Details

**Context:** Security model needs implementation details.

**Questions:**
- Certificate format (X.509? Custom?)
- Session key derivation
- Certificate rotation
- TLS cipher suite requirements

---

### OPEN-003: Error Handling

**Context:** Need to define error response format.

**Questions:**
- Error codes taxonomy
- Retry semantics
- Connection error vs command error

---

### OPEN-004: Version Negotiation (RESOLVED)

**Resolution:** Addressed by DEC-050 (Protocol Versioning Strategy).

**Context:** SPINE lacks version negotiation, causing interop issues.

**Questions:**
- How to negotiate protocol version?
- How to handle feature version mismatches?
- Backwards compatibility strategy

---

### OPEN-005: Discovery Details

**Context:** mDNS/DNS-SD for discovery, QR code for commissioning.

**Questions:**
- mDNS TXT record format
- Service type naming
- QR code content format (like Matter's setup payload?)

---

## Declined Proposals

*(None yet - document declined ideas here with reasons)*

---

## Research Notes

### Matter Protocol Insights

**Source:** [Matter Documentation](https://project-chip.github.io/connectedhomeip-doc/)

Key learnings:
- Matter has 4 interaction types: Read, Write, Subscribe, Invoke
- Clusters are well-defined with mandatory/optional attributes
- Server is stateful, Client is stateless
- Events are timestamped journal entries (not just current state)

### EEBUS Failure Analysis

**Source:** ship-go and spine-go analysis documents

Key learnings:
- 50+ specification ambiguities in SHIP alone
- 7,000+ potential RFE combinations create testing nightmare
- "Appropriate client" never defined - security hole
- No test specifications or reference implementations
- Double connection "most recent" rule causes race conditions

---

## Revision History

| Date | Changes |
|------|---------|
| 2025-01-24 | Initial creation with first decisions |
| 2025-01-25 | Added DEC-026: EnergyControl feature design (capability-first, forecast-optional) |
| 2025-01-25 | Added DEC-027: Feature separation (Electrical, Measurement, EnergyControl) with per-phase current limits and phase mapping |
| 2025-01-25 | Added DEC-028: Setpoints and V2H phase balancing - SetSetpoint, SetCurrentSetpoints commands, AsymmetricSupportEnum |
| 2025-01-25 | Added DEC-029: Measurement feature (comprehensive AC/DC) and EndpointType enum for multi-component devices |
| 2025-01-25 | Added DEC-030: Status feature for per-endpoint operating state (separates observation from control) |
| 2025-01-25 | Added DEC-031: DeviceInfo feature with IANA PEN-based deviceId, complete device structure in single read |
| 2025-01-25 | Added DEC-032: ChargingSession feature for EV charging with ISO 15118 support |
| 2025-01-25 | Added DEC-033: Signals + Tariff + Plan features for ToUT, POEN, CEVC, forecasts. Direction-based split: Signals (IN), Plan (OUT), Tariff (structure) |
| 2025-01-25 | Added DEC-034: Explicit ControlStateEnum and ProcessStateEnum - replaces implicit heartbeat-based state inference with explicit device-reported state. Universal across LPC, EVSE, battery, heat pump. Adds failsafe config and OptionalProcess for OHPCF-style task scheduling. |
| 2025-01-25 | Added DEC-035: Matter-style capability discovery with global attributes (featureMap, attributeList, acceptedCommandList). Defines 11 feature flags for optional capability sets. Enables self-describing devices without trial-and-error attribute reading. |
| 2025-01-25 | Added DEC-036: Charging mode and responsibility model. ChargingModeEnum for optimization strategy (PV surplus, price optimized). "CEM suggests, EVSE decides" pattern with start/stop delays. Covers OSCEV 2.0 use cases. |
| 2025-01-25 | Updated DEC-036: Electrical feature is dynamic - reflects current system capability including connected devices (e.g., EV). No separate EV constraint fields needed; Electrical updates when EV connects. Clean two-layer model: Electrical (capability) + EnergyControl (policy). |
| 2025-01-25 | Added DEC-037: Two-level capability discovery. FeatureMap is 32-bit bitmap (aligned with Matter) for high-level category checks. Detailed capabilities in feature attributes (supportsAsymmetric, supportedDirections, supportedChargingModes). Pattern: featureMap for quick check, attributes for specifics. |
| 2025-01-25 | Added DEC-038: Command parameters vs stored attributes. Duration, cause are transient command parameters (like Matter), not stored attributes (unlike EEBUS). No "remaining duration" attribute. |
| 2025-01-25 | Added DEC-039: State machine interaction rules. Process continues during FAILSAFE, scheduled processes start as planned, PAUSED behavior is device-specific (PICS). Connection loss detection max 95s, failsafe timer accuracy +/- 1%. |
| 2026-01-28 | Added DEC-044: Message correlation via MessageID, 32-bit ID rationale, 10-second request timeout. Documents implicit ACK/NACK model (response status = ack) and MessageID wraparound behavior. |
| 2026-01-28 | Added DEC-045: Transport-layer heartbeat sufficient for failsafe. No application-layer heartbeats needed. Analyzes EEBUS LPC/LPP redundant heartbeat design, Matter's approach, and why command arrival itself proves controller liveness. |
| 2026-01-29 | Added DEC-047: Commissioning security hardening. Zone-based connection model, PASE attempt backoff, nonce binding for renewal, generic error responses, handshake timeout. |
| 2026-01-29 | Added DEC-048: Commissioning window duration alignment. Default 15 minutes (was 3 hours), min 3 minutes (was 1 hour), max 3 hours (was 24 hours). Aligns with Matter 5.4.2.3.1. |
| 2026-01-30 | Added DEC-050: Protocol versioning strategy. Single protocol version (major.minor) on the wire via ALPN + specVersion. Removes per-feature clusterRevision. Feature revisions tracked in spec as version manifest per release. Resolves OPEN-004. |
| 2026-01-31 | Added DEC-051: Static attributeList immutability. attributeList is immutable for connection lifetime. Unavailable data uses null values, not structural changes. |
| 2026-01-31 | Added DEC-052: Feature-level subscription model. Use cases default to subscribe-all for features. Replaces incomplete per-attribute subscription lists.
| 2026-01-31 | Added DEC-054: Endpoint-aware PICS format. Application feature codes scoped to endpoints (`MASH.S.E01.CTRL.A01`). Transport/pairing codes unchanged. Enables per-endpoint conformance validation. |
| 2026-01-31 | Added DEC-053: Endpoint type conformance registry. Two-layer model: feature YAML defines superset, endpoint type registry defines per-type mandatory/recommended attributes. |
| 2026-01-31 | Added DEC-055: Use cases on the wire. Adds `useCases` attribute to DeviceInfo with name + major.minor version + endpointId. REST API versioning model: minor = forward-compatible, major = new contract. Bundled with specVersion releases. Enables business logic testing and explicit device contracts. |
| 2026-02-01 | Added DEC-057: Integer use case IDs and scenario bitmaps. Replaces string names with uint16 IDs on the wire. Adds uint32 scenario bitmap to UseCaseDecl. Matter-inspired atomic scenario contracts. All 11 use case YAMLs restructured with scenarios. |
| 2026-02-02 | Added DEC-058: Capability snapshots in protocol logs. Logging-layer mechanism to periodically emit device model snapshots with hybrid time/count trigger and MinMessages floor. Addresses discovery data loss on long-lived connections. |
| 2026-02-02 | Added DEC-059: Auto re-enter commissioning on last zone removal. Device calls EnterCommissioningMode() when RemoveZone reduces zone count to 0. Test harness sends RemoveZone on backward precondition transitions. |
| 2026-02-03 | Added DEC-060: TEST zone type for conformance testing. ZoneTypeTest=3, observer-only (excluded from limit/setpoint resolution), gated behind TestMode, MaxZones bumped to 3. Fixed zone type extraction from Zone CA certificate. |
| 2026-02-04 | Added DEC-063: Busy rejection response. CommissioningError(5=DEVICE_BUSY) with RetryAfter field instead of silent close on commissioning rejection. |
| 2026-02-04 | Added DEC-064: Stale connection reaper. Periodic background goroutine force-closes pre-operational connections exceeding 90s staleness threshold. |
| 2026-02-04 | Added DEC-061: Message-gated commissioning locking. Moves commissioning lock from TLS accept to first PASERequest receipt. Splits PASEServerSession.Handshake into WaitForPASERequest (5s timeout, no lock) + CompleteHandshake (85s, with lock). Idle TLS connections no longer block commissioning. |
| 2026-02-04 | Added DEC-065: Connection cooldown starts at release. Moves lastCommissioningAttempt timestamp from acceptCommissioningConnection to releaseCommissioningConnection so cooldown prevents rapid re-attempts after completion, not during in-progress connections. |
| 2026-02-11 | Added DEC-067: Single-port ALPN-based commissioning routing and stable commissioning certificate. Uses `GetConfigForClient` with ALPN `mash-comm/1` (commissioning) and `mash/1` (operational) on a single port. Commissioning cert generated once (at first startup or factory provisioning) instead of per-window. |
| 2026-02-14 | Added DEC-068: Invalid passcode validation. Devices reject 12 prohibited low-entropy setup codes (Matter 5.1.7.1). Valid range narrowed to 1-99999998. Standard test passcode changed from 12345678 to 20202021. |
| 2026-04-24 | Added DEC-081: Remove `_mashd._udp` commissioner-discovery service. "Device picks controller" flow is architecturally incoherent in a pure server-device model; no production code path used it. Delete service type from specs, Go API, test harness, and TC-MASHD conformance cases. |
| 2026-04-25 | Added DEC-082: Two-port deployment as conformant alternative to single-port ALPN routing. Surfaced while porting MASH to Python (mash-py): DEC-067's single-port pattern assumed a `GetConfigForClient`-equivalent primitive that isn't available in Python's stdlib `ssl` (or in C/Zig/some Rust TLS stacks). DEC-082 makes both deployment shapes conformant — same wire behavior, observable only as different mDNS SRV ports. Single-port (DEC-067) remains canonical for Go; two-port is canonical for languages without dynamic TLS config callbacks. |
| 2026-04-25 | Added DEC-083: CBOR tag 77 wrapper for control messages. Surfaced during mash-py Phase 1 — payload-less Responses with msgID ∈ {1,2,3} encode byte-identically to ControlMessages, and audit found no mash-go dispatcher uses session state as a tiebreaker (msgID=3 + no payload silently closes the connection). Wraps all Ping/Pong/Close in `D8 4D <map>` (tag 77 = ASCII 'M'); +2 bytes per control message; eliminates the ambiguity at the wire level. Both mash-go and mash-py updated together; resolves spec-issues Issue #2. |
| 2026-04-26 | Added DEC-084: SPAKE2+ Algorithm Specification (RFC 9383 + PBKDF2). Surfaced during mash-py Phase 7 — the `commissioning-pase.md §2` SPAKE2+ section was unimplementable as written (`K_shared` undefined, `M = G·H(label)` type-mismatched, PASE_X `bytes[32]` impossible) and the Go reference had exploitable security gaps (no MHF before HKDF — verifier theft brute-forces the 8-digit code in seconds; transcript not length-prefixed; identity element not rejected). DEC-084 adopts RFC 9383 + Matter conventions wholesale: fixed RFC 9383 P-256 M/N constants, PBKDF2-HMAC-SHA256 with 100,000 iterations, derived salt (`SHA-256("MASH PBKDF2 salt v1" \|\| discriminator \|\| setup_code)[:16]`), length-prefixed RFC 9383 §3.3 transcript, KcA/KcB confirmation MAC keys per RFC 9383 §3.4, mandatory identity-element rejection. Preserves the original spec's K_main / K_confirm / K_encrypt three-key chain (clearer than RFC 9383's split). Wire-breaking: `pA`/`pB` are now 65 bytes uncompressed (was incorrectly typed `bytes[32]`). Lands in lockstep across mash-go (Phase 7C) and mash-py (Phase 7D); resolves spec-issues Issue #5. |
| 2026-04-26 | Added DEC-085: Commissioning cert install uses dedicated MsgTypes 10–13/20, not the renewal envelope. Surfaced during mash-py Phase 9 — three downstream specs (`security.md §4.3/4.5`, `stack-architecture.md §8.4`, `commissioning-pase.md §4.3`) and `mash-go` had drifted from `message-types.md` (canonical) and were driving the renewal envelope (MsgTypes 30–33) on the commissioning ALPN. Decision: `message-types.md` wins. Cert install on `mash-comm/1` uses `CSRRequest (10)`, `CSRResponse (11)`, `CertInstall (12)`, `CertInstallResponse (13)`, `CommissioningComplete (20)`. Renewal stays on `mash/1` with 30–33. Disjoint integer ranges + ALPN routing preserve the namespace-separation invariant single-port (DEC-067) and two-port (DEC-082) deployments depend on. Lands in lockstep across mash-go (Issue #7 fix), mash-py (Phase 9 implementation), and three spec-doc patches; resolves spec-issues Issue #7. |
