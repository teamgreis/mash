# Commissioning (PASE) Behavior

> Precise specification of device commissioning using SPAKE2+

**Status:** Draft
**Created:** 2025-01-25

---

## 1. Overview

Commissioning is the process by which a controller joins a device to its zone. MASH uses SPAKE2+ (Password-Authenticated Secure Password Authenticated Key Exchange) following Matter's proven approach.

**Key principles:**
- Setup code provides shared secret (from QR code or label)
- SPAKE2+ establishes encrypted session without revealing password
- Controller issues operational certificate after successful PASE
- Device can commission to multiple zones (up to 5)

---

## 2. SPAKE2+ Cryptographic Parameters

> **Normative spec:** This section defines MASH's SPAKE2+ algorithm completely. Two implementers reading only this section MUST be able to produce byte-identical intermediate values (w0, w1, pA, pB, Z, V, K_shared, K_main, K_confirm, K_encrypt, KcA, KcB, confirmA, confirmB) given identical inputs.
>
> Codified by **DEC-084**. Replaces the pre-DEC-084 §2 (which was incomplete and contradictory; see `mash-py-spec-issues.md` Issue #5 for the eight findings).

### 2.1 Algorithm Specification

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| Group | P-256 (`secp256r1`) | DEC-014; matches TLS cipher preference, widely supported |
| Hash | SHA-256 | Standard, efficient, hardware-accelerated on common MCUs |
| KDF | HKDF-SHA256 (RFC 5869) | Standard, RFC-conformant |
| MAC | HMAC-SHA256 (RFC 4231) | Standard, used for key confirmation |
| Memory-Hard KDF (MHF) | PBKDF2-HMAC-SHA256 (RFC 8018) | Defends against offline brute-force of stolen verifiers |
| MHF iteration count | **100,000** | Protocol constant (v1). ESP32 budget ~2s, fits §5.3 |

> **Why an MHF.** The setup code carries only ~27 bits of entropy (8 decimal digits = ~10⁸ values). Without a memory-hard or computation-bound key-derivation function before HKDF, a stolen device verifier can be brute-forced for the original setup code in seconds on a GPU. PBKDF2-HMAC-SHA256 at 100,000 iterations raises that to ~3 hours per device on a high-end GPU — affordable for a determined attacker but infeasible for opportunistic attacks. Verifier theft is treated as full device compromise (see §2.5). Argon2id may be specified as an optional MHF in a future protocol version for hardware that can spare the additional memory.

### 2.2 SPAKE2+ Generator Points M and N

MASH uses the **fixed RFC 9383 §4 P-256 SPAKE2+ generator points verbatim**. These are universally agreed across the SPAKE2+ ecosystem (RFC 9383, Matter, all conformant implementations); using them lets MASH share testing infrastructure and security analysis with that ecosystem.

```
M.x = 886e2f97ace46e55ba9dd7242579f2993b64e16ef3dcab95afd497333d8fa12f
M.y = 5ff355163e43ce224e0b0e65ff02ac8e5c7be09419c785e0ca547d55a12e2d20

N.x = d8bbd6c639c62937b04d997f38c3770719c629d7014d49a24b4f98baa1292b49
N.y = 07d60aa6bfade45008a636337f5168c64d9bd36034808cd564490b1e656edbe7
```

Both points are on the P-256 curve. They are used in their SEC1 uncompressed encoding (`0x04 || X || Y`, 65 bytes) wherever the algorithm includes a point in a hash input.

> **No MASH-specific derivation.** Pre-DEC-084 the spec specified `M = G * H("MASH SPAKE2+ M v1")` with "hash-to-curve per RFC 9380." That formula was type-mismatched (the multiplication requires `H` to produce a scalar, but RFC 9380 hash-to-curve produces a point) and would have produced different points than every other SPAKE2+ deployment in existence. DEC-084 standardizes on the RFC 9383 fixed constants — same as Matter, same as every other interoperable implementation.

### 2.3 Salt Derivation, MHF, and w0 / w1

**Setup code format.** 8 decimal digits, encoded as 8 ASCII bytes (e.g., `"20202021"` → `b"20202021"`, 8 bytes). See DEC-068 for invalid-passcode validation; this section assumes a valid setup code.

**Salt derivation.** The salt is per-device (different setup codes on different devices produce different verifiers) and computed deterministically from the QR-code contents — no exchange between controller and device is required:

```
salt = SHA-256(b"MASH PBKDF2 salt v1" || discriminator (2 bytes BE) || setup_code_utf8)[0:16]
```

- `discriminator` is the device's 12-bit discriminator from the QR code, encoded as a 2-byte big-endian unsigned integer (high 4 bits zero).
- `setup_code_utf8` is the 8-byte UTF-8 encoding of the 8-digit setup code.
- The output is the first 16 bytes of the SHA-256 hash.
- The salt is not secret. Its only requirement is per-(discriminator, setup_code)-pair uniqueness, which the SHA-256 prefix guarantees.

> **Why derived, not exchanged.** Matter uses a `PBKDFParameter` exchange to convey per-device salt. Deriving the salt instead avoids one round trip per commissioning, eliminates a state to negotiate, and removes a wire-format dependency. Both controller and device arrive at the same salt independently from QR-code contents both sides already have.

**Memory-Hard KDF.**

```
mhf_out = PBKDF2-HMAC-SHA256(
    password = setup_code_utf8,   // 8 bytes
    salt     = salt,              // 16 bytes (from above)
    c        = 100000,            // iterations (protocol constant)
    dkLen    = 80                 // output length
)
// mhf_out is 80 bytes
```

**w0 and w1 derivation.** The 80 bytes of MHF output are split and reduced modulo the P-256 curve order `n`:

```
w0_buf = mhf_out[0:40]              // first 40 bytes
w1_buf = mhf_out[40:80]             // last 40 bytes
w0 = (big-endian integer of w0_buf) mod n
w1 = (big-endian integer of w1_buf) mod n
```

`n` is the P-256 base-point order (`0xFFFFFFFF00000000FFFFFFFFFFFFFFFFBCE6FAADA7179E84F3B9CAC2FC632551`). Using 40-byte slices (320 bits) reduced modulo 256-bit `n` gives a near-uniform distribution of `w0` and `w1` over `[0, n)`, per RFC 9383 §3.2 / Matter §3.10.

If either `w0` or `w1` reduces to zero (probability ≈ 2⁻²⁵⁶), the device MUST reject the setup code as invalid. In practice this never happens for any valid setup code under DEC-068.

**Verifier point L.** The device computes:

```
L = w1 * G       // P-256 base-point multiplication
```

**Verifier storage.** The device persists `(discriminator, salt, w0, L)` in NVS after factory provisioning. Total: 16 + 16 + 32 + 65 = 129 bytes per device. The device SHOULD discard the original setup code from NVS after computing the verifier — the setup code remains on the printed label, the only place it is needed for re-pairing.

The controller does not persist the verifier. It re-derives `(salt, w0, w1)` from the QR-code contents at the start of each commissioning attempt.

### 2.4 SPAKE2+ Handshake and Session-Key Derivation

**Identity strings.** MASH binds the SPAKE2+ transcript to fixed protocol-constant identity strings:

```
ControllerIdentity = b"mash-controller"   // 15 bytes
DeviceIdentity     = b"mash-device"       // 11 bytes
```

These differentiate roles in the transcript (preventing a controller's MAC from being valid as a device's MAC and vice versa). Session-specific entropy is provided separately by the random ephemeral scalars `x` and `y`.

**Public values.** Each side generates a fresh random ephemeral scalar uniformly in `[1, n − 1]`:

```
Controller: x ←_R [1, n − 1]
            pA = x * G + w0 * M

Device:     y ←_R [1, n − 1]
            pB = y * G + w0 * N
```

Public values are exchanged in their SEC1 uncompressed encoding (`0x04 || X || Y`, 65 bytes).

**Point validation.** Both sides MUST validate received points before using them — see §2.5.

**Shared secrets Z and V.**

```
Controller computes:
    Y_unmasked = pB − w0 * N      // strip the device's mask
    Z = x * Y_unmasked
    V = w1 * Y_unmasked

Device computes:
    X_unmasked = pA − w0 * M      // strip the controller's mask
    Z = y * X_unmasked
    V = y * L                     // L = w1 * G, so this equals y * w1 * G
                                  // same value as the controller's V
```

**Transcript composition.** The transcript `TT` is a length-prefixed concatenation per RFC 9383 §3.3. Each field is preceded by an 8-byte little-endian unsigned-integer length:

```
TT = LEN(ControllerIdentity)  || ControllerIdentity
   || LEN(DeviceIdentity)     || DeviceIdentity
   || LEN(M_bytes)            || M_bytes
   || LEN(N_bytes)            || N_bytes
   || LEN(pA)                 || pA
   || LEN(pB)                 || pB
   || LEN(Z_bytes)            || Z_bytes
   || LEN(V_bytes)            || V_bytes
   || LEN(w0_bytes)           || w0_bytes

Where:
    LEN(s) = uint64 little-endian (8 bytes) of len(s)
    M_bytes, N_bytes, Z_bytes, V_bytes = SEC1 uncompressed (65 bytes)
    w0_bytes = 32-byte big-endian encoding of w0
```

The length prefixes prevent canonicalization attacks where one variable-length field could absorb bytes from the next.

**Shared secret K_shared.**

```
K_shared = SHA-256(TT)        // 32 bytes
```

**Session-key chain.** Three keys are derived from K_shared with MASH-specific HKDF info strings:

```
K_main    = HKDF-SHA256(salt="", IKM=K_shared, info=b"MASH PASE Session Key",  L=32)
K_confirm = HKDF-SHA256(salt="", IKM=K_shared, info=b"MASH PASE Confirmation", L=32)
K_encrypt = HKDF-SHA256(salt="", IKM=K_main,   info=b"MASH PASE Encryption",   L=32)
```

Notes:
- All HKDF calls use the combined Extract-then-Expand form per RFC 5869 §2.
- `K_encrypt` derives from `K_main`, not directly from `K_shared`. This lets `K_main` be exposed to other layers (e.g., for session identification) without revealing `K_encrypt`.
- The three info strings are domain-separation tags; using them prevents cross-protocol confusion if K_shared were ever reused.

**Confirmation-MAC keys KcA and KcB.** Per RFC 9383 §3.4:

```
KcA_KcB = HKDF-SHA256(salt="", IKM=K_confirm, info=b"ConfirmationKeys", L=32)
KcA = KcA_KcB[0:16]      // controller's MAC key
KcB = KcA_KcB[16:32]     // device's MAC key
```

**Confirmation MACs.** Each side MACs the *peer's* public point with its *own* MAC key:

```
confirmA = HMAC-SHA256(KcA, pB)      // controller → device
confirmB = HMAC-SHA256(KcB, pA)      // device → controller
```

Each MAC output is 32 bytes. The role labels in the MAC keys (KcA vs KcB) prevent reflection attacks — a controller's confirmation MAC cannot be replayed as the device's, even though both share the same K_confirm.

### 2.5 Implementation Requirements

These requirements apply to both controller and device implementations. All are normative.

**Point validation (mandatory).** Every received SEC1-encoded point (pA on the device side, pB on the controller side) MUST be validated:

1. The encoding MUST be parseable as SEC1 uncompressed (65 bytes, leading byte `0x04`). Compressed encoding is not used in MASH.
2. The decoded point MUST satisfy the P-256 curve equation `y² ≡ x³ − 3x + b (mod p)`.
3. The decoded point MUST NOT be the point at infinity (the identity element of the group).

Failure of any check is a fatal protocol error — the receiver MUST abort the handshake, send `PASEComplete` with `ErrorCode = ErrCodeAuthFailed` (1) per DEC-047, and close the TLS connection. Information about which specific check failed MUST NOT be leaked to the peer.

> **Why identity-element rejection.** SPAKE2+ as defined by RFC 9383 implicitly requires this check; omitting it allows degenerate-point attacks where an attacker who can submit the identity element forces predictable Z and V values, enabling brute-force of the setup code without verifier theft. Both pre-DEC-084 mash-go and the original spec missed this requirement.

**Constant-time MAC comparison.** Confirmation-MAC verification MUST use a constant-time byte-string comparison primitive (e.g., `hmac.compare_digest` in Python, `subtle.ConstantTimeCompare` in Go). Using `==` or `bytes.Equal` short-circuits on the first mismatching byte and leaks timing information.

**Ephemeral scalar handling.** The random ephemeral scalars `x` and `y` are session secrets:
- They MUST be generated with a cryptographically secure RNG (e.g., `crypto/rand` in Go, `secrets` in Python).
- They MUST be uniformly distributed over `[1, n − 1]`.
- They MUST be discarded immediately after the session ends (before the TLS connection closes) and MUST NOT be persisted.

**w0, w1, K_shared, K_main, K_confirm, K_encrypt, KcA, KcB handling.** These are all session secrets derived from the setup code or transcript:
- They MUST be discarded when the PASE session ends (typically after `CertInstallResponse` is sent / received).
- They MUST NOT be persisted to non-volatile storage.
- The verifier values `(salt, w0, L)` are the only SPAKE2+-derived values the device persists.

**Verifier theft as full compromise.** If a device's verifier is exfiltrated (NVS dump via JTAG, glitch attack, side channel, etc.), the attacker can brute-force the setup code in ~3 hours on a high-end GPU (per §2.1). The compromised device MUST be re-paired with a new setup code; the original setup code printed on the label is no longer secret. Devices SHOULD use NVS read protection (e.g., ESP32 flash encryption) and SHOULD support tamper detection where the deployment justifies it.

**No alternative MHF in v1.** Implementations MUST use PBKDF2-HMAC-SHA256 with exactly 100,000 iterations. Argon2id and other MHF candidates are reserved for future protocol versions; mixing MHF choices across v1 implementations would prevent verifier interoperability.

**No PBKDFParameter exchange in v1.** Implementations MUST derive salt from the QR-code contents per §2.3 (not exchange it as a separate message). Per-device iteration negotiation is reserved for future protocol versions.

---

## 3. Commissioning States

### 3.1 State Machine

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    Commissioning State Machine                           │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│   ┌──────────────┐                                                       │
│   │    IDLE      │  Device not in commissioning mode                     │
│   └──────┬───────┘                                                       │
│          │ Button press / QR display                                     │
│          ▼                                                               │
│   ┌──────────────┐                                                       │
│   │ ADVERTISING  │  mDNS with CM=1, waiting for connection               │
│   │  (120s max)  │                                                       │
│   └──────┬───────┘                                                       │
│          │ TLS connection received                                       │
│          ▼                                                               │
│   ┌──────────────┐                                                       │
│   │ PASE_WAIT_X  │  Device waiting for controller's X value              │
│   │  (30s max)   │                                                       │
│   └──────┬───────┘                                                       │
│          │ X received                                                    │
│          ▼                                                               │
│   ┌──────────────┐                                                       │
│   │ PASE_COMPUTE │  Device computing Y and verifier                      │
│   │  (10s max)   │                                                       │
│   └──────┬───────┘                                                       │
│          │ Computation complete                                          │
│          ▼                                                               │
│   ┌──────────────┐                                                       │
│   │ PASE_WAIT_V  │  Device waiting for controller's verifier             │
│   │  (30s max)   │                                                       │
│   └──────┬───────┘                                                       │
│          │ Verifier validated                                            │
│          ▼                                                               │
│   ┌──────────────┐                                                       │
│   │ PASE_VERIFIED│  SPAKE2+ complete, session encrypted                  │
│   └──────┬───────┘                                                       │
│          │ CSR request received                                          │
│          ▼                                                               │
│   ┌──────────────┐                                                       │
│   │ CSR_GENERATE │  Device generating key pair and CSR                   │
│   │  (10s max)   │                                                       │
│   └──────┬───────┘                                                       │
│          │ CSR sent                                                      │
│          ▼                                                               │
│   ┌──────────────┐                                                       │
│   │ CERT_WAIT    │  Waiting for operational certificate                  │
│   │  (30s max)   │                                                       │
│   └──────┬───────┘                                                       │
│          │ Certificate received                                          │
│          ▼                                                               │
│   ┌──────────────┐                                                       │
│   │ CERT_INSTALL │  Installing certificate to zone slot                  │
│   │  (5s max)    │                                                       │
│   └──────┬───────┘                                                       │
│          │ Installation complete                                         │
│          ▼                                                               │
│   ┌──────────────┐                                                       │
│   │  COMPLETE    │  Commissioning successful, transition to OPERATIONAL  │
│   └──────────────┘                                                       │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

### 3.2 State Definitions

| State | Description | Timeout | Exit on Timeout |
|-------|-------------|---------|-----------------|
| IDLE | Not commissioning | None | N/A |
| ADVERTISING | mDNS with CM=1 | 120s | → IDLE |
| PASE_WAIT_X | Waiting for X value | 30s | → ADVERTISING |
| PASE_COMPUTE | Computing Y | 10s | → ADVERTISING |
| PASE_WAIT_V | Waiting for verifier | 30s | → ADVERTISING |
| PASE_VERIFIED | Session established | None | N/A |
| CSR_GENERATE | Key pair generation | 10s | → ADVERTISING |
| CERT_WAIT | Waiting for certificate | 30s | → ADVERTISING |
| CERT_INSTALL | Installing certificate | 5s | → ADVERTISING |
| COMPLETE | Success | None | → OPERATIONAL |

### 3.3 Transition Rules

```
IDLE → ADVERTISING         : Physical action (button, display interaction)
IDLE → IDLE                : API commissioning request (not supported yet)

ADVERTISING → IDLE         : 120s timeout
ADVERTISING → ADVERTISING  : Connection failed, retry allowed
ADVERTISING → PASE_WAIT_X  : TLS connection established (no zone cert)

PASE_WAIT_X → ADVERTISING  : 30s timeout, verification failure
PASE_WAIT_X → PASE_COMPUTE : Valid X received

PASE_COMPUTE → ADVERTISING : Computation error
PASE_COMPUTE → PASE_WAIT_V : Y and verifier sent

PASE_WAIT_V → ADVERTISING  : 30s timeout, verification failure
PASE_WAIT_V → PASE_VERIFIED: Verifier validated

PASE_VERIFIED → CSR_GENERATE: CSR request received

CSR_GENERATE → ADVERTISING : Key generation error
CSR_GENERATE → CERT_WAIT   : CSR sent

CERT_WAIT → ADVERTISING    : 30s timeout
CERT_WAIT → CERT_INSTALL   : Certificate received

CERT_INSTALL → ADVERTISING : Installation error, storage full
CERT_INSTALL → COMPLETE    : Certificate installed

COMPLETE → OPERATIONAL     : (immediate transition)
```

---

## 4. Message Flow

### 4.1 Complete Commissioning Sequence

```
Controller                                    Device
    │                                            │
    │◄─────── mDNS: _mash._tcp (CM=1) ───────────┤  ADVERTISING
    │                                            │
    │─────── TCP Connect ───────────────────────►│
    │                                            │
    │─────── TLS Handshake (no client cert) ────►│
    │◄────── TLS Handshake (self-signed) ────────┤
    │                                            │
    │─────── PASE_X { X, context } ─────────────►│  PASE_WAIT_X
    │                                            │  PASE_COMPUTE
    │◄────── PASE_Y { Y, verifier_device } ──────┤
    │                                            │  PASE_WAIT_V
    │─────── PASE_VERIFY { verifier_ctrl } ─────►│
    │◄────── PASE_CONFIRM { status } ────────────┤  PASE_VERIFIED
    │                                            │
    │─────── CSRRequest      (MsgType 10) ──────►│  CSR_GENERATE
    │◄────── CSRResponse     (MsgType 11) ───────┤
    │                                            │  CERT_WAIT
    │─────── CertInstall     (MsgType 12) ──────►│  CERT_INSTALL
    │◄────── CertInstallResponse (MsgType 13) ───┤
    │◄────── CommissioningComplete (MsgType 20) ─┤  COMPLETE
    │                                            │
    │◄─────── Commissioning conn closed ─────────┤  → ADVERTISING (_mash._tcp)
    │                                            │
    │─── reconnect on operational ALPN ─────────►│  → OPERATIONAL
```

> ATTESTATION (controller-side device manufacturer verification) is **RESERVED** in
> MASH v1: no MsgType is allocated yet, no wire format is normative, and no reference
> implementation drives it. The pre-DEC-085 string-keyed sketches (`"attestation_req"` /
> `"attestation_rsp"`) have been removed pending a follow-up DEC.

### 4.2 PASE Messages

> **Codified by DEC-084.** Pre-DEC-084 this section described four messages with placeholder string types and incorrect 32-byte wire sizes for P-256 points (compressed P-256 is 33 bytes, uncompressed is 65; neither is 32). The wire formats below match `mash-go/pkg/commissioning/messages.go` MsgTypes 1–4 and use the correct uncompressed encoding per §2.

PASE messages are CBOR maps with integer keys, framed per `transport.md §4`. Four messages comprise the PASE handshake:

| MsgType | Name | Direction | Carries |
|---|---|---|---|
| 1 | `PASERequest` | Controller → Device | `pA` (controller's public value) |
| 2 | `PASEResponse` | Device → Controller | `pB` (device's public value) |
| 3 | `PASEConfirm` | Controller → Device | `confirmA` (controller's confirmation MAC) |
| 4 | `PASEComplete` | Device → Controller | `confirmB` (device's confirmation MAC) + status |

**PASERequest (MsgType 1, Controller → Device):**
```cbor
{
  1: 1,                  // msgType (PASERequest)
  2: <bytes[65]>,        // pA — controller's public value (SEC1 uncompressed: 0x04 || X || Y)
  3: <bytes>             // ClientIdentity (currently fixed to b"mash-controller", reserved
                         //   for future delegated-commissioning use; see DEC-019)
}
```

**PASEResponse (MsgType 2, Device → Controller):**
```cbor
{
  1: 2,                  // msgType (PASEResponse)
  2: <bytes[65]>         // pB — device's public value (SEC1 uncompressed: 0x04 || X || Y)
}
```

**PASEConfirm (MsgType 3, Controller → Device):**
```cbor
{
  1: 3,                  // msgType (PASEConfirm)
  2: <bytes[32]>         // confirmA = HMAC-SHA256(KcA, pB) — see §2.4
}
```

**PASEComplete (MsgType 4, Device → Controller):**
```cbor
{
  1: 4,                  // msgType (PASEComplete)
  2: <bytes[32]>,        // confirmB = HMAC-SHA256(KcB, pA) — see §2.4
  3: <uint8>             // errorCode (0 = success; see §6.2)
}
```

**Validation requirements:**

- The `ClientIdentity` field in `PASERequest` MUST equal `b"mash-controller"` in MASH v1. Devices MUST reject other values with `ErrCodeAuthFailed`. Reserved for future delegated-commissioning use.
- Wire sizes are exact: pA and pB MUST be exactly 65 bytes (uncompressed). Confirmation MACs MUST be exactly 32 bytes (HMAC-SHA256 output).
- Point validation per §2.5 MUST occur before any cryptographic operation involving the received point.
- On `PASEConfirm` MAC mismatch: device MUST send `PASEComplete` with `errorCode = ErrCodeAuthFailed` (1) and close the connection per DEC-047.
- On `PASEComplete` errorCode != 0: controller MUST treat the handshake as failed, surface the error to the user, and close the connection.

### 4.3 Certificate Messages

> **Codified by DEC-085.** Pre-DEC-085 this section showed string-keyed wire examples
> (`"csr_req"`, `"cert_install"`, …) that contradicted §3 of this same document and the
> canonical `message-types.md §3` registry. The wire formats below match
> `mash-go/pkg/commissioning/messages.go` MsgTypes 10–13 and 20, and use integer keys per
> the rest of the protocol.

Cert-install messages are CBOR maps with integer keys, framed per `transport.md §4`. Five
messages comprise the post-PASE cert exchange on the commissioning ALPN (`mash-comm/1`):

| MsgType | Name | Direction | Carries |
|---|---|---|---|
| 10 | `CSRRequest` | Controller → Device | `nonce` (32-byte anti-replay) |
| 11 | `CSRResponse` | Device → Controller | `csr` (PKCS#10 DER) |
| 12 | `CertInstall` | Controller → Device | `operationalCert` + `caCert` + `zoneType` + `zonePriority` |
| 13 | `CertInstallResponse` | Device → Controller | `errorCode` (0 = success) |
| 20 | `CommissioningComplete` | Device → Controller | (no payload) |

**ATTESTATION** (optional manufacturer-identity verification before cert exchange) is
**RESERVED** in MASH v1. No MsgType is allocated and no wire format is normative; a
follow-up DEC will define it if and when the use case is specified. Implementations MUST
NOT send any frame in MsgType range 5–9 on the commissioning ALPN.

**CSRRequest (MsgType 10, Controller → Device):**
```cbor
{
  1: 10,                 // msgType (CSRRequest)
  2: <bytes[32]>         // nonce — random, anti-replay
}
```

**CSRResponse (MsgType 11, Device → Controller):**
```cbor
{
  1: 11,                 // msgType (CSRResponse)
  2: <bytes>,            // csr — PKCS#10 DER-encoded CSR using a fresh P-256 key pair
  3: <uint8>             // errorCode (0 = success; see §6.2). On non-zero, csr MAY be empty.
}
```

**CertInstall (MsgType 12, Controller → Device):**
```cbor
{
  1: 12,                 // msgType (CertInstall)
  2: <bytes>,            // operationalCert — Zone CA-signed X.509 DER
  3: <bytes>,            // caCert — Zone CA certificate (X.509 DER)
  4: <uint8>,            // zoneType (1 = GRID, 2 = LOCAL, 3 = TEST per DEC-060)
  5: <uint8>             // zonePriority (1 = highest, 2 = next; controller-assigned)
}
```

**CertInstallResponse (MsgType 13, Device → Controller):**
```cbor
{
  1: 13,                 // msgType (CertInstallResponse)
  2: <uint8>             // errorCode (0 = success; see §6.2)
}
```

The device MUST validate the operational cert chain (`operationalCert` ←
`caCert` ← self-signed) and the zone-type slot rules from DEC-043 before storing.
On any validation failure it sends `CertInstallResponse` with the appropriate
`errorCode` (`ErrCodeCertInstallFailed = 4` for chain failures,
`ErrCodeZoneTypeExists = 10` for duplicate zone-type per DEC-043) and aborts;
`CommissioningComplete` is **not** sent on the failure path.

**CommissioningComplete (MsgType 20, Device → Controller):**
```cbor
{
  1: 20                  // msgType (CommissioningComplete) — empty payload
}
```

Sent only after `CertInstallResponse{errorCode = 0}`. Signals that the device has
durably installed the operational cert and is ready to accept an operational TLS
reconnection on `mash/1`. After sending, the device MUST close the commissioning
connection per `security.md §4.5`. The controller waits for both `CertInstallResponse`
and `CommissioningComplete` before initiating the operational reconnection.

**Validation requirements:**

- `nonce` in `CSRRequest` MUST be exactly 32 bytes, generated from a CSPRNG fresh per
  commissioning attempt.
- The CSR public key MUST be on the P-256 curve. Controllers MUST reject other curves with
  `errorCode = ErrCodeCSRFailed (3)` returned in a subsequent `CertInstall` failure path is
  not used; instead the controller closes the commissioning connection and the device
  observes the abort. (CSR validity is a controller-side check.)
- `operationalCert.SubjectPublicKeyInfo` MUST equal the CSR's public key (the device
  rejects a cert install whose public key it did not originate).
- `operationalCert.Issuer` MUST equal `caCert.Subject` and the chain `operationalCert` ←
  `caCert` MUST verify under standard X.509 path validation.
- `zoneType` MUST be one of the values in DEC-060; `zonePriority` MUST be 1 or 2.

### 4.4 Operational Certificate Format

Controller creates the operational certificate from the device's CSR:

**Certificate Structure:**

```
Certificate:
    Version: 3 (0x2)
    Serial Number: <random 128-bit>
    Signature Algorithm: ecdsa-with-SHA256
    Issuer: <Zone CA Subject>
    Validity:
        Not Before: <current time - 5 minutes>  (clock skew allowance)
        Not After: <current time + 365 days>
    Subject:
        CN = <device-id>                        (16 hex chars, from CSR public key)
        O = <zone-name>                         (optional, user-friendly)
        OU = MASH Device
    Subject Public Key Info:
        Algorithm: id-ecPublicKey (P-256)
        Public Key: <from CSR>
    Extensions:
        Basic Constraints: critical
            CA: FALSE
        Key Usage: critical
            Digital Signature
            Key Encipherment
        Extended Key Usage:
            TLS Web Server Authentication (1.3.6.1.5.5.7.3.1)
            TLS Web Client Authentication (1.3.6.1.5.5.7.3.2)
        Subject Key Identifier:
            <SHA-1 of public key>
        Authority Key Identifier:
            keyIdentifier: <Zone CA SKI>
```

**Device ID derivation:**
```python
def derive_device_id(csr: PKCS10) -> str:
    """Derive device ID from CSR public key."""
    public_key_der = csr.public_key.to_der()  # SubjectPublicKeyInfo DER
    hash = SHA256(public_key_der)
    return hash[0:8].hex().upper()  # 16 hex chars (64 bits)
```

**Certificate generation algorithm:**
```python
def create_operational_cert(
    csr: PKCS10,
    zone_ca_cert: X509,
    zone_ca_key: PrivateKey,
    zone_name: str
) -> X509:
    """Create operational certificate from CSR."""

    # Derive device ID from CSR public key
    device_id = derive_device_id(csr)

    # Build certificate
    cert = X509Certificate()
    cert.version = 3
    cert.serial_number = random_bytes(16)
    cert.issuer = zone_ca_cert.subject
    cert.not_before = now() - timedelta(minutes=5)  # Clock skew
    cert.not_after = now() + timedelta(days=365)

    # Subject DN
    cert.subject = X509Name()
    cert.subject.CN = device_id
    cert.subject.O = zone_name  # Optional
    cert.subject.OU = "MASH Device"

    # Copy public key from CSR
    cert.public_key = csr.public_key

    # Extensions
    cert.add_extension(BasicConstraints(ca=False), critical=True)
    cert.add_extension(KeyUsage(
        digital_signature=True,
        key_encipherment=True
    ), critical=True)
    cert.add_extension(ExtendedKeyUsage([
        OID_SERVER_AUTH,
        OID_CLIENT_AUTH
    ]))
    cert.add_extension(SubjectKeyIdentifier(cert.public_key))
    cert.add_extension(AuthorityKeyIdentifier(zone_ca_cert))

    # Sign with Zone CA
    cert.sign(zone_ca_key, algorithm=SHA256)

    return cert
```

**Validity period:**
- Default: 365 days (1 year)
- Controller MAY use shorter validity for higher security
- Device MUST accept any validity up to 10 years
- See `zone-lifecycle.md` for renewal procedures

---

## 5. Timing Requirements

### 5.1 Commissioning Window

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| Window duration | 120 seconds | User has time to scan QR |
| Window activation | Physical action | Prevents remote commissioning |
| Extension | None | Window does not extend on activity |
| Cooldown | 10 seconds | Before re-opening window |

### 5.2 Phase Timeouts

| Phase | Timeout | Cumulative Max |
|-------|---------|----------------|
| Advertising | 120s | 120s |
| PASE handshake | 30s | 150s |
| CSR generation | 10s | 160s |
| Certificate wait | 30s | 190s |
| Certificate install | 5s | 195s |

**Note:** Phases overlap with advertising. Total commissioning must complete within 120s window.

### 5.3 SPAKE2+ Computation Time

| Device Class | Expected Time | Maximum |
|--------------|---------------|---------|
| Constrained (ESP32) | 2-5 seconds | 10 seconds |
| Standard (RPi) | < 1 second | 5 seconds |
| Powerful (x86) | < 100 ms | 1 second |

### 5.4 Connection Protection Timing (DEC-047)

| Parameter | Value | PICS Item |
|-----------|-------|-----------|
| Max commissioning connections | 1 | MASH.S.COMM.MAX_COMMISSIONING_CONN |
| Connection cooldown | 500ms | MASH.S.COMM.CONN_COOLDOWN |
| Overall handshake timeout | 85s | MASH.S.COMM.HANDSHAKE_TIMEOUT |

**Note:** Max operational connections = MASH.S.ZONE.MAX (one per zone).

### 5.5 PASE Attempt Backoff (DEC-047)

| Attempts | Backoff Delay | PICS Item |
|----------|---------------|-----------|
| 1-3 | 0ms | MASH.S.COMM.BACKOFF_TIER1 |
| 4-6 | 1000ms | MASH.S.COMM.BACKOFF_TIER2 |
| 7-10 | 3000ms | MASH.S.COMM.BACKOFF_TIER3 |
| 11+ | 10000ms | MASH.S.COMM.BACKOFF_TIER4 |

**Behavior:**
- Backoff counter resets when commissioning window closes
- Backoff counter resets on successful commissioning
- Delay is applied before sending PASE response (prevents timing analysis)

---

## 6. Error Handling

### 6.1 PASE Verification Failure

**Cause:** Wrong setup code entered.

**Device behavior:**
1. Send PASE_CONFIRM with status = 1 (VERIFICATION_FAILED)
2. Close TLS connection
3. Return to ADVERTISING state
4. Accept new commissioning attempts

**Controller behavior:**
1. Receive PASE_CONFIRM with failure status
2. Report error to user: "Incorrect setup code"
3. Allow user to retry

**Retry policy:**
- No retry limit (user can keep trying)
- No backoff between attempts
- No lockout mechanism

### 6.2 Error Codes

| Code | Name | Description |
|------|------|-------------|
| 0 | SUCCESS | Operation successful |
| 1 | VERIFICATION_FAILED | SPAKE2+ verification failed |
| 2 | TIMEOUT | Operation timed out |
| 3 | ZONE_FULL | Device has 2 zones, cannot add more |
| 4 | ALREADY_COMMISSIONED | Device already commissioned to this zone |
| 5 | INVALID_CERT | Certificate validation failed |
| 6 | STORAGE_ERROR | Cannot store certificate |
| 7 | KEY_GENERATION_ERROR | Cannot generate key pair |
| 8 | INVALID_FORMAT | Message format error |
| 9 | INTERNAL_ERROR | Device internal error |
| 10 | ZONE_TYPE_EXISTS | Device already has a zone of this type |

### 6.3 Connection Loss During Commissioning

**If connection lost in any state:**
1. Cancel current commissioning attempt
2. Release any generated keys (if not installed)
3. Return to ADVERTISING state (if window still open)
4. Return to IDLE state (if window expired)

**Controller behavior:**
1. Detect connection loss
2. Report to user: "Connection lost during commissioning"
3. User can retry from start

### 6.4 Zone Capacity

A device can have at most one zone per zone type (see DEC-043):
- Max 1 GRID zone
- Max 1 LOCAL zone

**When device already has a zone of the same type:**
1. Device accepts TLS connection
2. Device sends PASE_CONFIRM with status = 10 (ZONE_TYPE_EXISTS)
3. Connection closed
4. Controller reports: "Device already has a zone of this type"

**When device already has both zone types (2 zones):**
1. Device accepts TLS connection
2. Device sends PASE_CONFIRM with status = 3 (ZONE_FULL)
3. Connection closed
4. Controller reports: "Device has maximum zones"

**To add another zone of the same type:**
1. Controller must remove existing zone first (RemoveZone command)
2. Then retry commissioning

### 6.5 Consolidated Error Codes (DEC-047)

To prevent information leakage, authentication errors are consolidated:

| Code | Name | When Used |
|------|------|-----------|
| 0 | SUCCESS | Operation successful |
| 1 | AUTH_FAILED | Any SPAKE2+ or verification failure |
| 2 | TIMEOUT | Operation timed out |
| 3 | ZONE_FULL | Device has maximum zones |
| 4 | ALREADY_COMMISSIONED | Already in this zone |
| 5 | INVALID_CERT | Certificate validation failed |
| 6 | STORAGE_ERROR | Cannot store certificate |
| 7 | KEY_GENERATION_ERROR | Cannot generate key pair |
| 10 | ZONE_TYPE_EXISTS | Zone type already present |

**Removed codes:** InvalidPublicKey (merged into AUTH_FAILED), ConfirmFailed (merged into AUTH_FAILED)

**Timing requirement:** Device MUST add random delay (100-500ms) to all error responses to prevent timing-based oracle attacks.

---

## 7. Multi-Zone Commissioning

### 7.1 Zone Slots

Device maintains up to 2 zone slots (one per zone type):

```
zoneSlots[2] = {
  [GRID]:  { zoneCA: <cert>, opCert: <cert>, priority: 1 } or null,
  [LOCAL]: { zoneCA: <cert>, opCert: <cert>, priority: 2 } or null
}
```

Only one zone per type is allowed. See DEC-043.

### 7.2 Concurrent Commissioning

**Rule:** Only one commissioning session at a time.

**Behavior:**
- If device is in any commissioning state (PASE_*, CSR_*, CERT_*):
- New commissioning connection is rejected
- Existing session continues
- New controller receives: "Device busy commissioning"

**Queue behavior:** No queue. Rejected controllers must retry.

### 7.3 Existing Zones During Commissioning

**Rule:** Existing zone connections remain OPERATIONAL.

**Behavior:**
- Controller A (LOCAL) is connected and OPERATIONAL
- Controller B (GRID) starts commissioning
- Controller A continues normal operation
- Controller B completes commissioning
- Controller B establishes OPERATIONAL connection
- Both controllers now connected

### 7.4 Same Zone Re-Commissioning

**Scenario:** Controller tries to commission device that's already in its zone.

**Behavior:**
1. Device checks if certificate issuer matches existing zone CA
2. If match: Return ALREADY_COMMISSIONED error
3. Controller should use existing operational connection

**To re-commission (e.g., after key compromise):**
1. Controller sends RemoveZone command (existing connection)
2. Device removes zone slot
3. Controller initiates new commissioning

---

## 8. Delegated Commissioning (Admin Flow)

### 8.1 Admin Authorization

Apps act as admins, not zone owners. Admin flow:

```
User              App              EMS (Zone Owner)        Device
 │                 │                     │                    │
 │─ "Add admin" ──►│                     │                    │
 │                 │── Request admin ───►│                    │
 │                 │◄── Temp QR (5min) ──┤                    │
 │◄── Display QR ──┤                     │                    │
 │── Scan QR ─────►│── SPAKE2+ ─────────►│                    │
 │                 │◄── Admin token ─────┤                    │
```

### 8.2 Admin Token Format

```cbor
{
  1: <bytes[32]>,        // token_id
  2: <uint64>,           // issued_at (Unix timestamp)
  3: <uint64>,           // expires_at (Unix timestamp)
  4: <bytes[]>,          // zone_ca_fingerprint (SHA-256)
  5: <uint8>,            // permissions (bitmap)
  6: <bytes[64]>         // signature (ECDSA over fields 1-5)
}
```

**Permissions bitmap:**

| Bit | Permission |
|-----|------------|
| 0 | Can commission devices |
| 1 | Can remove devices |
| 2 | Can view device list |
| 3-7 | Reserved |

### 8.3 Admin Commissioning Flow

```
App (Admin)                              EMS (Owner)              Device
    │                                        │                       │
    │───── PASE with setup code ────────────────────────────────────►│
    │◄───────────────────────────────────────────────────── CSR ─────┤
    │                                        │                       │
    │─── Forward CSR + admin token ─────────►│                       │
    │◄── Signed operational cert ────────────┤                       │
    │                                        │                       │
    │─── CERT_INSTALL ──────────────────────────────────────────────►│
    │◄──────────────────────────────────────────────────── ACK ──────┤
```

### 8.4 Admin Token Validation

Device validates admin token during CERT_INSTALL:

1. Verify signature using zone CA public key
2. Check expires_at > current time
3. Check permissions include "can commission"
4. Check zone_ca_fingerprint matches received zone CA cert

**If validation fails:**
- Return CERT_ACK with status = 5 (INVALID_CERT)
- Error message: "Invalid admin token"

---

## 9. Certificate Profile

### 9.1 Operational Certificate

| Field | Value |
|-------|-------|
| Version | v3 |
| Serial | Random 128-bit |
| Issuer | Zone CA DN |
| Subject | Device ID + Zone ID |
| Validity | 1 year |
| Key Usage | Digital Signature, Key Encipherment |
| Extended Key Usage | Client Auth, Server Auth |
| Subject Alt Name | URI: mash://device/{device_id} |

### 9.2 Zone CA Certificate

| Field | Value |
|-------|-------|
| Version | v3 |
| Serial | Random 128-bit |
| Issuer | Self-signed (Zone CA) |
| Subject | Zone name + Zone ID |
| Validity | 10 years |
| Key Usage | Certificate Sign, CRL Sign |
| Basic Constraints | CA:TRUE, pathlen:1 |

### 9.3 Device Attestation Certificate (Optional)

| Field | Value |
|-------|-------|
| Version | v3 |
| Issuer | Manufacturer CA |
| Subject | Vendor ID + Product ID + Device ID |
| Validity | 20 years |
| Key Usage | Digital Signature |

---

## 10. PICS Items

```
# SPAKE2+ parameters
MASH.S.PASE.GROUP=P256               # SPAKE2+ group
MASH.S.PASE.HASH=SHA256              # Hash algorithm
MASH.S.PASE.KDF=HKDF_SHA256          # Key derivation

# Timing
MASH.S.PASE.WINDOW_DURATION=120      # Commissioning window (seconds)
MASH.S.PASE.PASE_TIMEOUT=30          # PASE handshake timeout
MASH.S.PASE.CSR_TIMEOUT=10           # CSR generation timeout
MASH.S.PASE.CERT_TIMEOUT=30          # Certificate wait timeout

# Capacity
MASH.S.PASE.MAX_ZONES=5              # Maximum zone slots
MASH.S.PASE.MAX_CONCURRENT=1         # Concurrent commissioning sessions

# Features
MASH.S.PASE.ATTESTATION=1            # Device attestation supported
MASH.S.PASE.ADMIN_DELEGATION=1       # Admin delegation supported

# Error behavior
MASH.S.PASE.RETRY_LIMIT=0            # 0 = unlimited retries
MASH.S.PASE.RETRY_BACKOFF=0          # No backoff between retries
```

---

## 11. Test Cases

### TC-PASE-*: SPAKE2+ Protocol

| ID | Description | Steps | Expected |
|----|-------------|-------|----------|
| TC-PASE-1 | Successful PASE | Correct setup code | PASE_VERIFIED state |
| TC-PASE-2 | Wrong setup code | Incorrect setup code | VERIFICATION_FAILED |
| TC-PASE-3 | PASE timeout | No X sent | Return to ADVERTISING after 30s |
| TC-PASE-4 | Invalid X value | Malformed X | Close connection |
| TC-PASE-5 | Replay X | Same X twice | Fresh Y each time |

### TC-COMM-*: Commissioning Flow

| ID | Description | Steps | Expected |
|----|-------------|-------|----------|
| TC-COMM-1 | Complete commissioning | Full flow | OPERATIONAL state |
| TC-COMM-2 | Window timeout | Wait 120s | Return to IDLE |
| TC-COMM-3 | CSR timeout | No CSR_REQ | Return to ADVERTISING |
| TC-COMM-4 | Invalid certificate | Malformed cert | INVALID_CERT error |
| TC-COMM-5 | Storage full | Max certs stored | STORAGE_ERROR |

### TC-ZONE-*: Multi-Zone

| ID | Description | Steps | Expected |
|----|-------------|-------|----------|
| TC-ZONE-1 | Second zone | Commission LOCAL after GRID | Both zones active |
| TC-ZONE-2 | Same type | Commission second GRID | ZONE_TYPE_EXISTS error |
| TC-ZONE-3 | Third zone | Commission when both slots full | ZONE_FULL error |
| TC-ZONE-4 | Same zone | Re-commission | ALREADY_COMMISSIONED |
| TC-ZONE-5 | Concurrent | Two controllers | Second rejected |

### TC-ADMIN-*: Delegated Commissioning

| ID | Description | Steps | Expected |
|----|-------------|-------|----------|
| TC-ADMIN-1 | Valid admin token | Admin commissions | Success |
| TC-ADMIN-2 | Expired token | Expired admin token | INVALID_CERT error |
| TC-ADMIN-3 | Invalid signature | Tampered token | INVALID_CERT error |
| TC-ADMIN-4 | Wrong permissions | No commission perm | INVALID_CERT error |

### TC-CERT-*: Certificate Handling

| ID | Description | Steps | Expected |
|----|-------------|-------|----------|
| TC-CERT-1 | Valid chain | Full cert chain | Success |
| TC-CERT-2 | Self-signed device | No attestation | Success (optional) |
| TC-CERT-3 | Invalid chain | Broken chain | INVALID_CERT error |
| TC-CERT-4 | Expired cert | Expired op cert | INVALID_CERT error |

---

## 12. Security Considerations

### 12.1 Setup Code Protection

- **Display security:** QR should only be shown during commissioning window
- **Physical security:** Label should not be visible during normal operation
- **Entropy:** 27 bits sufficient for 120-second window (online attack only)
- **Brute force:** ~100 million combinations, no lockout

### 12.2 SPAKE2+ Security

- **Offline attack resistance:** w0/w1 derivation prevents offline dictionary attack
- **Forward secrecy:** Ephemeral X/Y values, no persistent session keys
- **MITM resistance:** Verifier exchange authenticates both parties
- **Replay resistance:** Random X/Y per session

### 12.3 Certificate Security

- **Zone isolation:** Each zone has independent CA
- **Rotation:** 1-year validity with 7-day renewal window (DEC-077)
- **Revocation:** Immediate via RemoveZone command
- **Path validation:** Maximum path length = 2 (Zone CA → Op Cert)

---

## 13. Security Hardening (DEC-047)

### 13.1 Connection Flood Protection

**Requirement:** Device MUST limit concurrent commissioning connections.

**Test Procedure:**
1. Device enters commissioning mode
2. Tester opens a commissioning connection
3. Tester attempts a second commissioning connection
4. Verify: Second connection is rejected at TLS level

**Expected Result:** Connection rejected (no PASE exchange)

### 13.2 PASE Backoff Enforcement

**Requirement:** Device MUST implement exponential backoff on failed attempts.

**Test Procedure:**
1. Device enters commissioning mode
2. Tester sends 4 PASE requests with wrong setup code
3. Measure time between request and error response for attempt 4
4. Verify: Delay >= BACKOFF_TIER2 (1000ms)

**Expected Result:** Response delayed by at least 1000ms

### 13.3 Nonce Binding Validation

**Requirement:** Certificate renewal MUST validate nonce binding.

**Test Procedure:**
1. Controller sends CertRenewalRequest with nonce A
2. Device responds with CertRenewalCSR including NonceHash(A)
3. Controller sends CertRenewalInstall (simulating different session)
4. Controller sends another CertRenewalRequest with nonce B
5. Controller sends CertRenewalInstall with cert from step 2
6. Verify: Device rejects with RenewalStatusInvalidNonce

**Expected Result:** Renewal rejected due to nonce mismatch

### 13.4 Error Response Timing

**Requirement:** Error responses MUST have consistent timing.

**Test Procedure:**
1. Measure response time for: invalid public key (100 attempts)
2. Measure response time for: wrong password (100 attempts)
3. Calculate mean and standard deviation for each
4. Verify: Mean difference < 100ms, distributions overlap

**Expected Result:** Cannot distinguish error types by timing
