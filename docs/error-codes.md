# MASH Error and Status Code Registry

> Authoritative inventory of every numeric status and error code used in MASH, grouped by the plane in which it appears.

**Status:** Normative

---

## 1. Operation Response Status Codes

Returned in CBOR key `2` of a Response message (see [message-types.md §2](message-types.md#2-data-plane-envelope-cbor)). A value of `0` indicates success; any non-zero value is an error.

| Value | Name | Meaning | Applies to |
|---|---|---|---|
| 0 | `SUCCESS` | Operation completed successfully. | All |
| 1 | `INVALID_ENDPOINT` | The target endpoint does not exist on this device. | All |
| 2 | `INVALID_FEATURE` | The target feature is not present on the specified endpoint. | All |
| 3 | `INVALID_ATTRIBUTE` | The target attribute does not exist on the feature. | Read, Write, Subscribe |
| 4 | `INVALID_COMMAND` | The invoked command does not exist. | Invoke |
| 5 | `INVALID_PARAMETER` | A parameter is out of range or wrong type. | Write, Invoke |
| 6 | `READ_ONLY` | The attribute cannot be written. | Write |
| 7 | `WRITE_ONLY` | The attribute cannot be read. | Read, Subscribe |
| 8 | `NOT_AUTHORIZED` | The requesting zone is not permitted to perform this operation. | All |
| 9 | `BUSY` | The device is temporarily unable to process this request; retry later. | All |
| 10 | `UNSUPPORTED` | The operation is not supported on this target. | All |
| 11 | `CONSTRAINT_ERROR` | A value-level constraint (range, enum, cross-attribute) is violated. | Write, Invoke |
| 12 | `TIMEOUT` | The device's internal handling of the operation timed out. | All |
| 13 | `RESOURCE_EXHAUSTED` | A device resource limit has been reached (e.g. subscription table full). | All |

Source: `mash-go/pkg/wire/status.go:6–48`; also documented in [interaction-model.md §8.1](interaction-model.md#81-status-codes).

Values 14–255 are reserved. A client receiving an unknown status value SHOULD treat it as a generic error and MAY log it for diagnostic purposes.

### 1.1 Optional error payload

A non-success Response MAY include an error payload in CBOR key `3`:

```cbor
{
  1: <messageId>,           // echoed MessageID
  2: 5,                     // status: INVALID_PARAMETER
  3: {
    1: "consumptionLimit must be >= 0"  // human-readable message (optional)
  }
}
```

The payload is advisory; implementations MUST NOT rely on its presence or content for programmatic logic. See [interaction-model.md §8.2](interaction-model.md#82-error-response).

---

## 2. Commissioning Error Codes (PASE Phase)

Returned in the `status` field of `MsgPASEComplete` (MsgType 4) or, in a few cases, carried in `MsgCSRResponse` or `MsgCertInstallResponse`.

| Value | Name | Meaning | Retriable? |
|---|---|---|---|
| 0 | `ErrCodeSuccess` | SPAKE2+ confirmation passed and certificate flow may proceed. | — |
| 1 | `ErrCodeAuthFailed` | SPAKE2+ authentication failed. Intentionally generic; see [DEC-047](decision-log.md#dec-047-commissioning-security-hardening). Supersedes the earlier `ErrCodeInvalidPublicKey` at the same value. | No |
| 2 | `ErrCodeConfirmFailed` | *Deprecated.* Old SPAKE2+ confirmation failure. All new implementations return `ErrCodeAuthFailed` instead. | No |
| 3 | `ErrCodeCSRFailed` | The device was unable to produce a valid CSR. | No |
| 4 | `ErrCodeCertInstallFailed` | The supplied operational certificate could not be installed (validation failed, key mismatch, etc.). | No |
| 5 | `ErrCodeBusy` | The device is not currently accepting commissioning; the controller SHOULD retry after the `RetryAfter` hint (see [DEC-063](decision-log.md#dec-063-busy-rejection-response)). | Yes |
| 10 | `ErrCodeZoneTypeExists` | The device already holds a zone of the requested type (GRID or LOCAL) and cannot accept another of that type (see [DEC-043](decision-log.md#dec-043-one-zone-per-zone-type-constraint)). | No |
| 255 | `ErrCodeInternalError` | A non-specific device-side failure. | No |

Source: `mash-go/pkg/commissioning/messages.go:41–55`.

Values 6–9 and 11–254 are reserved. `ErrCodeInvalidPublicKey` is retained in source as an alias at value 1 for backwards reference and is equivalent to `ErrCodeAuthFailed` on the wire.

---

## 3. `CommissioningError` Envelope (MsgType 255)

The `MsgCommissioningError` message is sent by a device to abort a commissioning attempt before (or in lieu of) a normal PASE completion. The envelope carries a commissioning error code and optional diagnostics.

| CBOR key | Field | Type | Presence |
|---|---|---|---|
| 1 | MsgType (255) | uint8 | Required |
| 2 | Error code | uint8 | Required |
| 3 | Human-readable message | text string | Optional |
| 4 | `RetryAfter` (milliseconds) | uint32 | Present when code = 5 |

The Error code field uses the same value space as [§2](#2-commissioning-error-codes-pase-phase). In current MASH deployments, only `ErrCodeBusy` (5) is known to be sent via this envelope; see [security.md §4.5](security.md#45-commissioning-connection-lifecycle) and [DEC-063](decision-log.md#dec-063-busy-rejection-response).

After sending `MsgCommissioningError`, the device MUST close the commissioning connection.

---

## 4. Integer-Space Disambiguation

Low integer values are reused across the three tables above. The decoder always knows which table applies because the protocol plane is identified by ALPN and message framing.

| Value | Meaning in Operation Response (Status) | Meaning in Commissioning Error |
|---|---|---|
| 0 | `SUCCESS` | `ErrCodeSuccess` |
| 1 | `INVALID_ENDPOINT` | `ErrCodeAuthFailed` |
| 2 | `INVALID_FEATURE` | `ErrCodeConfirmFailed` (deprecated) |
| 3 | `INVALID_ATTRIBUTE` | `ErrCodeCSRFailed` |
| 4 | `INVALID_COMMAND` | `ErrCodeCertInstallFailed` |
| 5 | `INVALID_PARAMETER` | `ErrCodeBusy` |
| 9 | `BUSY` | *(unused)* |
| 10 | `UNSUPPORTED` | `ErrCodeZoneTypeExists` |
| 255 | *(reserved)* | `ErrCodeInternalError` |

Note the deliberate similarity between operation-response `BUSY` (9) and commissioning `ErrCodeBusy` (5): both indicate temporary unavailability but live in distinct code spaces. The commissioning code additionally admits a `RetryAfter` hint in the envelope.

---

## 5. Retry Semantics

| Error | Client behavior |
|---|---|
| `SUCCESS` (0) | Proceed with response payload. |
| `BUSY` (9) on operation Response | Back off and retry the same request after a short delay (1–5 s). Do not flood. |
| `TIMEOUT` (12) | Close the connection and reconnect; re-send pending requests after mTLS handshake succeeds. See [interaction-model.md §8.3](interaction-model.md#83-request-timeout). |
| `RESOURCE_EXHAUSTED` (13) | Reduce pressure (e.g. unsubscribe from unused attributes) before retrying. |
| `ErrCodeBusy` (5, PASE) | Wait the `RetryAfter` interval, then re-attempt commissioning from `MsgPASERequest`. |
| All other codes | Non-retriable; treat as a persistent failure and surface to the user/operator. |

Implementations MAY consult an internal error classification to distinguish transient (infrastructure) from permanent (device, protocol) failures. A reference classification scheme is implemented in `mash-go/internal/testharness/runner/errors.go` (`ErrCatInfrastructure`, `ErrCatDevice`, `ErrCatProtocol`).

---

## 6. Related Documents

- [message-types.md](message-types.md) — envelopes that carry these codes.
- [interaction-model.md §8](interaction-model.md#8-error-handling) — data-plane error handling prose.
- [security.md §4](security.md#4-commissioning-pase-like) — commissioning flow context.
- [decision-log.md](decision-log.md) — DEC-047 (generic auth failure), DEC-063 (DEVICE_BUSY and RetryAfter), DEC-068 (invalid passcode handling).
