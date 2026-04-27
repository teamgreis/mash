# MASH Spec-Portability Issue Tracker

This document tracks **spec issues** surfaced while implementing MASH in Python (mash-py). The goal is multi-language portability: to ensure the spec doesn't quietly assume a Go-shaped infrastructure.

Each entry captures:
1. The implicit assumption found (often phrased "the spec assumes the implementer has X").
2. Which non-Go languages are affected and how badly.
3. Spec options considered.
4. Disposition (open / decided / deferred).

The mash-py implementation is the first probe. A future Rust or C port will surface different issues (memory management, static typing). Python catches API-shape and infrastructure-API issues; it does *not* catch memory-management or strict-typing issues.

## Process

When a phase of mash-py hits friction, before working around it ask:

> "Is this a Python implementation problem, or a spec assumption that hurts every non-Go implementer?"

If the latter:
1. Add an entry here.
2. Decide spec disposition (revise, annex, or accept-with-rationale).
3. If revising, file a DEC-NNN.
4. Update the plan to reflect the spec resolution.
5. Then proceed with the implementation.

This way, each phase produces durable spec value, not just Python code.

---

## Issues

### Issue #1 — Single-port ALPN routing assumes `GetConfigForClient`-equivalent primitive

**Status:** ✅ Resolved
**Resolution:** DEC-082 (2026-04-25)
**Surfaced during:** mash-py-tdd-plan.md drafting, phase 4 design

**Implicit assumption.**
DEC-067 specified single-port ALPN-based routing for commissioning (`mash-comm/1`) and operational (`mash/1`) connections. The implementation pattern uses Go's `tls.Config.GetConfigForClient`, a callback that:
1. Receives the parsed `ClientHelloInfo` (including ALPN list).
2. Fires *before* the cert and `ClientAuth` decisions are locked.
3. Can return an entirely different `tls.Config` per request.

This primitive is Go-specific. The spec didn't make the assumption visible.

**Languages affected.**

| Language / TLS lib | Has `GetConfigForClient` equivalent? | Workaround cost without spec change |
|---|---|---|
| Go (`crypto/tls`) | ✅ Yes | — |
| Python (stdlib `ssl`) | ❌ No (`sni_callback` is SNI-only; `set_alpn_protocols` is choice-only) | ~400 LOC ClientHello parser + BIO handshake pump |
| C / mbedTLS | ❌ No | ~600 LOC parser + buffered TLS state |
| Zig (`std.crypto.tls`) | ❌ Not yet | Similar to C |
| Rust / rustls | ⚠️ Partial (callbacks exist but less flexible) | ~300 LOC adapter |
| PyOpenSSL | ✅ `set_tlsext_servername_callback` fires early enough | (Heavyweight C-extension dep; CircuitPython unsupported) |

**Spec options considered.**

A. **Annex with portability recipe.** Document the ClientHello-peek + BIO pattern in a transport implementation note. Single-port stays mandatory.
B. **Allow two-port deployment.** Make two-port a first-class conformant deployment alongside single-port.
C. **Drop TLS during commissioning (Matter style).** PASE-derived AES-CCM channel; eliminates dual-TLS-context entirely. Largest spec change.

**Decision.** Option B. Codified as **DEC-082**: two-port deployment is conformant alongside single-port. mDNS SRV records (already canonical per DEC-078) carry the actual port per service; controllers and tests dial the SRV-advertised port without caring about deployment shape.

**Why B over A and C:**
- A leaves a Go-shaped assumption embedded in the canonical pattern, with an annex saying "here's how to work around it." That's worse than just admitting two patterns exist.
- C trades a TLS-config-callback burden for a custom-AEAD-framing burden — worse for portability across languages, since standard TLS libraries are mature in every language and bespoke AEAD framing is not.
- B preserves DEC-067 unchanged for languages where it works well (Go), and unblocks every other language with no wire-protocol change.

**Affected files (updated 2026-04-25):**
- `docs/decision-log.md` — added DEC-082; cross-referenced DEC-067; updated changelog.
- `docs/transport.md §5.3` — both deployment patterns documented.
- `docs/discovery.md §2.1` — explicit "SRV port is authoritative" note + per-pattern port behavior.
- `docs/security.md §4.3, §4.7` — flow diagram and TLS-separation section both reflect two patterns.
- `mash-py-tdd-plan.md` — Decision A flipped to two-port; Phase 4 simplified (was 4a + 4b, now Phase 4 only).

**mash-py implementation:** two-port. Operational on 8443, commissioning on 8444 (default). See `mash-py/mash_py/service/listeners.py` (Phase 4).

**mash-go implementation:** single-port (DEC-067 unchanged). Reference behavior preserved.

---

### Issue #2 — `peek_message_type` cannot distinguish small-msgID error responses from control messages

**Status:** ✅ Resolved
**Resolution:** DEC-083 (2026-04-25) — wrap every Control message in CBOR tag 77; presence of `D8 4D` prefix is the unambiguous CONTROL marker. Both mash-go and mash-py updated together.
**Surfaced during:** mash-py Phase 1 — porting `peek.py` from `pkg/wire/codec.go:181-272`

**Implicit assumption.**
The wire-format peek classifier in mash-go uses the byte structure alone. A response of the form `{1: msgID, 2: status}` with no payload is byte-identical to a control message of the form `{1: type, 2: sequence}` whenever:

- `msgID` is in {1, 2, 3} (the same range as `ControlMessageType`), and
- `status` is any uint32 value, and
- there is no payload at key 3.

`PeekMessageType` resolves the tie in favor of CONTROL (codec.go:230-238 — control check runs before status>4 → Response). This means a perfectly legal MASH response carrying e.g. `{MessageID:1, Status:INVALID_PARAMETER}` peeks as a control message. mash-py's port reproduces the same behavior, since divergence would split interop.

In practice, the dispatcher disambiguates at the session layer: the receiver knows it has an in-flight request with `msgID=1` and routes the frame to the response decoder regardless of peek's verdict. Peek is a fast-path classification, not the authoritative one. But the spec doesn't say that — it doesn't even mention peek. A non-Go implementer following the wire format alone has no way to know.

**Languages affected.**
All — this is a spec-text issue, not a language-specific API issue.

**Spec options considered.**

A. **Document peek as an optimization, not authoritative.** Spec adds: "Receivers MUST use the session state (in-flight messageIds, expected control sequences) for authoritative classification. The wire format permits structural ambiguity for short error responses; peek is a hint only."
B. **Reorder checks: status>4 → Response wins over control.** Would break the existing Go test corpus and require behavior change. Wire-format-compatible (no encoding change), but receivers running the old algorithm would misclassify.
C. **Forbid messageIds in {1, 2, 3} on the wire.** Disambiguates by construction, but wastes 3 valid uint32 values and breaks every existing test.
D. **Add a tag byte to control messages** (e.g. CBOR tag 65535 or a prepended sentinel). Wire-incompatible — major version bump.

**Decision.** **Option D (chosen)** — wrap control messages in CBOR tag 77 (`D8 4D`). Codified as **DEC-083**. Audit of mash-go's five peek consumers (2026-04-25) found that none consult session state as a tiebreaker — every dispatcher routes purely on peek's verdict, including transport-level handlers that close the connection on `Type:Close` (msgID=3 ambiguous case). Option A as originally proposed would have required ~1 day of dispatcher rewrites in four mash-go call sites plus equivalent work in every future port. Option D fixes the ambiguity at the wire level once, costs +2 bytes per control message, and lets all dispatchers stay simple. Spec window for the wire-format change was open (no production deployments), so the cheaper-now-than-later argument carried.

**Why D over A, B, C:**
- A (peek-is-a-hint, document only) was a punt: documenting "session state is authoritative" doesn't make today's code authoritative.
- B (reorder peek so status>4 wins) didn't cover the worst case (`Status:Success + msgID=3 + no payload` → connection close).
- C (forbid msgIDs in {1,2,3}) wasted three messageId slots and added a magic-number rule with no structural justification.

**Files updated 2026-04-25:**
- `docs/decision-log.md` — added DEC-083; cross-referenced DEC-069, DEC-082; updated changelog.
- `docs/transport.md §6.2-6.4` — control messages shown wrapped in tag 77.
- `docs/interaction-model.md §3.4` — new section documenting Control message wire form.
- `mash-go/pkg/wire/codec.go` — `EncodeControlMessage`/`DecodeControlMessage` use `cbor.Tag{Number: 77, ...}`; `PeekMessageType` simplified from ~92 lines to ~30 (tag prefix + key-4 discriminator).
- `mash-py/mash_py/wire/codec.py` + `peek.py` — same shape via `cbor2.CBORTag(77, ...)`.
- `mash-py/tests/vectors/wire/control_vectors.json` — regenerated; every entry now begins with `d84d`.
- `mash-py/tests/unit/wire/test_peek.py` — flipped the ambiguity-acknowledgement test to a regression guard.

---

### Issue #4 — Operational-cert generation: spec vs. mash-go divergences

**Status:** Open
**Surfaced during:** mash-py Phase 3 — porting cert helpers from `mash-go/pkg/cert`

**Implicit assumption.**
`docs/testing/behavior/commissioning-pase.md` §"Certificate generation algorithm" (lines 388-475) is the only normative description of operational-certificate content. It is internally consistent and unambiguous, but `mash-go/pkg/cert/generate.go` diverges from it in five places:

