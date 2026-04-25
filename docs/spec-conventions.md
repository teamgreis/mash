# MASH Specification Conventions

> Conformance language, document set, and normative references for the MASH 1.0 specification.

**Status:** Normative
**Applies to:** MASH protocol version 1.0

---

## 1. Purpose

This document defines how the MASH specification uses conformance keywords, which documents make up the specification, and which external specifications MASH depends on. A clean-room reimplementer should read this document first.

---

## 2. Conformance Language

The key words **MUST**, **MUST NOT**, **REQUIRED**, **SHALL**, **SHALL NOT**, **SHOULD**, **SHOULD NOT**, **RECOMMENDED**, **NOT RECOMMENDED**, **MAY**, and **OPTIONAL** in the MASH specification are to be interpreted as described in BCP 14 ([RFC 2119], [RFC 8174]) when, and only when, they appear in all capitals, as shown here.

Lowercase uses of these words ("should", "must") carry no normative meaning.

---

## 3. Normative vs Informative Content

### 3.1 Normative documents

Implementations claiming MASH 1.0 conformance MUST comply with the requirements stated in these documents:

| Document | Scope |
|---|---|
| [protocol-overview.md](protocol-overview.md) | Architecture, device model, roles |
| [transport.md](transport.md) | TCP/TLS profile, framing, keep-alive |
| [security.md](security.md) | Certificate hierarchy, commissioning, zones |
| [discovery.md](discovery.md) | mDNS/DNS-SD service types, TXT records, QR codes |
| [interaction-model.md](interaction-model.md) | Read/Write/Subscribe/Invoke semantics |
| [multi-zone.md](multi-zone.md) | Zone types, priority, resolution |
| [stack-architecture.md](stack-architecture.md) | Wire protocol §6, state machines §13, certificate renewal §11 |
| [features/README.md](features/README.md) | Feature registry, FeatureMap, attribute conformance |
| [features/\*/1.0.yaml](features/README.md) | Machine-readable feature definitions (attribute IDs, types, ranges, commands) |
| [message-types.md](message-types.md) | Message type registry across data, commissioning, and renewal planes |
| [error-codes.md](error-codes.md) | Status codes, PASE error codes, commissioning error codes |
| [security-considerations.md](security-considerations.md) | Threats, required mitigations, and common implementation pitfalls |
| [glossary.md](glossary.md) | Term definitions used throughout the specification |
| [spec-conventions.md](spec-conventions.md) | This document |

Feature YAML files under `docs/features/<name>/1.0.yaml` are the authoritative source of truth for attribute IDs, CBOR keys, enum values, and command definitions. Any disagreement between a YAML file and prose is resolved in favor of the YAML.

### 3.2 Informative documents

These documents provide context, rationale, or implementation guidance but are not binding on implementations:

| Document | Scope |
|---|---|
| [decision-log.md](decision-log.md) | Design rationale (DEC-NNN). Referenced by normative docs; the *decision outcomes* are normative where cited, the discussion is not. |
| [matter-comparison.md](matter-comparison.md) | PKI comparison with Matter |
| [protocol-comparison.md](protocol-comparison.md) | Comparison with Matter 1.5 and EEBUS |
| [device-attestation-considerations.md](device-attestation-considerations.md) | Rationale for omitting device attestation |
| `docs/design/` | Design notes and implementation plans |
| `docs/research/` | Background analysis |
| [testing/README.md](testing/README.md) | Conformance test process and PICS format |
| [generated/index.md](generated/index.md) | Auto-generated reference pages from YAML |

### 3.3 Feature version identifiers

Every feature YAML file is named `<feature>/1.0.yaml`. The `1.0` corresponds to the feature's `revision` attribute (CBOR key 0xFFFD). Future feature revisions will live under `<feature>/1.1.yaml`, `<feature>/2.0.yaml`, etc. The protocol version advertised by `DeviceInfo.specVersion` and the QR code version field is `1` for this specification.

---

## 4. Normative References

The reader is expected to be familiar with these specifications. A conforming MASH implementation depends on them for interoperability.

