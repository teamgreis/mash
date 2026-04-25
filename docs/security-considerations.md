# MASH Security Considerations

> Threats considered, mitigations required, and common pitfalls. A checklist for developers building MASH devices and controllers.

**Status:** Normative summary. The binding requirements are stated in [security.md](security.md), [transport.md](transport.md), and the cited DECs in [decision-log.md](decision-log.md); this document collects them in implementer-friendly form.

---

## 1. Scope and Audience

This document is written for developers implementing a MASH device or controller. It is not a formal threat model and not a security audit. Read this once before you start, and again before you ship.

It assumes you have read [protocol-overview.md](protocol-overview.md), [security.md](security.md), and [transport.md](transport.md).

---

## 2. Threat Actors Considered

MASH's protections are designed against the following adversaries:

| Adversary | Capabilities | Primary defenses |
|---|---|---|
| **Local-network observer** | Passive sniffing on the LAN, including mDNS broadcasts and TLS handshake metadata. | TLS 1.3 mutual auth ([RFC 8446]); SPAKE2+ for commissioning; opaque mDNS payload (vendor/product/serial visible only by design). |
| **Local-network attacker** | Active injection, replay, MITM on the LAN. | TLS 1.3 mutual auth on operational sessions; SPAKE2+ resists offline dictionary attack on the setup code; certificate-renewal nonce echo prevents replay ([security.md §3.1](security.md#31-device-operational-certificate-renewal)). |
| **Brute-force commissioner** | Repeatedly attempts SPAKE2+ with guessed setup codes. | Single-attempt commissioning lock (DEC-061), 500 ms cooldown on release (DEC-065), generic auth-failure code (DEC-047), invalid-passcode rejection (DEC-068). |
| **Resource-exhaustion attacker** | Opens many TCP connections to a constrained device, never completing TLS or PASE. | Transport-level connection cap at TCP accept (DEC-062), stale-connection reaper with 90 s default timeout (DEC-064), per-phase handshake timers ([security.md §11.7](security.md#117-message-gated-commissioning-locking-dec-061)). |
| **Compromised peer in another zone** | A controller in zone B tries to read or write data in zone A. | Per-zone Zone CA: each operational connection's certificate chain is verified back to the zone the device was commissioned into ([security.md §5.1](security.md#51-session-establishment)). |
| **Casual physical access** | Reads the printed QR code, presses the commissioning button. | Out of scope for cryptographic defense — physical possession is treated as authorization for commissioning. The single-use commissioning window after `MsgCertInstallAck` ([security.md §4.5](security.md#45-commissioning-connection-lifecycle)) limits the attack to *this* commissioning attempt. |

---

## 3. Required Mitigations (Implementer Checklist)

Every conforming MASH device MUST implement the following. Items are grouped by lifecycle stage; each cites its authoritative requirement.

### 3.1 At TCP accept

- [ ] **Enforce the connection cap before TLS** — total accepted connections MUST NOT exceed `max_zones + 1`. Reject in `Accept()` itself, not after the TLS handshake. (DEC-062)
- [ ] **Run a stale-connection reaper** — periodically force-close pre-operational connections older than the staleness threshold (90 s default). The reaper MUST NOT close operational connections. (DEC-064)

### 3.2 During commissioning

- [ ] **Route by ALPN** — connections with ALPN `mash-comm/1` go to the commissioning handler; `mash/1` goes to the operational handler. Never serve operational requests on the commissioning ALPN, or vice versa. (DEC-067)
- [ ] **Reject prohibited setup codes** before the SPAKE2+ exchange begins. The prohibition list is inherited from Matter §5.1.7.1. (DEC-068)
- [ ] **Hold a single-attempt commissioning lock** — only one PASE exchange in progress at a time. Reject concurrent attempts with `CommissioningError(DEVICE_BUSY)` plus a `RetryAfter` hint. (DEC-061, DEC-063)
- [ ] **Enforce per-phase timers** — first-PASE-message 5 s, total handshake 15 s. Slow-loris attacks must not block the lock indefinitely. ([security.md §11.5](security.md#115-overall-handshake-timeout))
- [ ] **Start the cooldown timer on release** (success or failure), not on accept. Otherwise an in-progress commissioning blocks legitimate retries. (DEC-065)
- [ ] **Return generic auth-failure** — every SPAKE2+-related failure (invalid public key, bad confirmation, etc.) returns `ErrCodeAuthFailed` (value 1). Do NOT expose which specific check failed. (DEC-047)
- [ ] **Close the commissioning connection after `MsgCertInstallAck`** and require the controller to reconnect on the operational ALPN within 30 s. (DEC-066, [security.md §4.5](security.md#45-commissioning-connection-lifecycle))

### 3.3 During operational sessions

- [ ] **Verify the controller's certificate chain back to this zone's Zone CA** — not just to a bag of trusted certs. A device commissioned into zone A MUST refuse a TLS handshake with a controller cert signed by zone B's CA. ([security.md §5.1](security.md#51-session-establishment))
- [ ] **Reject CBOR frames larger than 8 KB** before parsing. Bound the read buffer. (DEC-069)
- [ ] **Reject malformed CBOR or unknown integer keys** without exposing partial decode state. Map non-fatal errors to the appropriate operation-response status (see [error-codes.md](error-codes.md)).
- [ ] **Authorize per-zone, not per-connection** — a request's authorization is the zone identity established at TLS handshake, regardless of which controller cert is presented. Multi-zone resolution rules apply ([multi-zone.md](multi-zone.md)).

### 3.4 Certificate lifecycle

- [ ] **Renew before expiry, not after.** Devices MUST honor `CertRenewalRequest` (MsgType 30) inside the 7-day pre-expiry window (DEC-077). Failure to implement renewal means every device expires silently 1 year after commissioning.
- [ ] **Validate the nonce echo** — `CertRenewalCSR` (MsgType 31) MUST carry `SHA256(nonce)[0:16]` over the controller's nonce from MsgType 30. Reject mismatches. ([stack-architecture.md §11](stack-architecture.md#11-certificate-renewal))
- [ ] **Atomic certificate switchover** — if `MsgCertRenewalInstall` arrives, install the new cert and respond with `MsgCertRenewalAck` *before* discarding the old cert. A failed install MUST leave the device with its existing cert. ([security.md §3.1](security.md#31-device-operational-certificate-renewal))

---

## 4. Threats NOT Mitigated by MASH

Implementers MUST NOT assume MASH protects against the following. If your deployment is exposed to any of these, you need additional defenses outside the protocol.

| Threat | Why MASH does not address it |
|---|---|
| **Counterfeit or compromised device hardware** | Device attestation is intentionally absent. MASH targets professionally installed energy devices where supply chain trust is human-mediated. See [device-attestation-considerations.md](device-attestation-considerations.md). |
| **Compromised firmware on a commissioned device** | The device's operational private key is assumed safe. There is no remote attestation or secure-boot requirement at the protocol level. |
| **Malicious Zone Owner** | A Zone CA holder has total authority within its zone by design. There is no protocol-level recourse if the owner misuses that authority — that's a key-management and operational concern. |
| **Physical possession of a device** | Anyone holding the device can read the QR code and re-commission. Treat physical access as authorization. |
| **Cross-zone correlation by mDNS observers** | mDNS records are broadcast in the clear and reveal vendor, product, and serial. An attacker on the LAN can enumerate which devices are commissionable or operational. (DEC-078 minimizes the TXT record but does not encrypt it.) |
| **Volumetric DoS at the network layer** | Connection caps and reapers protect device resources, not network bandwidth. Upstream rate limiting is the operator's responsibility. |
| **Side-channel attacks against SPAKE2+** | Timing, power, or EM leakage during SPAKE2+ on a constrained MCU is out of scope for the protocol. Use a hardened SPAKE2+ implementation if your threat model includes side channels. |
| **Long-term confidentiality after key compromise** | TLS 1.3 provides forward secrecy for *new* sessions, but prior session traffic recorded by an attacker becomes readable if the device's operational key is later extracted from flash. |

---

## 5. Common Implementation Pitfalls

Patterns that have caused real bugs in the reference implementation or are easy to get wrong.

1. **Enforcing the connection cap *after* TLS handshake instead of at TCP accept.** Wastes the device's TLS handshake budget (CPU, RAM) on connections that will be rejected. (Fixed by DEC-062.)
2. **Cooldown timer started at accept rather than release.** A still-in-progress commissioning attempt cannot be retried even after the original attempt finishes. (DEC-065 calls this out explicitly.)
3. **Returning a specific error for each SPAKE2+ failure mode.** Exposes information that helps brute-force attackers distinguish valid prefixes. Always collapse to `ErrCodeAuthFailed`. (DEC-047)
4. **Skipping the commissioning-connection close after `MsgCertInstallAck`.** Leaves the commissioning ALPN connection live. The controller MUST reconnect on the operational ALPN; reusing the commissioning connection is non-conformant. (DEC-066)
5. **Storing the Zone CA private key on the device.** Devices hold operational certificates only; the Zone CA private key never leaves the zone owner. If your device-side code paths can read or transmit the Zone CA private key, that's a defect.
6. **Trusting the controller's claimed identity from the certificate's Subject CN alone.** The trust comes from chain validation back to *this device's* Zone CA, not from the CN. Two zones with identically named controllers are still cryptographically distinct.
7. **Not implementing certificate renewal.** Operational certificates are 1-year. A device that ignores `MsgCertRenewalRequest` will silently fail authentication once its cert expires. (DEC-077 mandates a 7-day window; controllers attempt renewal there.)
8. **Buffering arbitrarily large incoming CBOR.** A malicious peer can send a 1 GB length prefix. Reject before allocating; the cap is 8 KB (DEC-069).
9. **Reusing the same self-signed commissioning certificate across reboots without rotation.** Stable for the device lifetime is acceptable per DEC-067, but avoid leaking the private key in firmware images or logs.
10. **Logging setup codes, SPAKE2+ public values, or controller nonces.** These are short-term secrets but appear in error paths. Scrub from logs.

---

## 6. Privacy Considerations

MASH is designed for energy management within a single home, building, or grid operator's responsibility. The following data is observable to attackers in the listed positions:

| Data | Visible to | Why |
|---|---|---|
| `_mash._tcp` and `_mash-comm._tcp` mDNS records | Anyone on the LAN | DNS-SD is link-local broadcast; opting out forfeits discovery. |
| Vendor, product, serial in TXT records | Anyone on the LAN | Required for QR-code-less reconnection and zone admin matching (DEC-078). |
| TLS handshake metadata (timing, certificate sizes) | LAN observers | TLS 1.3 hides the certificate exchange under handshake encryption, but timing remains observable. |
| Connection patterns (which controller talks to which device, when) | LAN observers | Encrypted at the application layer, but TCP endpoints and packet timing are visible. |
| Setup code on QR sticker | Anyone with physical access to the device | By design — physical access ≈ pairing authorization. Treat the QR code as a single-use credential and rotate the device's setup code after first commissioning if your deployment exposes the device to public view. |

Per-zone certificates do **not** include real-world identity (no email, no name) by default. If your deployment populates `Subject` fields with PII, that PII flows over the wire on every TLS handshake — consider whether that's acceptable for your use case.

---

## 7. Reporting Security Issues

This is a reference implementation; report suspected vulnerabilities through the project's normal issue channel ([github.com/mash-protocol/mash-go](https://github.com/mash-protocol/mash-go)) or contact the maintainers privately if the issue is not yet public. Do not embed setup codes, private keys, or production traffic in public reports.

---

## 8. Related Documents

- [security.md](security.md) — Normative cryptographic and commissioning specification.
- [transport.md](transport.md) — Connection caps, stale reapers, framing limits.
- [decision-log.md](decision-log.md) — DEC-047, DEC-061, DEC-062, DEC-063, DEC-064, DEC-065, DEC-066, DEC-067, DEC-068, DEC-069, DEC-077.
- [device-attestation-considerations.md](device-attestation-considerations.md) — Why MASH does not include device attestation.
- [error-codes.md](error-codes.md) — Status codes returned for security-related failures.

[RFC 8446]: https://www.rfc-editor.org/rfc/rfc8446
