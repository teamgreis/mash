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