| Field | Spec (`commissioning-pase.md`) | mash-go (`generate.go`) |
|---|---|---|
| Subject Key Identifier | `SHA-1 of public key` (line 414 — RFC 5280 method 1) | `SHA-256(SubjectPublicKeyInfo_DER)[:20]` (line 38-39) |
| Device-ID hex case | `hash[0:8].hex().upper()` (line 425) | `hex.EncodeToString` → lowercase (line 63) |
| Subject CN | `<device-id>` (line 398) | `<device-id>` (line 208) — match |
| Subject O | `<zone-name>` (line 399) | `"MASH Device"` (line 139) |
| Subject OU | `MASH Device` (line 400) | `"V<vendor>"`, `"P<product>"` (lines 141-143) |
| NotBefore | `now() - 5 minutes` (line 446 — clock-skew allowance) | `now()` (line 211) |
| NotAfter | `now() + 365 days` | `now() + OperationalCertValidity` (1 year) — match |

**Languages affected.** Direct portability impact is small — TLS chain validation is driven primarily by issuer/subject DN matching, with SKI/AKI as a hint. But:

1. A mixed-language chain (Python device cert ↔ Go-issued zone CA, or vice versa) where one side uses SHA-1 SKI and the other uses SHA-256[:20] SKI may fail the AKI-matches-SKI consistency check that some path-validation libraries enforce.
2. mash-py implementing the spec literally produces different cert content than mash-go does, even before random fields (serial, validity).
3. Phase 22 conformance ("does the Go controller accept a Python-issued cert?" and the reverse) needs a decision on which content is canonical before tests can be written meaningfully.

**Spec options considered.**

A. **Update mash-go to match the spec.** SHA-1 SKI is RFC 5280 method 1, ubiquitous and what every X.509 library produces by default. Subject DN composition and the -5 min clock skew are uncontroversial. Cost: ~1 day of Go changes plus updating the in-tree test corpus that hard-codes Go's current outputs.
B. **Update the spec to match mash-go.** SHA-256[:20] SKI is more collision-resistant (DEC-040 made a similar argument for full-cert fingerprint as device identity). Subject-DN composition could be argued either way. Cost: revise `commissioning-pase.md` lines 388-475, file a DEC, possibly document the SKI choice as a deliberate non-default.
C. **Acknowledge both, treat operational-cert content as implementation-defined within constraints.** Spec specifies only what TLS verification depends on (signature alg, validity, AKI/SKI consistency *within an implementation*) and lets each side pick its hash and subject layout. Cost: weakest interop guarantee; Phase 22 cross-implementation cert tests become very narrow.

**Disposition.** Open — needs spec-owner decision. mash-py implements the spec as written (SHA-1 SKI, spec-conformant subject DN, -5 min NotBefore, uppercase device-ID hex) and tests against itself. Phase 22 will be the forcing function: the cross-language conformance run will surface whichever mismatch actually breaks chain validation in practice, and the resolution chosen there will dictate which side updates.

**Why mash-py implements the spec rather than mash-go's behavior.**
- Per the project's stated multi-implementation goal (DEC-082 framing), the spec is the contract. Cloning mash-go would defeat the portability check that mash-py exists to perform.
- The spec text is unambiguous; if it is wrong, that is a spec change, not a Python implementation choice.
- If interop in Phase 22 fails, mash-go and the spec disagree concretely — that's the cleanest possible signal for the spec owner to act on.

**Affected files (mash-py side only):**
- `mash-py/mash_py/crypto/certs.py` — implements spec.
- `mash-py/tests/unit/crypto/test_certs.py` — asserts spec output.

**Affected files (pending spec/Go decision):**
- `docs/testing/behavior/commissioning-pase.md` — may need revision under option B or C.
- `mash-go/pkg/cert/generate.go` — may need revision under option A.
- `mash-py-tdd-plan.md` Phase 22 — cross-implementation cert acceptance test is contingent on this decision.

---

### Issue #3 — `mash-test` is single-port-shaped at the harness layer

**Status:** Deferred (until mash-py Phase 4 ships)
**Surfaced during:** post-DEC-083 review of conformance coverage (2026-04-25)

**Implicit assumption.**
DEC-082 declares two-port deployment conformant alongside single-port and states that "mDNS SRV records carry the actual port per service." A real controller dialing the SRV-advertised port is indifferent to deployment shape. But `mash-go/cmd/mash-test` — which is itself a synthetic controller used to verify conformance — accepts a single `-target host:port` flag and dials it for both ALPN values (`internal/testharness/runner/runner.go:825-831`). The runner reads SRV records (`discovery_handlers.go:591-594`) for *verification* assertions but never *uses* them to drive TCP connections.

This is not a spec issue per se — DEC-082 is consistent and complete — but the canonical reference *test harness* doesn't honor the spec's discovery contract. Today this is invisible because mash-go is single-port (both SRV records advertise the same port). The moment a two-port device exists (mash-py Phase 4), conformance runs against it fail at the commissioning ALPN connection.

**Languages affected.**
None directly — both shapes are wire-compatible. But every future two-port implementation (Python now; C/Zig/Rust if/when they arrive) hits the same harness gap.

**Disposition.** Deferred to mash-py Phase 22. The fix is a mash-go change (add `-srv-resolve` flag to `mash-test`, browse mDNS per ALPN, dial the SRV-advertised port). Documented as a Phase 22 prerequisite in `mash-py-tdd-plan.md` because it is the forcing function — there is no two-port device to test against until Phase 4 lands, and conformance is the one place the gap actually bites. Until then, mash-go's single-port shape continues to work with the existing `-target` flag.

**Why deferred rather than fixed now.**
- No two-port device exists yet; the change has no concrete failing test to validate against.
- macOS mDNS browse reliability is already flaky (see `TC-MASHC-*`, `TC-NOTFOUND-*` clusters in `mash-go/test-base-protocol.log`); making the harness depend on real mDNS by default would worsen baseline noise before there's value to offset it.
- An opt-in flag preserves the existing single-port-friendly path until mDNS reliability is acceptable for the default flip.

**Related:** DEC-082 (two-port deployment), `mash-py-tdd-plan.md` Phase 4 ("Harness coverage caveat") and Phase 22 ("Prerequisite (mash-go harness change — DEC-082 compliance)").

**Update 2026-04-26:** Brought forward into Phase 7B as part of the SPAKE2+ block restructure. The `-srv-resolve` flag is needed before 7C/7D can run live PASE against `mash-py`'s two-port device, so the work moves from Phase 22 to Phase 7B.

---

### Issue #5 — SPAKE2+ specification is incomplete, contradictory, and missing memory-hard KDF

**Status:** ✅ Resolved (spec text) — implementation tracking via `mash-py-tdd-plan.md` Phases 7B / 7C / 7D.
**Resolution:** **DEC-084** (2026-04-26) — full SPAKE2+ algorithm specification adopting RFC 9383 + Matter conventions: fixed RFC 9383 P-256 M/N constants, PBKDF2-HMAC-SHA256 100K iterations as MHF, derived per-device salt, length-prefixed RFC 9383 §3.3 transcript, KcA/KcB confirmation MAC keys, mandatory identity-element rejection in point validation. Spec sections `commissioning-pase.md §2` and `§4.2` rewritten in Phase 7A. The remaining sub-phases (7B conformance vectors, 7C Go implementation update, 7D Python implementation) implement DEC-084 and validate cross-language byte-equivalence.
**Surfaced during:** mash-py Phase 7 design / first-pass implementation (2026-04-26).

**Implicit assumption.**
The SPAKE2+ section of the spec (`docs/testing/behavior/commissioning-pase.md §2.2 – §2.4`) was written prescriptively but assumes a level of cryptographic completeness it does not actually deliver. The Go implementation (`mash-go/pkg/commissioning/spake2plus.go`) was written 4 days later by the same author and silently chose different primitives in eight places without filing a DEC. Both artifacts share security weaknesses that are not language-specific. The Phase 7 audit concludes that neither the spec nor the Go implementation is currently fit to deploy: the spec is *unimplementable as written* (`K_shared` is invoked but never defined; `M = G * H(label)` "with hash-to-curve per RFC 9380" is type-mismatched; PASE_X declares 32-byte points but compressed P-256 is 33 bytes), and the Go implementation has *exploitable security holes* (no MHF before HKDF — verifier theft brute-forces the 8-digit code in seconds; transcript not length-prefixed; identity element not rejected during point validation).

**Languages affected.**
All — this is a spec-text issue plus a security-design issue. Every future implementer is blocked equally.

**The eight findings.**

