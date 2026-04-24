# MASH Glossary

> Definitions of terms used throughout the MASH specification. Definitions reflect MASH's own usage; terms borrowed from external specifications (Matter, TLS, CBOR, etc.) retain their external meaning unless noted.

**Status:** Normative (defines terms referenced by other normative documents)

---

## Terms

**ALPN** — Application-Layer Protocol Negotiation ([RFC 7301]). MASH uses ALPN identifiers `mash-comm/1` to route a TLS connection to the commissioning handler and `mash/1` to route it to the operational handler on the same TCP port. See [security.md §4.7](security.md#47-alpn-based-connection-routing-dec-067).

**Attribute** — A named, typed data element within a feature. Attributes are readable (default), writable, or both. They can be subscribed to for change notifications. Each attribute has a stable integer ID used as a CBOR key. See [interaction-model.md §2.4](interaction-model.md#24-command-parameters-vs-attributes).

**`attributeList`** — Global attribute (CBOR key `0xFFFB`) on every feature that enumerates which attribute IDs are actually implemented on that feature instance. Used by controllers to perform endpoint-aware conformance checks. See [DEC-072](decision-log.md#dec-072-attributelist-must-fit-in-a-single-message).

**CASE** — Certificate-Authenticated Session Establishment. Informal label for MASH's operational TLS 1.3 session with mutual certificate authentication, performed after commissioning. See [security.md §5](security.md#5-operational-sessions-case-like).

**CBOR** — Concise Binary Object Representation ([RFC 8949]). Binary self-describing serialization used for every MASH message. MASH maps always use integer keys.

**Commissioning** — The process of pairing a device with a zone: SPAKE2+ authentication with the setup code, then certificate signing and installation, resulting in the device holding an operational certificate signed by the zone's CA. See [security.md §4](security.md#4-commissioning-pase-like).

**Command** — An invokable action on a feature, identified by an integer command ID. Commands accept parameters that are not persisted as readable state. Executed via the Invoke operation. See [interaction-model.md §7](interaction-model.md#7-invoke-operation).

**ConnState** — Internal connection state tracked by a MASH implementation: Disconnected, Connecting, Connected, Closing. See [stack-architecture.md §13.5](stack-architecture.md#135-connection-state).

**Controller** — A MASH role whose responsibilities include initiating TCP/TLS connections, commissioning devices, and issuing operational certificates. Examples: EMS, smart meter gateway (SMGW), phone app. See [protocol-overview.md §3](protocol-overview.md#3-devices-controllers).

**ControlState** — Device-level state machine indicating how the device is currently being controlled: AUTONOMOUS, CONTROLLED, LIMITED, FAILSAFE, OVERRIDE. Orthogonal to ProcessState. See [stack-architecture.md §13.1](stack-architecture.md#131-controlstate).

**Delta notification** — A change notification sent on an active subscription that carries only the attributes whose values changed since the last report. An absent key means unchanged; an explicit `null` means the attribute was cleared. See [interaction-model.md §6.3](interaction-model.md#63-change-notification).

**Device** — A MASH role whose responsibilities include advertising itself via mDNS, accepting connections from controllers, and exposing endpoints and features. Examples: EVSE, inverter, battery, heat pump, smart meter. See [protocol-overview.md §3](protocol-overview.md#3-devices-controllers).

**Discriminator** — A value carried in the QR code and advertised in the `_mash-comm._tcp` mDNS TXT record (`D=` field) that allows controllers to filter commissionable devices before initiating SPAKE2+. See [discovery.md §2](discovery.md) and [DEC-078](decision-log.md#dec-078-commissionable-mdns-txt-schema-canonical).

**DNS-SD** — DNS-Based Service Discovery ([RFC 6763]). MASH uses DNS-SD service type queries to discover `_mash-comm._tcp`, `_mash._tcp`, and `_mashp._udp` instances.

**Endpoint** — A physical or logical subsystem within a device, identified by an integer ID. Endpoint 0 is reserved for `DEVICE_ROOT` metadata; endpoints 1+ carry functional features. Each endpoint has an EndpointType (e.g. `EV_CHARGER`, `BATTERY`). See [protocol-overview.md §4](protocol-overview.md#4-device-model).

**Feature** — A cohesive set of attributes and commands representing one concern (e.g. `Measurement`, `EnergyControl`). Features are identified by integer IDs. A feature appears on zero or more endpoints within a device. See [features/README.md](features/README.md).

**FeatureMap** — Global attribute (CBOR key `0xFFFC`) on every feature, encoding a 32-bit bitmap of capability variants present on that feature instance (e.g. `CORE | EMOB | V2X`). Used for fast capability filtering. See [DEC-074](decision-log.md#dec-074-featuremap-bit-semantics).

**HKDF** — HMAC-based Extract-and-Expand Key Derivation Function ([RFC 5869]). Used in the SPAKE2+ flow to derive the confirmation keys and session keys.

**Invoke** — The MASH operation for executing a command with parameters. Parameters are not stored as readable state. Operation value 4. See [interaction-model.md §7](interaction-model.md#7-invoke-operation).

**MessageID** — A 32-bit per-direction, per-connection identifier that a sender places in CBOR key `1` of a Request and that the responder echoes in the matching Response. A MessageID of `0` identifies a Notification. See [interaction-model.md §3.4](interaction-model.md#34-messageid-management).

**mDNS** — Multicast DNS ([RFC 6762]). MASH uses mDNS to advertise and discover devices on the local link without external infrastructure.

**MsgType** — In the commissioning and certificate-renewal sub-protocols, the uint8 value in CBOR key `1` that classifies the message (e.g. `30` = CertRenewalRequest). The data-plane protocol does not carry a MsgType field; see [message-types.md](message-types.md).

**Mutual TLS** — TLS 1.3 handshake in which both client and server present X.509 certificates and each side verifies the other's certificate chain. Required for MASH operational sessions. See [security.md §5.1](security.md#51-session-establishment).

**Notification** — An unsolicited message sent on an active subscription. Encoded with `MessageID=0` at CBOR key `1`; the subscription ID appears at key `2`. See [interaction-model.md §6.3](interaction-model.md#63-change-notification).

**Operation** — The uint8 value in CBOR key `2` of a Request message identifying the requested action: `1`=Read, `2`=Write, `3`=Subscribe, `4`=Invoke. Not to be confused with MsgType. See [interaction-model.md §3.1](interaction-model.md#31-request-message).

**Operational certificate** — An X.509 certificate ([RFC 5280]), 1-year validity, issued by a Zone CA to a device or controller. Presented during every operational TLS handshake. Renewable in-session via MsgTypes 30–33. See [security.md §2.1](security.md#21-operational-certificate).

**OperatingState** — Feature-level state machine for a device's operational mode: UNKNOWN, OFFLINE, STANDBY, STARTING, RUNNING, PAUSED, SHUTTING_DOWN, FAULT, MAINTENANCE. See [stack-architecture.md §13.4](stack-architecture.md#134-operatingstate).

**PASE** — Password-Authenticated Session Establishment. MASH's commissioning protocol, a SPAKE2+ exchange using the 8-digit setup code as the shared secret. See [security.md §4](security.md#4-commissioning-pase-like).

**Priming report** — The initial notification delivered immediately after a successful Subscribe, carrying the current values of all subscribed attributes. Subsequent notifications are deltas. See [interaction-model.md §6.5](interaction-model.md#65-subscription-behavior).

**ProcessState** — Optional feature-level state machine tracking a device's process lifecycle (e.g. a heat-pump defrost or an EV charging session): NONE, AVAILABLE, SCHEDULED, RUNNING, PAUSED, COMPLETED, ABORTED. Opt-in per feature; see [DEC-075](decision-log.md#dec-075-process-lifecycle-is-opt-in) and [stack-architecture.md §13.2](stack-architecture.md#132-processstate-orthogonal-to-controlstate).

**QR code** — Machine-readable code carrying `MASH:<version>:<discriminator>:<setupcode>:<vendorid>:<productid>`. Used for out-of-band transport of commissioning data. See [protocol-overview.md §7.3](protocol-overview.md#73-qr-code-format).

**Read** — The MASH operation for retrieving current attribute values. Operation value 1. See [interaction-model.md §4](interaction-model.md#4-read-operation).

**Setup code** — 8-digit decimal value (~27 bits of entropy) used as the SPAKE2+ shared secret during commissioning. Derived from the QR code. Certain low-entropy values are prohibited. See [security.md §4.1](security.md#41-setup-code-dec-068).

**SPAKE2+** — Asymmetric password-authenticated key exchange ([RFC 9382]) over elliptic curve P-256. Provides mutual authentication and a fresh session key from the 8-digit setup code without exposing it to passive observers.

**Subscribe** — The MASH operation for registering interest in attribute changes; responses include a priming report and subsequent delta notifications. Operation value 3. See [interaction-model.md §6](interaction-model.md#6-subscribe-operation).

**TLS 1.3** — Transport Layer Security version 1.3 ([RFC 8446]). Required for every MASH connection. MASH further restricts the cipher suite and certificate profile; see [transport.md](transport.md) and [security.md](security.md).

**Write** — The MASH operation for setting an attribute to a new value. Writes fully replace; there is no append or partial update. Operation value 2. See [interaction-model.md §5](interaction-model.md#5-write-operation).

**Zone** — A cryptographic trust boundary. Every device and controller in a zone holds an operational certificate signed by the same Zone CA. A device can participate in up to two zones simultaneously (one `GRID`, one `LOCAL`; see [DEC-043](decision-log.md#dec-043-one-zone-per-zone-type-constraint)).

**Zone Admin** — A role holding a delegated admin token from the zone owner; can commission devices on the owner's behalf by forwarding CSRs for signing. Typical instance: phone app delegated by an EMS. See [security.md §7.2](security.md#72-zone-admin).

**Zone CA** — The certificate authority key pair that signs all operational certificates in a zone. 20-year validity. Held by the zone owner. See [security.md §2.2](security.md#22-zone-ca).

**Zone Member** — A role holding an operational certificate signed by the zone's Zone CA. Devices and controllers are both members of their zone. See [security.md §7.3](security.md#73-zone-member).

**Zone Owner** — The entity that holds a Zone CA's private key and issues operational certificates. Typically an EMS or smart meter gateway. See [security.md §7.1](security.md#71-zone-owner).

**Zone type** — One of `GRID`, `LOCAL`, `TEST`, determining the zone's priority (GRID highest) and conformance. See [multi-zone.md](multi-zone.md) and [DEC-043](decision-log.md#dec-043-one-zone-per-zone-type-constraint).

---

[RFC 8949]: https://www.rfc-editor.org/rfc/rfc8949
[RFC 8446]: https://www.rfc-editor.org/rfc/rfc8446
[RFC 6762]: https://www.rfc-editor.org/rfc/rfc6762
[RFC 6763]: https://www.rfc-editor.org/rfc/rfc6763
[RFC 9382]: https://www.rfc-editor.org/rfc/rfc9382
[RFC 5280]: https://www.rfc-editor.org/rfc/rfc5280
[RFC 5869]: https://www.rfc-editor.org/rfc/rfc5869
[RFC 7301]: https://www.rfc-editor.org/rfc/rfc7301