| Reference | Title | MASH uses |
|---|---|---|
| [RFC 2119] | Key words for use in RFCs to Indicate Requirement Levels | Conformance language |
| [RFC 8174] | Ambiguity of Uppercase vs Lowercase in RFC 2119 Key Words | Conformance language clarification |
| [RFC 8949] | Concise Binary Object Representation (CBOR) | Wire format for all MASH messages |
| [RFC 8446] | The Transport Layer Security (TLS) Protocol Version 1.3 | Operational and commissioning session encryption, mutual authentication |
| [RFC 6762] | Multicast DNS | Local network discovery |
| [RFC 6763] | DNS-Based Service Discovery | Service type browsing, TXT records |
| [RFC 9382] | SPAKE2+, an Augmented Password-Authenticated Key Exchange Protocol | Commissioning (PASE) |
| [RFC 9380] | Hashing to Elliptic Curves | SPAKE2+ hash-to-curve for M/N constants |
| [RFC 5869] | HMAC-based Extract-and-Expand Key Derivation Function (HKDF) | SPAKE2+ key derivation |
| [RFC 5280] | Internet X.509 Public Key Infrastructure Certificate and CRL Profile | Zone CA, operational certificates, self-signed commissioning certificates |
| [RFC 7301] | Transport Layer Security (TLS) Application-Layer Protocol Negotiation Extension | ALPN routing between `mash-comm/1` and `mash/1` (see DEC-067) |

[RFC 2119]: https://www.rfc-editor.org/rfc/rfc2119
[RFC 8174]: https://www.rfc-editor.org/rfc/rfc8174
[RFC 8949]: https://www.rfc-editor.org/rfc/rfc8949
[RFC 8446]: https://www.rfc-editor.org/rfc/rfc8446
[RFC 6762]: https://www.rfc-editor.org/rfc/rfc6762
[RFC 6763]: https://www.rfc-editor.org/rfc/rfc6763
[RFC 9382]: https://www.rfc-editor.org/rfc/rfc9382
[RFC 9380]: https://www.rfc-editor.org/rfc/rfc9380
[RFC 5869]: https://www.rfc-editor.org/rfc/rfc5869
[RFC 5280]: https://www.rfc-editor.org/rfc/rfc5280
[RFC 7301]: https://www.rfc-editor.org/rfc/rfc7301

---

## 5. Informative References

| Reference | Title | Purpose |
|---|---|---|
| Matter Core Specification, CSA | [csa-iot.org](https://csa-iot.org/developer-resource/specifications-download-request/) | Matter comparison, setup code prohibition table ([security.md §4.1](security.md#41-setup-code-dec-068)) |
| EEBUS SHIP | EEBUS Initiative | Historical comparison; MASH replaces SHIP |
| EEBUS SPINE | EEBUS Initiative | Historical comparison; MASH replaces SPINE |
| Modbus TCP | Modbus Organization | Lower bound on protocol simplicity |

---

## 6. Typographic Conventions

- `monospace` — CBOR keys, field names, enum identifiers, wire values, service type strings (e.g. `_mash._tcp`).
- **bold** — introducing or emphasizing a defined term on first use.
- *italic* — informative remarks inside otherwise normative prose.
- Code blocks fenced with `cbor`, `mermaid`, or `go` identify the content type for renderers.
- Hexadecimal values use the `0x` prefix (e.g. `0xFFFC`). Decimal values are unprefixed.

### 6.1 Identifier casing

| Symbol kind | Casing | Example |
|---|---|---|
| Endpoint type names (wire) | `SCREAMING_SNAKE_CASE` | `DEVICE_ROOT`, `GRID_CONNECTION`, `EV_CHARGER` |
| Feature names (wire) | `PascalCase` | `Measurement`, `EnergyControl`, `ChargingSession` |
| Attribute and command names (wire) | `camelCase` | `effectiveConsumptionLimit`, `setSetpoint` |
| Enum value names (wire) | `SCREAMING_SNAKE_CASE` | `AUTONOMOUS`, `PV_SURPLUS_THRESHOLD` |

The wire forms above are what appear in YAML definitions, generated tables, and prose. Implementation language bindings are free to translate to local conventions: Go uses `PascalCase` for exported identifiers (so `EVCharger` instead of `EV_CHARGER`), Python may use `snake_case`, and so on. When a class diagram or code listing uses a translated form, the canonical wire form is the one in the YAML definitions.

---

## 7. How to Read This Specification

A first-time reader building a MASH device or controller is recommended to read in this order:

1. [protocol-overview.md](protocol-overview.md) — mental model
2. This document — conventions and references
3. [glossary.md](glossary.md) — terminology
4. [transport.md](transport.md) and [discovery.md](discovery.md) — network layer
5. [interaction-model.md](interaction-model.md) + [message-types.md](message-types.md) + [error-codes.md](error-codes.md) — wire protocol
6. [security.md](security.md) — commissioning and sessions
7. [multi-zone.md](multi-zone.md) — zone model
8. [features/README.md](features/README.md) and the individual feature specs for the features your device supports
9. [stack-architecture.md](stack-architecture.md) §13 — state machines for reference during implementation
