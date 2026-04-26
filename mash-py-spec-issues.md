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