| # | Finding | In spec? | In Go? | Severity |
|---|---|---|---|---|
| 1 | No Memory-Hard KDF before HKDF — verifier theft makes 27-bit setup code brute-forceable in seconds on a GPU. RFC 9383 §4 *requires* an MHF. Go's source contains a comment admitting this gap (`spake2plus.go:89`). | Yes (omits) | Yes (omits) | **Critical** — exploitable in MASH's deployment model |
| 2 | `K_shared` invoked in §2.4 but never defined anywhere in the spec. | Yes | N/A | **Critical** — unimplementable |
| 3 | PASE_X / PASE_Y wire size declared `bytes[32]` in §4.2 but compressed P-256 is 33 bytes, uncompressed is 65 bytes; neither is 32. | Yes | N/A (Go uses uncompressed 65 anyway) | **Critical** — wire-protocol blocker |
| 4 | M / N derivation formula contradictory: `M = G * H("MASH SPAKE2+ M v1")` requires `H` to produce a scalar, but spec immediately says "where H is SHA-256 with hash-to-curve per RFC 9380" — RFC 9380 produces points, not scalars. Also wrong RFC citation (9382 ≠ 9383). | Yes | No (uses RFC 9383 fixed P-256 constants verbatim — Go's hex matches RFC 9383 §4 byte-for-byte; the source comment claiming derivation from `"SPAKE2+-P256-SHA256-HKDF-SHA256-HMAC-SHA256 M"` is fictional) | **High** — interop blocker |
| 5 | Transcript not length-prefixed (Go), not specified at all (spec). RFC 9383 §3.3 requires 8-byte LE length prefixes per field to prevent canonicalization attacks (one identity field absorbing bytes from the next). | Yes (silent) | Yes (bare concat) | **Medium** — theoretical attack reachable under crafted identity strings |
| 6 | No identity-element check during point validation. RFC 9383 §3.2 requires *both* on-curve AND not-the-point-at-infinity; Go's `IsOnCurve` does only the first. Degenerate points force predictable Z and V. | N/A | Yes | **Medium** — exploitable with active attacker control of pA / pB |
| 7 | No identity binding in `w0` / `w1` derivation (spec); non-standard binding via HKDF salt (Go). RFC 9383 binds identities in the transcript, not in the verifier KDF. Identity binding prevents Unknown-Key-Share attacks under setup-code reuse (DEC-018 explicitly allows reuse). | Yes (omits) | Yes (non-standard placement) | **Low** — defense in depth; not actively reachable in current deployment model |
| 8 | HKDF salt / IKM inverted in Go: `salt=password, IKM=clientID || serverID`. Both are non-standard placements that don't match RFC 9383. | N/A | Yes | **Low** — non-standard but not insecure |

**Spec options considered.**

A. **Patch incrementally.** Open `commissioning-pase.md`, fix the eight findings one by one, file no DEC. Cost: ~2 hours. Outcome: still no security review; finding #1 (MHF) is left undocumented as a deferred "we should add this someday" without forcing function.

B. **Adopt RFC 9383 + Matter conventions wholesale via DEC-NNN.** Comprehensive rewrite of `§2` and `§4.2`; explicit MHF specification (PBKDF2-HMAC-SHA256, 100k iterations, per-device salt); fixed RFC 9383 M/N; length-prefixed transcript; KcA/KcB confirmation MAC keys per RFC 9383 §3.4; explicit `K_shared = SHA-256(TT)` definition; preserve spec's K_main / K_confirm / K_encrypt naming (the one place the spec is *better* than Go). Cost: ~1 day of spec work + 7B/7C work to follow.

C. **Match Go silently in mash-py and move on.** Cost: ~30 minutes. Outcome: defeats the spec-portability validation that is the entire reason mash-py exists; ships SPAKE2+ with known security holes (#1, #5, #6) into a 20-year deployment lifecycle.

D. **Pull a vetted SPAKE2+ library.** Replace the hand-rolled implementation in mash-go with a vetted one (Cloudflare's `circl` has SPAKE2+; CircuitMatter's `pase.py` is RFC-aligned). Cost: medium (introduces a dependency, requires audit of the library's MASH integration). Outcome: best security; tradeoff is loss of the educational / portability value of a hand-rolled reference. Not chosen because the project explicitly values implementation independence.

**Decision.** Option **B** (adopt RFC 9383 + Matter via DEC-NNN). Codified as Phase 7 block restructure: 7A spec rewrite + DEC, 7B mash-test conformance vectors derived from a third-party oracle, 7C mash-go updated to pass 7B, 7D mash-py implements DEC-NNN and passes 7B. See `mash-py-tdd-plan.md` for the full sub-phase plan including the open sub-decisions the DEC must settle (MHF choice, salt distribution, verifier storage layout, etc.).

**Why B over A, C, D:**
- A leaves finding #1 (the security-critical MHF gap) without a forcing function. There is no production deployment yet, so the cost of fixing this comprehensively is at its lifetime minimum *now*.
- C silently buries the most important output of the mash-py project (spec-portability validation) and ships known-broken crypto.
- D is technically sound but removes the implementation-independence the project values; revisitable later if the hand-rolled implementation continues to surface issues.

**Same-author drift, surfaced by mash-py.** Both spec and Go were written by the same author 4 days apart; neither was reviewed by anyone with prior production PAKE experience; no DEC documents the choices in either. mash-py was the first independent reading; surfacing this divergence is the project doing exactly the job it was designed for. Document this as a process win, not a process failure.

**Affected files.**

7A:
- `docs/testing/behavior/commissioning-pase.md` — full rewrite of §2.2-§2.4 and §4.2
- `docs/decision-log.md` — new DEC-NNN
- `docs/security.md` §4.4 — cross-reference DEC-NNN
- `mash-py-spec-issues.md` — flip this issue's status to ✅ Resolved

7B:
- `tools/spake2plus_oracle/` (new) — third-party reference implementation for vector generation
- `mash-go/testdata/spake2plus_vectors.json` (new)
- `mash-go/cmd/mash-test/spake2plus_conformance.go` (new) — vector runner
- `mash-go/cmd/mash-test/cmd_run.go` — `-srv-resolve` flag (also closes Issue #3 above)

7C:
- `mash-go/pkg/commissioning/spake2plus.go` — substantive rewrite
- `mash-go/pkg/commissioning/spake2plus_test.go`, `messages.go`, related call sites
- `mash-go/pkg/cert/store.go` — verifier-storage layout extension

7D:
- `mash-py/mash_py/commissioning/spake2plus.py` — replaces the uncommitted Phase-7-as-written draft
- `mash-py/mash_py/commissioning/mhf.py` (new)
- `mash-py/tests/{unit,vectors,interop}/commissioning/`

**Related decisions.** DEC-014 (SPAKE2+ adopted), DEC-018 (setup code reuse — strengthens the case for identity binding), DEC-NNN (this issue's resolution).

---

### Issue #6 — PASE failure-path message envelope: spec says `PASEComplete`, mash-go uses `CommissioningError`

**Status:** ✅ Resolved (spec text unchanged; mash-go fixed in same workstream — 2026-04-26)
**Surfaced during:** mash-py Phase 8 (PASE message exchange — client side), 2026-04-26.

**Resolution.** mash-go `pkg/commissioning/session.go` was updated to send `PASEComplete{ErrCodeAuthFailed}` on every in-PASE failure mode (wrong client identity, invalid pA, MAC mismatch). The deprecated `ErrCodeInvalidPublicKey` and `ErrCodeConfirmFailed` constants were removed from `messages.go`; the runner classifier still recognises the legacy numeric value `2` on the wire for backward-compat with pre-fix devices. Failure-path interop is now exercised live: `mash-py/tests/interop/commissioning/test_pase_against_go_device.py::test_live_pase_wrong_setup_code_raises_auth_failed` drives a wrong setup code against a fixed mash-go device and pins `error_code == ErrCodeAuthFailed (1)` — locking the DEC-047 unification.

The pre-resolution analysis below is preserved for traceability.

**The divergence.**
`docs/testing/behavior/commissioning-pase.md §2.5` is normative on the failure path:

> Failure of any check is a fatal protocol error — the receiver MUST abort the handshake, send `PASEComplete` with `ErrorCode = ErrCodeAuthFailed` (1) per DEC-047, and close the TLS connection.

mash-go's `pkg/commissioning/session.go` does this for a *confirmation-MAC mismatch* (sends `PASEComplete` at line 249–253), but for two earlier failure modes it sends `CommissioningError` (MsgType 255) instead:

| Failure | mash-go behavior | Spec mandate |
|---|---|---|
| `ClientIdentity != b"mash-controller"` | `CommissioningError{ErrorCode: ErrCodeAuthFailed=1}` (lines 180–186) | `PASEComplete{ErrorCode: ErrCodeAuthFailed}` |
| Invalid pA (point validation per §2.5) | `CommissioningError{ErrorCode: ErrCodeInvalidPublicKey=1}` (lines 198–204) | `PASEComplete{ErrorCode: ErrCodeAuthFailed}` |
| MAC mismatch | `PASEComplete{ErrorCode: ErrCodeConfirmFailed=2}` (line 237) | `PASEComplete{ErrorCode: ErrCodeAuthFailed=1}` |

The numeric error code on the wire is the same value (1) for the first two — `ErrCodeInvalidPublicKey` and `ErrCodeAuthFailed` are both `uint8 = 1`, with `ErrCodeInvalidPublicKey` already commented as deprecated in `messages.go:43`. But the *message envelope* is different: a spec-conformant controller looking for MsgType 4 with a non-zero error code would not match a MsgType 255 failure message.

The third row is a different drift: mash-go sends `ErrCodeConfirmFailed = 2` even though `messages.go:44` comments the constant as deprecated in favor of `ErrCodeAuthFailed = 1`. Per DEC-047 this is supposed to consolidate to a single auth-failure code to prevent information leakage.

**Languages affected.**
Any implementation that follows the spec literally — currently only mash-py. Today, mash-go is the only implementation that sends `CommissioningError` on PASE invalid-pubkey paths; mash-go's own client also handles both envelopes (`session.go:67–71, 97–101`), so Go-vs-Go interop is fine. mash-py picks up the issue because Phase 8 is the first non-Go client.

**Why this is a mash-go bug, not a spec issue.**
The spec's choice — always reuse `PASEComplete` for failure — is the right design:
- One PASE failure envelope means the controller's state machine has exactly one "expecting PASEComplete" branch instead of two ("expect PASEComplete *or* CommissioningError").
- It matches the DEC-047 principle of treating all SPAKE2+ failures uniformly to prevent timing/structural oracles. If a peer can distinguish "wrong identity" (CommissioningError) from "wrong setup code" (PASEComplete + non-zero code) by message type alone, that's a structural oracle on top of the timing oracle DEC-047 already addresses.
- `CommissioningError` (MsgType 255) is genuinely useful for cross-cutting failures *outside* the PASE handshake (device busy, retry-after hints — DEC-063). Using it for PASE-specific failures conflates two channels.

`CommissioningError` does have a legitimate spec role, just not this one. `docs/security.md §13` and DEC-063 define it as the early-rejection envelope when the device cannot even *start* a PASE session — e.g. busy with another commissioning attempt. Used that way, the message is sent immediately after (or instead of) `PASEResponse` and carries `ErrCodeBusy = 5` plus a `RetryAfter` hint. Reusing the same envelope for *intra-PASE* failures collides with that role and is what makes mash-go's behavior wrong.

**Disposition (spec-strict).**
- **Spec text:** No change required — `commissioning-pase.md §2.5` is correct. `CommissioningError` stays reserved for DEC-063 (busy / cross-cutting non-PASE errors).
- **mash-go fix (separate workstream):** Three changes in `pkg/commissioning/session.go`:
  1. On invalid pA (line 198–204), send `PASEComplete{ErrorCode: ErrCodeAuthFailed}` instead of `CommissioningError`.
  2. On wrong client identity (line 180–186), same.
  3. On MAC mismatch (line 237), use `ErrCodeAuthFailed = 1` instead of `ErrCodeConfirmFailed = 2` (DEC-047 unification).
  4. Drop the deprecated `ErrCodeInvalidPublicKey` and `ErrCodeConfirmFailed` constants from `messages.go` once callers are updated.
  5. Update `session_test.go` assertions that currently match `*CommissioningError` on PASE failure paths.
- **mash-py Phase 8 client (this PR):** Strict to spec. The PASE state machine accepts:
  - In the "expecting PASEResponse" slot: `PASEResponse` (success path) **or** `CommissioningError{ErrCodeBusy}` (DEC-063 busy rejection — distinct error class, not auth failure). Anything else is a protocol error.
  - In the "expecting PASEComplete" slot: `PASEComplete` only. Non-zero `ErrorCode` is auth failure.
  - mash-py does **not** accept `CommissioningError` in lieu of `PASEComplete` as a failure envelope — that would paper over the mash-go bug and weaken the spec contract.
- **Interop consequence.** With mash-go in its current state:
  - **Success path** (correct setup code): clean — both runtimes follow spec.
  - **Failure paths against mash-go device:** mash-py raises `PASEProtocolError("unexpected CommissioningError during PASE")` because mash-go sends the wrong envelope. Failure-path interop tests are deferred until mash-go ships the fix; the Phase 8 acceptance gate is the success-path handshake.
- **DEC-047 conformance test:** Once mash-go is fixed, add a `TC-COMM-NEG-*` row that asserts the device emits `PASEComplete` (not `CommissioningError`) on every PASE-internal failure mode. Until then the mash-py interop suite documents the gap explicitly.

**Files affected.**
- `mash-go/pkg/commissioning/session.go` — three send sites need to switch envelope and code.
- `mash-go/pkg/commissioning/messages.go` — remove deprecated `ErrCodeInvalidPublicKey` and `ErrCodeConfirmFailed` after callers are updated.
- `mash-go/pkg/commissioning/session_test.go` — update assertions that currently match `*CommissioningError` on PASE failures.
- `mash-py/mash_py/commissioning/messages.py` — Phase 8 implementation; supports the dual-envelope decode path until mash-go ships the fix.
- `mash-py/mash_py/commissioning/pase_client.py` — Phase 8 client treats both envelopes as auth-failure.

**Related decisions.** DEC-047 (consolidated error codes), DEC-084 (SPAKE2+ algorithm spec).

---

### Issue #7 — Commissioning cert install: spec/spec/Go three-way envelope divergence

**Status:** ✅ Resolved (DEC-085, 2026-04-27 — three downstream specs patched, mash-go updated, mash-py landed spec-strict, end-to-end interop green).
**Surfaced during:** mash-py Phase 9 (CSR & Certificate Install), 2026-04-26.

**Resolution.** Codified by **DEC-085** in `docs/decision-log.md`. The three drifted spec sources (`security.md §4.3/§4.5`, `stack-architecture.md §8.4`, `commissioning-pase.md §4.3`) were patched to match `message-types.md §3` (which was already correct). mash-go's `pkg/service/device_service_commissioning.go::performCertExchange` and the controller-side cert install (split out of `controller_renewal.go::IssueInitialCertSync` into a new `pkg/service/controller_cert_install.go::PerformCommissioningCertInstall`) both drive the spec-strict envelope (MsgTypes 10/11/12/13/20). Two new wire-decoder unit tests pin the cross-plane invariant. mash-py's Phase 9 implementation (`messages.py` + `cert_store.py` + `cert_exchange.py`) lands spec-strict. The Phase 9 acceptance gate — `tests/interop/commissioning/test_full_commissioning_against_go.py::test_full_commissioning_against_go_device` — runs end-to-end against a live mash-go device and verifies (a) the operational cert chains to the controller's Zone CA, (b) the device ID derives correctly from the CSR public key, and (c) the device transitions to `_mash._tcp` advertising with the new device ID.

The pre-resolution analysis below is preserved for traceability.

**The divergence.**
Three documents in the canonical spec describe the post-PASE certificate-install step on the commissioning ALPN (`mash-comm/1`), and they disagree on which envelope is on the wire:

| Source | Says cert install uses | Wire shape |
|---|---|---|
| `docs/message-types.md §1, §3` (marked "Authoritative inventory / Status: Normative") | **MsgTypes 10–13 + 20** — `CSRRequest`, `CSRResponse`, `CertInstall`, `CertInstallResponse`, `CommissioningComplete` — distinct from renewal MsgTypes 30–33 | Flat CBOR map, key 1 = `uint8` MsgType. §1 explicitly states 30–33 is renewal **only on `mash/1`** and that "senders must NOT send commissioning MsgTypes on `mash/1` or vice versa; receivers MUST close the connection on cross-plane frames." |
| `docs/security.md §4.3` flow diagram + `§4.5` line 250 ("After the device sends `CertInstallAck`, **message type 33**, it MUST close the commissioning TLS connection") | **MsgTypes 30–33** (renewal envelope) reused on commissioning | Same shape as renewal |
| `docs/stack-architecture.md §8.4 Phase 3` (lines ~701–708) | **MsgTypes 30–33** explicitly (`CertRenewalRequest / CertRenewalCSR / CertRenewalInstall / CertRenewalAck`) | Same shape as renewal |
| `docs/testing/behavior/commissioning-pase.md §4.3` (lines ~517–552) | A *third* form with **string** type identifiers (`"csr_req"`, `"csr_rsp"`, `"cert_install"`, `"cert_ack"`) | Inconsistent with §3 of the same doc, which mandates `uint8` for key 1. Predates the integer-MsgType regime. |

**mash-go behavior.** `pkg/service/device_service_commissioning.go::performCertExchange` and `pkg/service/controller_renewal.go::IssueInitialCertSync` both drive 30–33 (renewal envelope) on `mash-comm/1`. The constants `MsgCSRRequest = 10`, `MsgCSRResponse = 11`, `MsgCertInstall = 12`, `MsgCertInstallResponse = 13`, `MsgCommissioningComplete = 20` are defined in `pkg/commissioning/messages.go` and routed by `pkg/commissioning/protocol.go`, but are dead constants in production code — only their CBOR encoding is exercised by `protocol_test.go` and `messages_test.go`.

**Decision: `message-types.md` is canonical; the others are stale.**

The decisive argument is the namespace separation invariant in `message-types.md §1`. MASH supports both single-port (DEC-067) and two-port (DEC-082) deployments, and the entire dispatcher routing model — "pick the namespace from ALPN, then decode" — relies on disjoint MsgType ranges per plane. If 30–33 is dual-purpose (commissioning *and* renewal), the disjoint-range property collapses and dispatchers must branch on connection state to disambiguate. That stateful coupling is exactly what an explicit MsgType byte was supposed to eliminate.

The architectural model is:
- **Commissioning vs. operational** — namespace-separated by ALPN (`mash-comm/1` vs `mash/1`). Required for single-port deployments.
- **Renewal vs. data plane** (both on `mash/1`) — *not* namespace-separated; renewal must run on the operational connection in-session and shares the data channel. Disambiguated structurally: renewal frames are flat maps with `MsgType ∈ {30,31,32,33}`; data-plane frames have a `MessageID` field. This is the *only* multiplexing the spec needs at runtime; the rest is ALPN-driven.

Cert install during commissioning is a commissioning-plane operation: it happens before the device has a ZoneID, the trust anchor (Zone CA) is being delivered for the first time, and the channel is PASE-authenticated rather than cert-authenticated. These properties differ from renewal (post-commissioning, ZoneID known, mTLS-authenticated, in-session). They warrant distinct envelopes — `CommissioningComplete (20)` is the meaningful state-machine terminator that has no renewal analogue, and `CertInstall (12)` carries `zoneType + zonePriority` fields that renewal does not need.

mash-go's collapse of the two flows into one envelope was a shortcut that became wedged because three downstream docs (`security.md`, `stack-architecture.md`, `commissioning-pase.md`) updated to match the implementation rather than `message-types.md`.

**Spec changes.**
- `docs/security.md §4.3` flow diagram and `§4.5` line 250 — replace `CertInstallAck (msg type 33)` with the spec-correct sequence `CertInstallResponse (13) + CommissioningComplete (20)`.
- `docs/stack-architecture.md §8.4 Phase 3` — replace `CertRenewalRequest/CSR/Install/Ack` with `CSRRequest/CSRResponse/CertInstall/CertInstallResponse/CommissioningComplete`.
- `docs/testing/behavior/commissioning-pase.md §4.3` — convert string-keyed wire examples to integer-keyed CBOR maps with `MsgType` values matching `message-types.md §3`. Mark `ATTESTATION_REQ/RSP` as "RESERVED, wire format not yet specified" (no implementation).
- `docs/decision-log.md` — add **DEC-085**: "Commissioning cert install uses dedicated MsgTypes 10–13/20, not renewal envelope" — codifies the namespace-separation rationale.

**mash-go changes (this workstream).**
- Split commissioning cert install out of `controller_renewal.go::IssueInitialCertSync` into a new `controller_commissioning.go::PerformCertInstall` (or similar). Use `CSRRequest (10)` instead of `CertRenewalRequest (30)`, etc.
- Replace `device_service_commissioning.go::performCertExchange` to drive 10/11/12/13/20. Move zone-type/zone-slot validation from "received Zone CA in CertRenewalRequest" to "received Zone CA in CertInstall". On rejection send `CertInstallResponse{ErrorCode: ErrCodeZoneTypeExists}` (the existing path).
- Device sends `CertInstallResponse (13)` after install, then `CommissioningComplete (20)`, then closes the commissioning connection. Controller must read both before reconnecting on operational ALPN.
- Renewal stays untouched on `mash/1` with MsgTypes 30–33.
- Add a regression test asserting that a `MsgType ∈ {30,31,32,33}` frame on `mash-comm/1` is rejected, and that a `MsgType ∈ {10,11,12,13,20}` frame on `mash/1` is rejected.

**mash-test changes.**
- `mash-go/testdata/cases/commissioning-*.yaml` — any case observing wire bytes for commissioning cert install must be regenerated.
- Add cross-plane rejection cases (the regression test above, expressed at the conformance layer).

**mash-py changes.**
- `mash_py/commissioning/messages.py` — extend with `CSRRequest`, `CSRResponse`, `CertInstall`, `CertInstallResponse`, `CommissioningComplete` dataclasses + encode/decode + decode dispatch.
- `mash_py/commissioning/cert_store.py` — new file: controller Zone CA generation + CSR signing.
- `mash_py/commissioning/cert_exchange.py` — new file: controller-side flow driving 10/11/12/13/20.
- Tests: unit coverage of encode/decode + cert chain validation; live interop test against the post-fix mash-go device (Phase 9 acceptance gate).

**Languages affected.**
Every non-Go implementation, identical to Issue #6 — anyone reading `message-types.md` first will write 10/11/12/13/20 and find that mash-go does not respond.

**Why a coordinated fix and not a mash-py-only adapter.**
Unlike Issue #6 (where mash-py decoded both envelopes during the gap because the bug was a few send sites), Issue #7 is structural: the wire shape, the field set (`zoneType`, `zonePriority`), and the message count (5 vs 4) all differ. An adapter would mean mash-py emits a renewal-shaped `CertRenewalRequest` (30) on commissioning, which is exactly the cross-plane frame mash-go's own `message-types.md §1` rule says receivers must reject. The fix has to be on both sides at once.

**Related decisions.** DEC-067 (single-port ALPN routing — depends on namespace separation), DEC-076 (in-session renewal), DEC-077 (renewal window), DEC-082 (two-port deployment), DEC-085 (this resolution, pending).

---

### Issue #8 — DEC-085 cross-plane invariant: YAML conformance coverage deferred

**Status:** 🟡 Open — coverage gap deliberately accepted for Phase 9, follow-up scope.
**Surfaced during:** mash-py Phase 9 closeout, 2026-04-27.

**The gap.**
DEC-085 codifies a namespace-separation invariant: commissioning MsgTypes (1–4, 10–13, 20, 255) and renewal MsgTypes (30–33) are disjoint integer ranges, routed by ALPN. `docs/message-types.md §1` states explicitly: *"senders must NOT send commissioning MsgTypes on `mash/1` or vice versa; receivers MUST close the connection on cross-plane frames."*

In Phase 9 this invariant is currently pinned at **two layers**:

- **Wire decoder layer (Go unit tests):** `mash-go/pkg/commissioning/messages_test.go::TestDecodePASEMessage_RejectsRenewalMsgTypes` and `TestDecodeRenewalMessage_RejectsCommissioningMsgTypes`. Each parametrises across the cross-plane MsgType set and asserts the decoder errors on every value.
- **Cross-impl byte vectors:** `mash-go/testdata/cert_install_vectors.json` plus `mash-py/tests/unit/commissioning/test_cert_install_vectors.py`. Locks the canonical encoding for the commissioning-plane MsgTypes both directions.

What's missing: **YAML conformance cases at the runner / dispatcher layer** — i.e., a test that opens a real `mash-comm/1` TLS connection, hand-rolls a `MsgType ∈ {30,31,32,33}` frame, sends it, and asserts the device closes the connection or surfaces a protocol error. Symmetric case for `MsgType ∈ {10,11,12,13,20}` on `mash/1`.

**Why deferred.**
Decoder-level unit tests already pin the invariant where it lives in code: every dispatcher on both ALPNs routes through one of the two `Decode*Message` helpers, and both of them reject the cross-plane MsgType. For a buggy refactor to slip past, someone would have to (a) introduce a new dispatcher path that bypasses the existing decoders, or (b) widen one of the decoders to accept the other plane's MsgTypes. Both are visible code changes that would be flagged in review and broken by the existing unit tests.

YAML conformance cases would catch a *third* failure mode: a dispatcher path that uses the right decoder but routes its output to the wrong handler chain. That's a real residual risk but smaller than the two the unit tests already cover.

The implementation cost is non-trivial: the runner has no existing step handler for "send a frame with arbitrary MsgType bytes" because all current YAML actions operate at high-level semantics (e.g., `commission`, `send_pase_request`). Adding a `send_raw_frame` handler is itself an infrastructure piece that deserves design review — should it be a generic "send these bytes" or a typed "send commissioning frame with msg_type=N"? Where does it live in the YAML schema? Phase 9 doesn't need that infrastructure for any other purpose.

**Disposition.**
- **Phase 9 closeout:** decoder unit tests + cross-impl vectors are the load-bearing pin. YAML conformance is belt-and-suspenders coverage.
- **Reopens when:** any of the following triggers fire:
  1. A dispatcher refactor in mash-go bypasses `DecodePASEMessage` or `DecodeRenewalMessage` for any plane.
  2. A new plane (e.g., delegated commissioning per DEC-019) introduces a third MsgType range and the namespace-separation surface area grows.
  3. A `send_raw_frame` runner step handler lands for unrelated reasons (e.g., negative-test conformance for malformed frames in another phase).
- **Owner:** TBD — likely whoever lands #1 or #3 above adds the YAML cases as part of that workstream.

**Files affected (for the eventual fix).**
- `mash-go/internal/testharness/runner/<new>.go` — `send_raw_frame` step handler.
- `mash-go/internal/testharness/runner/keys.go` — action constant.
- `mash-go/testdata/cases/commissioning-namespace-rejection.yaml` (new) — covers both planes.
- This file — mark Resolved.

**Related.** DEC-085 (the invariant), Issue #7 (the workstream that surfaced this gap).

---

### Issue #9 — `device_id` is overloaded: cert-CN identity vs. `DeviceInfo.deviceId` attribute

**Status:** 🟡 Partially resolved — option 3 applied to `DeviceInfo.deviceId` (mash-go fix landed 2026-04-27); mDNS DI TXT alignment still open.
**Surfaced during:** mash-py Phase 10, 2026-04-27.

**The clash.**
The spec uses "device id" for two distinct identifiers and gives them incompatible formats:

| Concept | Spec source | Format | Example |
|---|---|---|---|
| Operational-cert CN | ``commissioning-pase.md §4.4`` | 16 uppercase hex chars (``SHA256(SPKI_DER)[:8].hex().upper()``) | ``8152A81882CD34C3`` |
| ``DeviceInfo.deviceId`` (feature 0x01, attribute 1) | ``device-info.md §"Device ID Format"`` | Human-readable: ``PEN<number>.<serial>`` or ``<vendor>-<product>-<serial>`` | ``PEN12345.EVSE-001-A`` |
| mDNS ``DI`` TXT record | ``discovery.md §2.3`` | Human-readable (per the spec example ``DI=PEN12345.EVSE001``) | — |

Three observations:

1. The **operational-cert CN** is cryptographically derived from the device's keypair. It cannot be the human-readable form because the keypair (and hence the CN) is generated only at commissioning time, while the human-readable identifier is stable across re-commissionings.
2. The **mDNS instance name and DI TXT** in `discovery.md §2.3` use the human-readable form (``PEN12345-EVSE001._mash._tcp.local`` / ``DI=PEN12345.EVSE001``). But mash-go actually advertises with the cert-CN form (``8152A81882CD34C3._mash._tcp.local`` / ``DI=8152A81882CD34C3``), which is what mash-py's Phase 6 browser is built to parse.
3. The **DeviceInfo.deviceId attribute** as implemented in mash-go is the bare CLI ``-serial`` value (``PHASE10-INTEROP-001``), which matches neither the spec's PEN-prefix nor the vendor-product-serial format.

So three sources, three different format conventions, and the two implementations (spec and mash-go) disagree at every join.

**What Phase 10 does.**
Phase 10 acceptance verifies the end-to-end pipe (cert install → operational mTLS → Read DeviceInfo → response decoded). The interop test does NOT couple the two device-id concepts — it asserts ``snapshot.device_id`` (cert CN) is the 16-hex value and that ``DeviceInfo.deviceId`` is some non-empty string, and stops there. Strict format checks are gated on this issue's resolution.

**Spec options to consider.**

1. **Rename for clarity.** Keep both identifiers but rename them in the spec so neither is called "device id":
   - ``CertCommonName`` or ``OperationalIdentity`` (the 16-hex one).
   - ``DeviceInfo.deviceId`` stays as the human-readable attribute.
   Then revise `discovery.md §2.3` so the mDNS examples are consistent with whichever ID actually lands in the ``DI`` TXT record (almost certainly the operational identity, since that is what controllers use to find a device they have already paired with).

2. **Make ``DeviceInfo.deviceId`` equal the cert CN.** Drop the human-readable format from `device-info.md §"Device ID Format"` and re-spec attribute 1 as the 16-hex operational identity. The PEN-form / vendor-product-serial information becomes a derived view from ``vendorId``, ``productId``, ``serialNumber``. This matches the mDNS de-facto reality but breaks the existing `device-info.md` example and forces mash-go to change its CLI plumbing.

3. **Fix mash-go to follow the spec.** Have mash-go return ``PEN<vendorId>.<serialNumber>`` (or the fallback form) for ``DeviceInfo.deviceId``, and have its mDNS ``DI`` TXT use that same value. Cert CN stays separate. This is the most spec-faithful resolution but requires updating mash-go and re-running every prior interop snapshot that pinned the 16-hex form.

4. **Accept the overloading and document the disambiguation.** Add a note to `device-info.md` and `commissioning-pase.md` explaining that "device id" is overloaded and which one applies in each context. No code changes; lower portability cost but worse for newcomers.

**mash-py impact today.**
mash-py is the controller in Phase 10. The spec divergence is not blocking — Phase 10's acceptance test passes — but Phase 13 (Python device model) will have to commit to a value for `DeviceInfo.deviceId` and that value should match whatever the spec finally lands on. Until then, mash-py treats the two identifiers as orthogonal opaque strings.

**Disposition.**
- **Decision (2026-04-27):** option 3 — fix mash-go to produce a spec-compliant ``DeviceInfo.deviceId``. The spec (``device-info.md §"Device ID Format"``) is the reference; mash-go's prior behaviour of putting the bare ``-serial`` value into attribute 1 was wrong on its face.
- **Resolution applied to:** ``mash-go/cmd/mash-device/main.go`` — added a ``specDeviceID(brand, model, serial)`` helper that returns ``<brand>-<model>-<serial>`` (the "without PEN" fallback form, since the reference CLI uses placeholder VendorIDs ``0x1234`` rather than real IANA PENs). All four device-creation paths (EVSE, Inverter, Battery, HeatPump) now plumb the spec-format ID through both ``model.NewDevice`` and ``deviceInfo.SetDeviceID``.
- **Phase 10 acceptance:** ``tests/interop/transport/test_operational_connection.py`` now asserts the exact ``MASH-Phase10-EVSE-X-PHASE10-INTEROP-001`` value, not just "non-empty string". Live interop test green.
- **Still open:** mDNS DI TXT alignment. ``discovery.md §2.3`` example shows ``DI=PEN12345.EVSE001`` (human-readable) but mash-go advertises with the cert-CN form (``DI=<16-hex>``). The mash-py Phase 6 browser is built around the cert-CN form. Reopening this would require coordinated changes to ``device_service_discovery.go``, ``mash-py/discovery/``, and the ``discovery.md`` example. Deferred to a follow-up — mDNS DI is independent of `DeviceInfo.deviceId` attribute semantics, and Phase 11 onwards does not depend on the human-readable form for discovery.
- **Phase 13 (Python device model):** unblocked. Use the same fallback form ``<vendor>-<product>-<serial>`` (or ``PEN<n>.<serial>`` if a real PEN is available) when populating attribute 1.

**Files changed.**
- ``mash-go/cmd/mash-device/main.go`` — ``specDeviceID()`` helper + four call sites updated.
- ``mash-py/tests/interop/transport/test_operational_connection.py`` — tightened assertion to spec-format.

**Files still affected by the deferred mDNS alignment.**
- ``docs/discovery.md §2.3`` — mDNS examples either revise to show 16-hex or stay human-readable depending on the eventual resolution.
- ``mash-go/pkg/service/device_service_discovery.go`` — instance-name + DI TXT composition.
- ``mash-py/mash_py/discovery/`` — the OperationalService TXT parsing already reads whatever string lands in DI, so no change needed there.

**Related.** DEC-067, DEC-082 (operational reconnect), Phase 10 of the TDD plan.

---

### Issue #10 — Initial messageId: spec says "Start at 1," mash-go emits `2`

**Status:** ✅ Resolved (2026-04-27, same day as #11)
**Surfaced in:** Phase 11 — InteractionClient implementation (2026-04-27)
**Resolution:** mash-go `pkg/interaction/client.go` — `nextMsgID` initial value changed from `1` to `0` so the first `atomic.AddUint32` returns `1` per spec §3.5. New regression test `TestMessageIDStartsAtOne` added.

**The divergence.**

`docs/interaction-model.md §3.5` is unambiguous:

> **Allocation Rules:**
> 1. Start at 1 for new connections
> 2. Increment monotonically for each request
> 3. On overflow (reaching 2³²-1), wrap to 1 (skipping 0)

mash-go (`pkg/interaction/client.go:54`, `93–101`) initialises the counter at `1` and uses `atomic.AddUint32(&c.nextMsgID, 1)`, which **pre-increments**. The first request from a Go client therefore carries `messageId = 2`, not `1`. mash-py honours the spec literally: first request → `messageId = 1`.

**Severity.** Cosmetic. Both peers accept any non-zero messageId, so interop is unaffected. The divergence is visible in wire dumps and protocol logs — a controller-side log analyser comparing Go and Python traces will see different starting ids.

**Disposition.** mash-go bug. The fix is one line:

```go
// Current (pkg/interaction/client.go:54):
nextMsgID: 1,
// Fix:
nextMsgID: 0,  // first AddUint32 returns 1 per spec §3.5
```

The wraparound check (`if id != wire.NotificationMessageID`) already protects the case where the counter rolls past `0xFFFF_FFFF` back to `0`.

**Why file it as a portability issue rather than skip.** This issue is a tell that mash-go's wire-level behaviour was occasionally written-then-tested rather than spec-then-tested. The same code-shape risk (off-by-one introduced by a `&counter, 1` paired with a `1` initialiser) is likely to recur elsewhere; flagging keeps the principle visible.

**Related.** Phase 11 of the TDD plan; Issue #11 (also surfaced in Phase 11).

---

### Issue #11 — Invoke command parameters: spec specifies integer keys, mash-go uses string keys

**Status:** ✅ Resolved (2026-04-27, same day as surfaced)
**Surfaced in:** Phase 11 — interop test against `mash-device` removeZone command (2026-04-27)
**Resolution:** mash-go converted to integer-keyed Invoke parameters and responses end-to-end. YAML migration added explicit `id:` fields to all 17 commands across 7 features; codegen now emits `<Cmd>Param<Name>` and `<Cmd>Resp<Name>` `uint8` constants plus `<Cmd>ParamNameToID` / `<Cmd>RespNameToID` lookup tables for harness use. Interfaces (`model.CommandHandler`, `Feature.InvokeCommand`, `Device.InvokeCommand`, `dispatch.DeviceModel.InvokeCommand`, `interaction.Client.Invoke`, `service.DeviceSession.Invoke`, `service.ZoneSession.Invoke`) all switched from `map[string]any` → `map[uint8]any`. Single shared helper `wire.ToUint8Map` does the CBOR-decode narrowing (CBOR widens uint8 keys to uint64 during generic decode). mash-py interop verified: `test_invoke_remove_zone_decodes_integer_keyed_params` passes — the post-fix failure mode is `CONSTRAINT_ERROR` (zoneId mismatch, expected because mash-py doesn't compute the device-derived zone-id `sha256(k_main)[:8]`), proving the integer-keyed wire form decodes correctly.

**The divergence.**

`docs/interaction-model.md §7.1` shows command parameters as an integer-keyed CBOR map:

```cbor
5: {
  1: 1,              // commandId: SetLimit
  2: {               // command parameters
    1: 6000000,      // consumptionLimit  ← INTEGER key
    4: 2             // cause: LOCAL_PROTECTION
  }
}
```

This is consistent with the protocol-wide design choice in `§2.2`:

> All CBOR maps use integer keys for efficiency.

mash-go (`pkg/service/dispatch/protocol_handler.go:738`) decodes Invoke parameters as `map[string]any` and silently drops every entry whose CBOR key isn't a string:

```go
func convertMapAnyAnyToStringAny(m map[any]any) map[string]any {
    result := make(map[string]any)
    for k, v := range m {
        if key, ok := k.(string); ok {
            result[key] = v   // integer-keyed entries are dropped silently
        }
    }
    return result
}
```

Downstream, `pkg/service/remove_zone_handler.go:81` looks up parameters by name constants (`features.RemoveZoneParamZoneID = "zoneId"`), and the response payload is built the same way (`{"removed": true}` instead of `{1: true}`).

**Severity.** Hard interop break for the Invoke operation. A spec-conformant client (mash-py, integer keys) sends `{1: "zone-id"}`; mash-go's decoder drops the only entry, `removeZone` finds no `zoneId`, returns `INVALID_PARAMETER`. Symmetrically, a mash-go-style invoke against a spec-conformant device fails the same way. Read / Write / Subscribe are unaffected — they correctly use integer keys throughout.

**Why integer keys are correct (per the spec design).**

1. **Wire size.** Typical command names are 8–20 chars. With ten parameters that's ~150 bytes of overhead in keys alone vs ~10 bytes for integer keys. At the 8 KB hard ceiling (DEC-069), those bytes come out of payload room.
2. **MCU dispatch cost.** DEC-003 targets 256 KB ESP32-class MCUs. Integer-keyed parameters allow `switch (param_id)` dispatch into a packed struct; string-keyed parameters force `strcmp` per key plus heap allocation for the key strings unless the parser is unusually careful.
3. **Consistency.** Every other map in the protocol is integer-keyed (Read response §4.2, Write request/response §5.1/§5.2, Subscribe priming §6.2, Notification §3.3, Error §8.2). Invoke params being the lone string-keyed map breaks the codec author's reasonable invariant "all map keys are int."
4. **Schema discipline.** Explicit parameter IDs in YAML force allocation review, the same way attribute IDs do; names alone allow silent collisions across feature versions.
5. **Identity stability.** Renaming a parameter is display-only with integer IDs; with string keys, every rename is a wire-breaking change for every existing peer.

The string-key approach offers exactly one benefit: human-readable wire dumps. That is real but mitigated by tooling (`mash-log` already pretty-names attribute IDs).

**Disposition.** mash-go bug. Fix in `pkg/service/dispatch/protocol_handler.go` and the per-command handlers:

1. Decode Invoke parameters as `map[uint8]any` (or `map[uint16]any`) keyed by parameter ID, with a converter that accepts both string and integer keys during a deprecation window.
2. Update each command handler (`pkg/service/remove_zone_handler.go` and friends) to look up by parameter ID constants instead of name strings. Generated handlers in `pkg/features/*_gen.go` already have access to the parameter list — codegen needs to synthesise ID constants and ID-keyed lookups.
3. Build response payloads with integer keys.
4. Update `pkg/interaction/client.go::Invoke` signature: parameters become `map[uint8]any` or a typed payload struct.

**Codegen impact.** Each command in the YAML feature spec needs explicit parameter `id:` fields, parallel to attribute IDs. `device-info/1.0.yaml` for example would change:

```yaml
commands:
  - id: 0x10
    name: removeZone
    parameters:
      - { id: 1, name: zoneId, type: string, required: true }   # ← add id: 1
    response:
      - { id: 1, name: removed, type: bool, required: true }    # ← add id: 1
```

This is a small YAML migration touching every command in every feature; the codegen template change is mechanical.

**mash-py disposition.** mash-py is spec-faithful as of Phase 11 (sends integer keys, expects integer-keyed responses). The Phase 11 interop test for Invoke is marked `xfail(strict=True)` — it expects the divergence today and will flip to passing the moment mash-go is updated.

**Open question — controller-side bidirectional Invoke.** §11 mentions devices invoking commands on controllers (SMGW/EMS scenarios). When that lands in mash-py (Phase 13+), the same encoding will apply. Settling integer-keyed parameters now avoids two divergence points later.

**Related.** Phase 11 of the TDD plan; Issue #10 (also Phase 11). DEC-069 (8 KB max msg), DEC-003 (256 KB MCU target).

---

### Issue #12 — `EndpointInfo.featureMap` (spec field 5): spec text predates DEC-074

**Status:** ✅ Resolved 2026-04-27 in commit `ff7ba07` — spec field 5 dropped, DEC-074 addendum landed; both reference impls were already four-field-correct.
**Surfaced in:** Phase 13 — drafting `mash_py.model.Endpoint` against `docs/features/device-info.md` (2026-04-27)
**Blocks:** mash-py Phase 15 (operational server) acceptance — pick a side before emitting `EndpointInfo`
**Recent context:** DEC-074 (2026-04-23, 4 days old) reframes featureMap bit semantics in a way that undermines field 5; the spec example in device-info.md hasn't been updated yet.

**The divergence.**

`docs/features/device-info.md` declares the `EndpointInfo` shape returned in `DeviceInfo.endpoints` (attribute id 20):

```cbor
EndpointInfo:
{
  1: id,                     // uint8: endpoint ID
  2: type,                   // EndpointTypeEnum
  3: label,                  // string?: user-friendly name
  4: features,               // uint16[]: feature IDs on this endpoint
  5: featureMap,             // uint32: feature flags bitmap
}
```

mash-go's `pkg/model/endpoint.go:151` defines:

```go
type EndpointInfo struct {
    ID       uint8        `cbor:"1,keyasint"`
    Type     EndpointType `cbor:"2,keyasint"`
    Label    string       `cbor:"3,keyasint,omitempty"`
    Features []uint16     `cbor:"4,keyasint"`
}
```

Field 5 (`featureMap`) is absent. `Endpoint.Info()` populates the four fields above and never writes a featureMap. A spec-conformant controller reading `DeviceInfo.endpoints` therefore sees nothing where field 5 should be.

**Why field 5 *would* matter, naively.** The point of `EndpointInfo` is one-shot capability discovery: a controller reads `DeviceInfo` once and learns the entire endpoint topology in a single round-trip. With field 5 absent, a controller still gets the per-feature `featureMap` (0xFFFC, scoped to *one* feature) by reading each feature individually — but that's an extra read per feature per endpoint.

Note: the *per-feature* `featureMap` global attribute (id 0xFFFC) IS populated correctly on the wire (handled by `Feature.SetFeatureMap` and the global-attribute machinery). The gap is the spec's *aggregated per-endpoint* `featureMap` slot.

**Why DEC-074 is the load-bearing context.**

Four days before this issue surfaced, **DEC-074 (2026-04-23, commit `2ccc270`)** redefined what `featureMap` bits actually mean:

> `featureMap` bits encode **capability variants within a feature**. Same feature type (e.g. Electrical) can carry different bits on different devices: EVSE → `CORE|FLEX|EMOB|V2X`, battery inverter → `CORE|FLEX|BATTERY`. […]
>
> **Feature presence** is discovered via the endpoint's `featureList` and the per-feature existence check, NOT by inspecting featureMap bits.

DEC-074's stated rationale specifically calls out cross-feature aggregation as a category error:

> Matter's featureMap is scoped to a cluster (feature), not a device. DEC-035's cross-feature unification was a premature flattening.

In the same commit, DEC-074 dropped the device-level `FM` key from operational mDNS TXT records for exactly this reason ("[a] single device-level 'FM' hex number is lossy" because "bit semantics differ per feature").

**`EndpointInfo.featureMap` (field 5) is the same kind of cross-feature aggregation that DEC-074 rejected at the mDNS-TXT level.** The spec example payload in device-info.md (lines 97-119) shows `featureMap: 0x0001` and `featureMap: 0x0009` per endpoint — values that under DEC-074 have no well-defined cross-feature semantics. The numbers in the example are leftovers from DEC-035's superseded device-level framing.

**The actual divergence is in the spec, not in mash-go.** mash-go's omission of field 5 from `EndpointInfo` is DEC-074-aligned: don't advertise an aggregated bitmap because there's no consistent per-bit semantic. The spec text in `docs/features/device-info.md` predates DEC-074 by three weeks and hasn't been updated to reflect it.

**Spec options considered.**

A. **Honor the example — add field 5 to mash-go.** Wire-compatible but fights DEC-074. Would re-introduce the cross-feature ambiguity that DEC-074 explicitly removed from mDNS TXT. **Rejected.**
B. **Drop field 5 from the spec.** Update `docs/features/device-info.md`:
   - Remove `5: featureMap, // uint32` from the `EndpointInfo` shape.
   - Update the example payload to four-field endpoint records.
   - Add a one-line note pointing to DEC-074 + the per-feature `featureMap` (0xFFFC) read for capability variant discovery.
C. **Reframe field 5 as "feature-presence bitmap."** A 64-bit map where bit N is set iff feature ID N is on this endpoint. Disjoint from per-feature variant semantics. But this duplicates field 4 (`features: uint16[]`) for no clear benefit; sorted-uint16-array beats sparse bitmap when you have <64 features.

**Recommendation.** **B.** mash-go is already there. mash-py's Phase 13 model exposes `Feature.feature_map` / `Feature.set_feature_map()` (per-feature, DEC-074-aligned) but deliberately does NOT expose an `Endpoint.feature_map` aggregator slot — the omission is intentional and matches Option B.

**Disposition (proposed).**

1. Spec edit in `docs/features/device-info.md`:
   - Drop `5: featureMap, // uint32: feature flags bitmap` from the `EndpointInfo` shape (lines 68-76 region).
   - Remove the `featureMap: 0x0001` / `0x0009` lines from the example payload (lines 97-119 region).
   - Add a one-sentence note: "Per-feature capability variants are discovered by reading global attribute `featureMap` (0xFFFC) per feature; see DEC-074."
2. Add to DEC-074's "Implementation notes" that `EndpointInfo` field 5 is also dropped (the mDNS TXT precedent generalizes).
3. mash-go: no change. Existing `EndpointInfo` 4-field struct is correct.
4. mash-py: no change. Phase 13's `Endpoint` does not aggregate, by design.
5. Add a one-line mash-py-tdd-plan.md note in Phase 15 ("emit four-field `EndpointInfo` per DEC-074-extension").

**No code change required in either implementation.** This issue resolves with a single spec PR.

**Affected files (when resolved).**
- `docs/features/device-info.md` — drop field 5 from EndpointInfo shape + example.
- `docs/decision-log.md` — DEC-074 implementation-notes addendum.
- `mash-py-tdd-plan.md` — Phase 15 emission note.

**Related.** **DEC-074** (commit `2ccc270`, 2026-04-23 — featureMap bit semantics, dropped mDNS TXT FM key); DEC-035 (superseded device-level framing); Phase 13 of the TDD plan.

---

### Issue #13 — `device-info.md` example uses 16-bit feature IDs that contradict `protocol-versions.yaml`

**Status:** ✅ Resolved 2026-04-27 in commit `a5a25ec` — example rewritten with canonical YAML IDs; inline Feature ID Registry table replaced with a one-line pointer to `protocol-versions.yaml`; `docs/features/README.md` admonition extended to declare the YAML registry canonical for codegen.
**Surfaced in:** Phase 13 — auditing the spec for endpoint/feature ID widths (2026-04-27)
**Blocks:** Nothing today; flagged so a spec edit isn't held back when the next port asks "which is right?"

**The divergence.**

`docs/features/device-info.md` lines 97-119 illustrate `DeviceInfo.endpoints` with 16-bit feature IDs:

```cbor
endpoints: [
  { id: 0, type: DEVICE_ROOT, features: [0x0006], featureMap: 0x0001 },
  { id: 1, type: EV_CHARGER, label: "Port 1",
    features: [0x0001, 0x0002, 0x0003, 0x0005, 0x0007], featureMap: 0x0009 },
  ...
]
```

And the prose registry directly below (lines 124-137):

```
| ID | Name |
|----|------|
| 0x0001 | Electrical |
| 0x0002 | Measurement |
| 0x0003 | EnergyControl |
| 0x0005 | Status |
| 0x0006 | DeviceInfo |
| 0x0007 | ChargingSession |
| 0x0008 | Signals |
| 0x0009 | Tariff |
| 0x000A | Plan |
```

But the canonical machine-readable registry — `docs/features/protocol-versions.yaml`, the source codegen consumes for both Go and Python — assigns:

```yaml
DEVICE_INFO:      0x01
STATUS:           0x02
ELECTRICAL:       0x03
MEASUREMENT:      0x04
ENERGY_CONTROL:   0x05
CHARGING_SESSION: 0x06
TARIFF:           0x07
SIGNALS:          0x08
PLAN:             0x09
TEST_CONTROL:     0x0A
```

The example payload in device-info.md is therefore wrong on **two** axes simultaneously:

1. **Width mismatch.** Example uses `0x0006` (`uint16`-shaped); registry assigns `0x06` (`uint8`). The wire format in `EndpointInfo.features` is declared as `uint16[]` (device-info.md §EndpointInfo line 73), so the width itself is correct — but writing `0x0006` instead of `0x06` in the prose example is misleading.
2. **Value mismatch.** Even after width-normalizing, the example claims `DeviceInfo = 0x06`, `Electrical = 0x01`, `Status = 0x05`, `ChargingSession = 0x07`. The registry says `DeviceInfo = 0x01`, `Electrical = 0x03`, `Status = 0x02`, `ChargingSession = 0x06`. **All seven mappings disagree.**

**Severity.** Documentation-only. No implementation reads the prose example — both Go and Python codegen consume `protocol-versions.yaml`, which is internally consistent. A new-language porter who reads the prose first and the YAML second will lose ~30 minutes reconciling, and may file a spurious bug against whichever registry they read first.

**Recent context.** Commit `58e9a4b` (2026-04-25, "docs+docgen: reconcile YAML drift and expand onboarding") swept several YAML-vs-prose drifts (charging-session `sessionId`, `evMin`/`evTargetStateOfCharge`, energy-control `optionalProcess` flattening, three-doc `max_zones` mismatch). The `device-info.md` feature-ID example/registry drift was missed in that pass — surfacing it here so it's queued for the next one.

**Disposition.** Doc fix in `docs/features/device-info.md`:
1. Re-do the example endpoint payload with the registry's actual IDs (`features: [0x03, 0x04, 0x05, 0x02, 0x06]` for an EV_CHARGER with Electrical/Measurement/EnergyControl/Status/ChargingSession). Resolve Issue #12's field-5 question first so the example doesn't carry forward `featureMap: 0x0009`.
2. Either drop the embedded "Feature ID Registry" table from device-info.md (replace with a pointer to `protocol-versions.yaml`) or auto-generate it from the YAML via the `mash-docgen` pipeline so it can't drift again — same approach commit `58e9a4b` used for other normative tables.
3. Add a one-line note in `docs/features/README.md` that the YAML registry is canonical and prose tables are illustrative only (commit `58e9a4b` already added a normative-reference banner to per-feature pages; extend it to README).

**Recommendation.** Auto-generate from the YAML. The prose table will drift again the moment a new feature is added; the YAML registry has machinery (`mash-docgen` per `58e9a4b`) that already handles regeneration.

**No code action required.** mash-py and mash-go are both registry-faithful as of Phase 12 codegen.

**Related.** Phase 12 codegen (YAML as single source of truth); commit `58e9a4b` (the doc/YAML drift sweep that this slot belongs in); Issue #12 (resolve first to avoid re-encoding the spec example with a stale field 5); DEC-074 (featureMap).

---

## Backlog (anticipated; not yet hit)

The following are *suspected* spec assumptions that may need similar treatment when mash-py reaches the relevant phase. Listed here so we recognize them when we see them.

- **Per-connection goroutine pattern.** Go spawns a goroutine per connection's read loop; subscriptions also tend to spawn goroutines. Most languages have async equivalents (Python asyncio, Rust tokio), but C/Zig require explicit threading. Probably not a spec issue — wire behavior is what matters — but if the spec ever uses "goroutine" or "channel" prescriptively, flag it.
- **`map[uint16]any` and similar dynamic-typed payloads.** Wire is CBOR; types come from the YAML schema (codegen). For Rust/Zig, every payload type must be enumerable at compile time. If any spec text says "an arbitrary value at this position," flag it as needing a closed type union.
- **Context propagation (`context.Context`).** Go's context is for cancellation/deadlines. The spec shouldn't depend on context features being available in the implementer's language; if it does (e.g., "the device MUST honor controller-side deadlines via X"), check that X is portable.
- **Atomic operations.** DEC-064's connection cap is implemented in Go with `atomic.Int32`. Most languages have equivalents but a mention of `atomic.Bool` in spec text would be Go-shaped — should be "atomically updated boolean" or similar.
- **Filesystem layout assumptions for cert storage.** Go's `cert.Store` is an interface; reference impl is filesystem. Spec should not mandate a specific persistence mechanism. Verify when porting cert-renewal logic (Phase 19).

These are *prompts for vigilance*, not open issues. Move them up to the Issues section if/when we hit one.

---

## Out of scope for this tracker

- **Bugs in mash-go.** File those as Go issues; don't conflate with spec portability.
- **Python-specific implementation choices.** "asyncio vs threads" is mash-py's call; not a spec issue unless the spec mandates an async model (which it shouldn't).
- **Performance differences.** Python is slower than Go; that's expected and not a spec issue.
- **Post-CircuitPython concerns.** When/if mash-py targets CircuitPython, the constraints shift (no `cryptography` package, etc.); that's a mash-py implementation choice, separate from spec portability.
