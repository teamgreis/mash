# MASH Message Type Registry

> Authoritative inventory of every message type used in MASH across the three protocol planes.

**Status:** Normative

---

## 1. Three Planes, Three Classification Mechanisms

MASH carries three distinct sub-protocols over its TCP/TLS connection. Each uses a different mechanism to classify inbound frames:

| Plane | Classification | MsgType field? |
|---|---|---|
| Data plane (Read/Write/Subscribe/Invoke, Notifications, Control) | Structural — inferred from `MessageID` and the value/type of key `2` | No |
| Commissioning plane (on ALPN `mash-comm/1`) | Explicit — `MsgType` is CBOR key `1`, `uint8` | Yes |
| Certificate renewal (on ALPN `mash/1`, operational connection) | Explicit — `MsgType` is CBOR key `1`, `uint8` | Yes |

Classification details:

- **Data plane** uses `PeekMessageType` logic: `MessageID=0` → Notification; Control messages present three specific `ControlType` values in key `1` with no endpoint/feature fields; Requests present `Operation` values 1–4 in key `2`; Responses carry a `Status` in key `2`. See [interaction-model.md §3](interaction-model.md#3-message-format) and `mash-go/pkg/wire/codec.go` (`PeekMessageType`).
- **Commissioning** uses a compact flat CBOR structure where key `1` is a `uint8` MsgType. Values 1–4 are SPAKE2+, 10–13 are CSR/certificate installation, 20 is Complete, 255 is Error. Defined in `mash-go/pkg/commissioning/messages.go:8–37`.
- **Renewal** uses the same flat structure as commissioning but a disjoint MsgType range (30–33). Defined in `mash-go/pkg/commissioning/renewal_messages.go:41–50` and documented at [stack-architecture.md §11.1](stack-architecture.md#111-in-session-renewal-flow).

The three MsgType namespaces do not collide because each plane is identified by its connection context (commissioning ALPN, operational ALPN + in-session renewal framing).

---

## 2. Data Plane Envelope (CBOR)

Every data-plane frame is a CBOR map with integer keys. Presence and meaning of each key depends on message category.

| Key | Request | Response | Notification | Control |
|---|---|---|---|---|
| 1 | `MessageID` (uint32, non-zero) | `MessageID` (uint32, echoed) | `0` (fixed marker) | `ControlType` (uint8, 1–3) |
| 2 | `Operation` (uint8, 1–4) | `Status` (uint8, 0–13) | `SubscriptionID` (uint32) | optional `Sequence` (uint32) |
| 3 | `EndpointID` (uint8) | response payload (map, optional) | `EndpointID` (uint8) | absent |
| 4 | `FeatureID` (uint8) | absent | `FeatureID` (uint8) | absent |
| 5 | request payload (varies by Operation) | absent | changes map | absent |

Source of truth: `mash-go/pkg/wire/message.go` (Request, Response, Notification, ControlMessage types) and `mash-go/pkg/wire/codec.go` (encoding/decoding, `PeekMessageType`).

### 2.1 Operation values (inside Request)

| Value | Name | Semantics |
|---|---|---|
| 1 | Read | Retrieve current attribute values. Full spec: [interaction-model.md §4](interaction-model.md#4-read-operation). |
| 2 | Write | Replace an attribute value (full replacement). Full spec: [interaction-model.md §5](interaction-model.md#5-write-operation). |
| 3 | Subscribe | Register for change notifications. Full spec: [interaction-model.md §6](interaction-model.md#6-subscribe-operation). |
| 4 | Invoke | Execute a command with parameters. Full spec: [interaction-model.md §7](interaction-model.md#7-invoke-operation). |

Source: `mash-go/pkg/wire/operation.go:7–23`.

### 2.2 Status values (inside Response)

See [error-codes.md §1](error-codes.md#1-operation-response-status-codes).

### 2.3 Control message types

| Value | Name | Semantics |
|---|---|---|
| 1 | Ping | Liveness probe. Recipient SHOULD reply with Pong carrying the same Sequence. |
| 2 | Pong | Reply to Ping. |
| 3 | Close | Graceful connection close. After sending Close, the sender SHOULD stop sending new requests. |

Source: `mash-go/pkg/wire/message.go:324–333`. Keep-alive policy (30 s ping interval) is specified in [transport.md](transport.md).

---

## 3. Commissioning Plane MsgTypes (ALPN `mash-comm/1`)

Used during the initial pairing of a device with a zone. Transport is a self-signed TLS session with no client authentication, routed by ALPN to the commissioning handler. See [security.md §4](security.md#4-commissioning-pase-like).

| MsgType | Hex | Name | Direction | Purpose |
|---|---|---|---|---|
| 1 | 0x01 | `MsgPASERequest` | Controller → Device | Begin SPAKE2+ exchange |
| 2 | 0x02 | `MsgPASEResponse` | Device → Controller | Device's public value + verifier |
| 3 | 0x03 | `MsgPASEConfirm` | Controller → Device | Controller confirmation |
| 4 | 0x04 | `MsgPASEComplete` | Device → Controller | Device confirmation + final status |
| 10 | 0x0A | `MsgCSRRequest` | Controller → Device | Request a Certificate Signing Request |
| 11 | 0x0B | `MsgCSRResponse` | Device → Controller | CSR for operational key pair |
| 12 | 0x0C | `MsgCertInstall` | Controller → Device | Deliver the signed operational certificate |
| 13 | 0x0D | `MsgCertInstallResponse` | Device → Controller | Certificate install status |
| 20 | 0x14 | `MsgCommissioningComplete` | Device → Controller | Handshake finished; device ready for operational connection |
| 255 | 0xFF | `MsgCommissioningError` | Device → Controller | Reject or abort commissioning; see [error-codes.md §3](error-codes.md#3-commissioningerror-envelope-msgtype-255) |

Source: `mash-go/pkg/commissioning/messages.go:8–37`.

Unassigned values in the ranges 5–9, 14–19, 21–29, and 34–254 are reserved; implementations MUST ignore or reject unknown commissioning MsgTypes.

---

## 4. Certificate Renewal MsgTypes (operational ALPN `mash/1`)

In-session renewal of a device's operational certificate. Runs on the live operational TLS connection and does not trigger reconnection. See [security.md §3.1](security.md#31-device-operational-certificate-renewal) and [stack-architecture.md §11](stack-architecture.md#11-certificate-renewal).

| MsgType | Hex | Name | Direction | Purpose |
|---|---|---|---|---|
| 30 | 0x1E | `CertRenewalRequest` | Controller → Device | Initiate renewal; carries a 32-byte nonce |
| 31 | 0x1F | `CertRenewalCSR` | Device → Controller | CSR + `SHA256(nonce)[0:16]` proof |
| 32 | 0x20 | `CertRenewalInstall` | Controller → Device | Signed replacement certificate + sequence number |
| 33 | 0x21 | `CertRenewalAck` | Device → Controller | Install result + active sequence |

Source: `mash-go/pkg/commissioning/renewal_messages.go:41–50` and `docs/stack-architecture.md:913–919`.

Renewal MsgTypes are decoded by the operational dispatcher, not by `PeekMessageType`. Frame framing is the same as the data plane, but the payload is a flat CBOR map with key `1` = MsgType (uint8) rather than the data-plane envelope.

---

## 5. Reserved and Unassigned Values

### 5.1 Data plane Operation values

Operation values 0 and 5–255 are reserved. A recipient that receives a Request with an unknown Operation MUST reply with `Status = UNSUPPORTED` (value 10).

### 5.2 Data plane Status values

Status values 14–255 are reserved. See [error-codes.md](error-codes.md) for defined values.

### 5.3 Commissioning MsgType values

Unassigned values (5–9, 14–19, 21–29, 34–254) are reserved. A recipient that receives a commissioning frame with an unknown MsgType MUST send `MsgCommissioningError` (255) with error code `ErrCodeInternalError` (255) and MAY close the connection.

### 5.4 Renewal MsgType values

Values outside 30–33 are not valid on the renewal sub-protocol. A recipient that receives one MUST ignore or close the connection; renewal is always initiated by the controller.

---

## 6. Bidirectionality

All four data-plane operations (Read, Write, Subscribe, Invoke) and Control messages MAY be initiated by either side of the connection. Commissioning is always initiated by the controller. Renewal is always initiated by the controller. See [interaction-model.md §11](interaction-model.md#11-bidirectional-communication).

---

## 7. Related Documents

- [interaction-model.md](interaction-model.md) — Data plane semantics in full.
- [security.md §4](security.md#4-commissioning-pase-like) — Commissioning protocol, including the cryptographic content of each MsgType.
- [stack-architecture.md §11](stack-architecture.md#11-certificate-renewal) — Renewal cryptographic content and sequence.
- [error-codes.md](error-codes.md) — Status and error-code registries referenced by message types above.
