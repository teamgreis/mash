# MASH Python Reference Implementation — TDD Plan

**Status:** Draft for review
**Date:** 2026-04-25
**Companion to:** `mash-go/` (Go reference implementation)

This plan covers a parallel Python implementation of the MASH protocol. Every phase is grounded in concrete file/symbol references from `mash-go`, gathered through targeted source exploration; no claims are speculative. The plan is structured so each phase ends with a *live interop check against `mash-go`*, not just unit-test green.

---

## Table of Contents

1. [Goals & Non-Goals](#1-goals--non-goals)
2. [Architectural Decisions to Make Up-Front](#2-architectural-decisions-to-make-up-front)
3. [Python Stack Architecture](#3-python-stack-architecture)
4. [Repository Layout](#4-repository-layout)
5. [TDD Methodology](#5-tdd-methodology)
6. [Cross-Language Test Vector Strategy](#6-cross-language-test-vector-strategy)
7. [Phased Build Plan](#7-phased-build-plan) — 22 phases, each with acceptance + interop verification
8. [Conformance Gate](#8-conformance-gate)
9. [CI / Tooling](#9-ci--tooling)
10. [Risks & Mitigations](#10-risks--mitigations)
11. [Open Questions for the User](#11-open-questions-for-the-user)

---

## 1. Goals & Non-Goals

### Goals

- A Python reference implementation that **interoperates byte-for-byte** with the Go reference: a Python device commissioned by a Go controller, a Go device commissioned by a Python controller, all combinations.
- Passes the existing Go conformance harness (`mash-test`) when run against a Python device.
- Passes a Python conformance harness (port subset of the Go runner) when run against a Go device — proves the harness itself is portable.
- Treats `docs/features/*.yaml` as the single source of truth: Python codegen reads the same YAML as Go codegen.
- Is suitable both for desktop/server use (CPython 3.11+) and *eventually* for ESP32-class targets (CircuitPython); the latter is opt-in, not a hard constraint on phase 1.

### Non-Goals (Phase 1)

- Replacing `mash-go` as the canonical reference. The Go implementation remains canonical; Python is a peer that must agree.
- A from-scratch SPAKE2+ implementation. We lift the algorithm core from Adafruit CircuitMatter (MIT, RFC 9383 conformant) and wrap it in MASH-specific framing.
- Backwards compatibility with non-existent earlier protocol versions.
- A graphical commissioning UI. CLI only for now.

---

## 2. Architectural Decisions to Make Up-Front

These need a one-sentence answer from the user **before phase 1 starts** because they shape the whole stack.

### Decision A: Two-Port Deployment per DEC-082 — **LOCKED**

**Background.** The Go device listens on a single TCP port (8443) and uses `tls.Config.GetConfigForClient` to inspect the ClientHello's `SupportedProtos` list and return a *different* `tls.Config` based on whether `mash-comm/1` or `mash/1` is offered (DEC-067). Python's stdlib `ssl` module has no equivalent callback — `SSLContext.sni_callback` only fires on SNI, and `set_alpn_protocols` only chooses among offered protocols.

**Resolution.** This portability gap was elevated to a spec issue. **DEC-082 (2026-04-25)** formalizes two-port deployment as a conformant alternative to DEC-067's single-port. Both patterns produce identical wire behavior; the difference is observable only as different mDNS SRV ports.

mash-py uses the two-port pattern:
- **Operational listener:** TCP port 8443. SSLContext with operational ALPN `mash/1` only, `CERT_REQUIRED` client auth, Zone CA-signed cert (selected per zone via `sni_callback` — Phase 20).
- **Commissioning listener:** TCP port 8444 (default; configurable). SSLContext with commissioning ALPN `mash-comm/1` only, `CERT_NONE` client auth, self-signed stable cert.

mDNS SRV records advertise the actual port for each service; controllers (Go or Python) dial the SRV-advertised port without caring about the deployment shape.

**Why this is now the right choice:**
- **Spec-conformant per DEC-082.** Not a deviation; the spec explicitly accepts both patterns.
- **Trivial in stdlib `ssl`.** Two `asyncio.Server` instances, two `SSLContext`s, no BIO trickery, no ClientHello parsing.
- **Same security guarantees.** Separate listeners with independent TLS configs prevent operational-cert leakage by the same mechanism single-port does — different `tls.Config` objects, just bound to different sockets.
- **No spec-portability burden.** Future C/Zig/Rust ports inherit DEC-082 directly.

**Implementation cost.** ~50–80 LOC in `mash_py/service/device.py` to manage two listeners with shared connection-cap counter (DEC-064). The dispatcher and ClientHello parser previously planned for Phase 4b are no longer needed.

**Rejected alternatives:**

| Option | Why rejected |
|---|---|
| Single-port ClientHello dispatcher | ~400 LOC of TLS-protocol code; security-sensitive parser bugs possible; not needed once DEC-082 lands. |
| PyOpenSSL with `set_tlsext_servername_callback` | Heavyweight C-extension dep; CircuitPython unsupported; pulls us off the CPython-stdlib happy path. |
| Single SSLContext + post-handshake validation | Cert mismatch — operational needs Zone CA-signed cert, commissioning needs self-signed; one SSLContext can present only one cert chain. |

**Cross-references:**
- Spec: DEC-067 (single-port, recommended for capable implementations), DEC-082 (two-port, conformant alternative — the canonical pattern for languages without dynamic TLS config callbacks).
- Tracker: `mash-py-spec-issues.md` Issue #1 (resolved by DEC-082).

### Decision B: CircuitPython Compatibility — Phase 1 Constraint or Phase 2 Stretch?

**Implication of "Phase 1 constraint":** No `cryptography` package (uses CFFI, doesn't run on CircuitPython). Must use `ecdsa` (pure-Python) and a hand-rolled AES-CCM/AES-GCM, like CircuitMatter does. Slower; more code to write; smaller library set.

**Implication of "Phase 2 stretch":** Phase 1 uses the full `cryptography` library (TLS 1.3, X.509, EC, AEAD all in one). Phase 2 swaps in CircuitPython-compatible primitives behind a thin `mash_py.crypto` interface.

**Recommended default:** Phase 2 stretch. Phase 1 should use `cryptography`; we get a working device faster and the interface boundary makes the swap mechanical later.

**Decision needed from user:** confirm phase-2 stretch.

### Decision C: Codegen Strategy

**Background.** `mash-go/cmd/mash-featgen/templates.go` contains all Go templates inline. funcMap helpers like `goTypeName`, `accessConst` are Go-specific. Adding a `--lang python` flag would conflate two languages in one tool.

**Recommended default:** Separate `mash-py/codegen/` Python tool using Jinja2 templates, reading the same YAML in `docs/features/`. The two generators must agree on schema; a small CI job will diff their outputs' "spec coverage" to catch drift.

**Decision needed from user:** confirm the separate-generator approach.

### Decision D: Async Model

**Recommended default:** asyncio throughout. The Go impl uses one goroutine per connection (read loop) plus background goroutines for accept loops, mDNS, and notification dispatch. Python `asyncio` Tasks map cleanly:
- One `asyncio.Task` per connection's read loop (mirrors Go's read-loop goroutine — `pkg/transport/connection.go:435–482`).
- One Task per accept loop.
- One Task per zone's `NotificationDispatcher` (`pkg/service/dispatch/notification_dispatcher.go:82–89`).

**Decision needed from user:** any objection to asyncio (vs. threads, trio, anyio)?

### Decision E: SPAKE2+ Sourcing

**Recommended default:** lift the SPAKE2+ algorithm core from CircuitMatter's `circuitmatter/pase.py` (MIT). Specifically the functions `Crypto_pA`, `Crypto_pB`, `Crypto_Transcript`, `Crypto_P2`, `compute_session_keys`, `compute_verification`. Re-use under MIT attribution; do not depend on the package directly because its message framing is Matter-specific. Wrap the core in a MASH-shaped module: `mash_py.commissioning.spake2plus`.

**Verification target:** the M and N points in `mash-go/pkg/commissioning/spake2plus.go:38–49` are the same RFC 9383 hex strings CircuitMatter uses. The HKDF info string differs (Go uses `"SPAKE2+-P256-SHA256"`, Matter uses Matter-specific). So Python must use the *Go* info string when targeting MASH interop.

**Decision needed from user:** confirm CircuitMatter-as-reference is acceptable (MIT license terms are simple — preserve copyright header).

---

## 3. Python Stack Architecture

### Module dependency graph

```
                ┌──────────────────────────────┐
                │   mash_py.cli                │  (mash-device, mash-controller, mash-test-py)
                └───────────────┬──────────────┘
                                │
          ┌─────────────────────┴──────────────────────┐
          ▼                                            ▼
┌────────────────────┐                       ┌────────────────────┐
│ mash_py.service    │                       │ mash_py.testharness│
│  Device + Ctrl     │                       │  runner, loader    │
│  orchestration     │                       └────────────────────┘
└──────────┬─────────┘                                  │
           │                                            │
           ▼                                            ▼
┌────────────────────┐   ┌────────────────────┐   ┌────────────────────┐
│ mash_py.zone       │   │ mash_py.usecase    │   │ mash_py.discovery  │
│ Zone state, multi- │   │ Use case defs      │   │ mDNS adv + browse  │
│ zone resolution    │   │ (generated)        │   │ (zeroconf)         │
└──────────┬─────────┘   └─────────┬──────────┘   └──────────┬─────────┘
           ▼                       ▼                          │
┌────────────────────┐   ┌────────────────────┐              │
│ mash_py.features   │◄──┤ mash_py.model      │              │
│ Feature impls      │   │ Endpoint, Feature, │              │
│ (partial gen)      │   │ Attribute (gen)    │              │
└──────────┬─────────┘   └─────────┬──────────┘              │
           │                       │                          │
           └───────────────────────┴──────────────────────────┘
                                   │
                                   ▼
                  ┌────────────────────────────────┐
                  │ mash_py.interaction            │
                  │ Read/Write/Subscribe/Invoke    │
                  │ dispatch, sub manager          │
                  └───────────────┬────────────────┘
                                  │
              ┌───────────────────┴───────────────────┐
              ▼                                        ▼
┌────────────────────────┐                ┌────────────────────────┐
│ mash_py.commissioning  │                │ mash_py.transport      │
│ PASE (SPAKE2+),        │                │ Framer, Connection,    │
│ CSR, cert install,     │                │ TLS contexts, ALPN     │
│ renewal                │                └────────────┬───────────┘
└────────────┬───────────┘                              │
             │                                          │
             ▼                                          ▼
┌────────────────────────┐                ┌────────────────────────┐
│ mash_py.crypto         │                │ mash_py.wire           │
│ Thin facade over       │                │ CBOR codec,            │
│ `cryptography` (P1)    │                │ Request/Response/      │
│ swappable for          │                │ Notification/Control,  │
│ ecdsa+pure-Python (P2) │                │ PeekMessageType        │
└────────────────────────┘                └────────────────────────┘
```

### Key design rules

- **No package depends upward.** `wire` knows nothing about `transport`. `transport` knows nothing about `service`. Tests can exercise each layer in isolation.
- **All wire-touching modules are pure functions** where possible. CBOR codecs in `mash_py.wire` take `bytes` in, return `bytes` out, no I/O.
- **All I/O is `async`**. There is no sync API at the public surface. CLI tools use `asyncio.run`.
- **`mash_py.crypto` is a facade**, not a re-export. Phase 1 implements it on top of `cryptography`; phase 2 swaps in pure-Python primitives without changing call sites.
- **Generated code lives next to handwritten code** in the same module, mirroring Go's `*_gen.go` pattern. Generated files are named `_gen.py` and have a top banner forbidding hand-edits.

### External dependencies (CPython 3.11+)

| Package | Used for | Phase |
|---|---|---|
| `cbor2` | CBOR encode/decode (canonical mode, integer keys) | 1 |
| `cryptography` | TLS, X.509, EC, AES-GCM, HKDF | 1 |
| `zeroconf` | mDNS advertise + browse | 6 |
| `pyyaml` | YAML loading for codegen | 12 |
| `jinja2` | Templates for codegen | 12 |
| `ecdsa` | (Phase 2 only) pure-Python P-256 for CircuitPython compat | 2 stretch |
| `pytest`, `pytest-asyncio` | test runner | all |
| `hypothesis` | property tests for codecs | 1 |
| `ruff`, `mypy` | lint + type-check | 1 |

No transitive heavyweights. No PyOpenSSL. No Twisted.

---

## 4. Repository Layout

```
mash-py/
├── pyproject.toml
├── README.md
├── Makefile                    # generate, test, conformance, lint
├── mash_py/
│   ├── __init__.py
│   ├── wire/                   # CBOR codec + framing primitives
│   │   ├── __init__.py
│   │   ├── codec.py            # encode/decode Request/Response/Notification/Control
│   │   ├── framing.py          # 4-byte BE length prefix
│   │   ├── peek.py             # PeekMessageType-equivalent classifier
│   │   ├── operations.py       # Operation/Status/MessageType enums
│   │   └── payloads.py         # ReadPayload, SubscribePayload, InvokePayload, ErrorPayload
│   ├── transport/              # TLS + connection lifecycle
│   │   ├── __init__.py
│   │   ├── tls.py              # SSLContext factories (operational, commissioning)
│   │   ├── connection.py       # async Connection with read loop
│   │   └── alpn.py             # ALPN constants
│   ├── crypto/                 # facade over cryptography
│   │   ├── __init__.py
│   │   ├── ec.py               # P-256 ops (point add, scalar mult, hash-to-point)
│   │   ├── kdf.py              # HKDF-SHA256
│   │   ├── mac.py              # HMAC-SHA256
│   │   └── certs.py            # X.509 generation + verification + CSR
│   ├── commissioning/
│   │   ├── __init__.py
│   │   ├── spake2plus.py       # algorithm core (lifted from CircuitMatter, adapted)
│   │   ├── messages.py         # PASERequest/Response/Confirm/Complete + CSR/CertInstall
│   │   ├── pase_client.py      # controller-side PASE state machine
│   │   ├── pase_server.py      # device-side PASE state machine
│   │   ├── renewal_messages.py # MsgTypes 30-33
│   │   └── renewal.py          # in-session renewal state machines
│   ├── interaction/
│   │   ├── __init__.py
│   │   ├── client.py           # Read/Write/Subscribe/Invoke client (matches MessageID)
│   │   ├── server.py           # request dispatcher
│   │   └── subscription.py     # subscription manager + dispatcher
│   ├── model/
│   │   ├── __init__.py
│   │   ├── feature.py          # Feature, Attribute, Command base types
│   │   ├── endpoint.py         # Endpoint
│   │   ├── device.py           # Device (collection of endpoints)
│   │   ├── _gen.py             # generated: feature type IDs, endpoint type IDs
│   │   └── shared_gen.py       # generated: shared enums (Phase, Direction, etc.)
│   ├── features/               # feature implementations (mostly generated)
│   │   ├── __init__.py
│   │   ├── device_info_gen.py
│   │   ├── measurement_gen.py
│   │   ├── electrical_gen.py
│   │   ├── energy_control_gen.py
│   │   ├── status_gen.py
│   │   ├── charging_session_gen.py
│   │   ├── tariff_gen.py
│   │   ├── signals_gen.py
│   │   ├── plan_gen.py
│   │   └── test_control.py     # handwritten — implements trigger handlers
│   ├── usecase/
│   │   ├── __init__.py
│   │   └── definitions_gen.py  # generated from docs/usecases/1.0/
│   ├── zone/
│   │   ├── __init__.py
│   │   ├── zone.py             # Zone type, MultiZoneValue, resolution
│   │   └── pool.py             # zone state container
│   ├── discovery/
│   │   ├── __init__.py
│   │   ├── types.py            # service constants, TXT record schemas
│   │   ├── advertiser.py       # async wrapper over zeroconf
│   │   └── browser.py
│   ├── service/
│   │   ├── __init__.py
│   │   ├── device.py           # DeviceService — lifecycle, accept loop
│   │   ├── controller.py       # ControllerService — discovery, commissioning, sessions
│   │   ├── zone_session.py
│   │   ├── device_session.py
│   │   ├── failsafe.py
│   │   └── cert_store.py       # in-memory + file backends
│   ├── testharness/
│   │   ├── __init__.py
│   │   ├── runner.py           # subset of mash-go's runner
│   │   ├── loader.py           # YAML test case loader
│   │   ├── pics.py
│   │   └── handlers/           # handler implementations per action verb
│   └── cli/
│       ├── __init__.py
│       ├── device.py           # `mash-py-device` entry point
│       ├── controller.py       # `mash-py-controller`
│       └── test.py             # `mash-py-test`
├── codegen/                    # NOT under mash_py — runs at build time
│   ├── __init__.py
│   ├── __main__.py             # `python -m codegen`
│   ├── specparse.py
│   ├── helpers.py              # py_type_name, py_access_const, etc.
│   └── templates/
│       ├── feature.py.j2
│       ├── enums.py.j2
│       ├── shared.py.j2
│       └── usecase.py.j2
├── tests/
│   ├── unit/                   # mirrors mash_py/ tree
│   ├── vectors/                # cross-language byte vectors (committed JSON)
│   ├── interop/                # tests that spawn or talk to mash-go binaries
│   └── conformance/            # runs Go mash-test against Python device
└── tools/
    ├── gen_vectors.go          # one-off Go program: dumps test vectors as JSON for Python
    └── run_interop.sh          # orchestrate Python<->Go interop runs
```

This layout sits **alongside `mash-go/`** in the same repo. No code is shared at language boundary; YAML in `docs/features/` is the only shared input.

---

## 5. TDD Methodology

Every phase follows the same five-step ritual:

1. **Write the test first.** Three layers, in this order:
   - **Unit:** pure Python. Encode/decode round-trip, state-machine transitions, error cases.
   - **Vector:** load a JSON file from `tests/vectors/` produced by a Go test, assert the Python code produces identical bytes (or accepts identical bytes).
   - **Interop:** spawn a Go binary (or run against an already-running one) and exchange real frames.
2. **Implement minimal code** to pass the unit + vector tests.
3. **Run the interop test.** This is the "go-it-or-not" gate. If it fails, the failure mode is one of:
   - **Wire mismatch.** Diff the bytes (Python sent vs Go expected). 90% of the time it's a CBOR integer encoding edge case or a missing key. Fix by widening or correcting the codec.
   - **State mismatch.** Read both sides' logs. Usually one side advanced its state machine while the other was still waiting. Fix the state machine.
   - **Spec ambiguity.** The Go behavior isn't what the spec says. File a `decision-log.md` entry; clarify the spec; update both.
4. **Commit.** Each commit message references the phase number and the Go file:line it ports from.
5. **Update the plan.** If the phase revealed something the plan didn't anticipate, edit this document — don't quietly absorb the surprise.

### Test naming convention

```
tests/unit/wire/test_codec.py::TestRequestRoundTrip::test_read_request
tests/vectors/wire/test_request_vectors.py::test_read_request_matches_go
tests/interop/wire/test_against_go.py::test_python_request_decoded_by_go
```

The path tells you the test layer at a glance, which directly maps to which kind of failure to expect.

### Refinement loop on interop failure

When a phase fails interop:

1. **Capture both wire logs.** Go side: pass `-protocol-log` to `mash-test` or use the device's `--protocol-log` flag. Python side: enable the always-on hex dump in `mash_py.wire.codec` (toggleable env var `MASHPY_WIRELOG=1`).
2. **Diff frame by frame.** Same length? Same first byte? CBOR has self-describing prefixes — an `0xA4` is a 4-key map; reading from there usually localises the difference fast.
3. **Write a regression test in `tests/vectors/`** with the bytes from the failing exchange. Now the bug is committed before the fix.
4. **Fix the Python side.** Almost always the Python side; the Go side has been load-tested longer.
5. **Re-run interop.** If it now passes, mark the phase complete. If not, GOTO 1 — there's a second bug.

---

## 6. Cross-Language Test Vector Strategy

This is the load-bearing piece of the plan. Without it, "Python passes its unit tests" doesn't mean "Python interoperates with Go."

### How vectors are produced

A small Go program at `mash-py/tools/gen_vectors.go` is built once per protocol-spec change. It:

1. Imports `mash-go/pkg/wire`, `pkg/commissioning`, etc.
2. Encodes a curated set of canonical messages (one per type, plus edge cases — empty payloads, max-size, all-zero IDs, boundary integer types).
3. Writes each as a JSON file: `{"name": "...", "description": "...", "go_struct_repr": "...", "cbor_hex": "A4..."}`.

Output goes to `mash-py/tests/vectors/`, committed to the repo.

### What vectors cover

| Vector category | Source Go test | Vectors produced |
|---|---|---|
| Wire codec | `pkg/wire/codec_test.go:7–662` | ~30 vectors: each message type, each operation, error responses, NaN/Infinity rejection, duplicate-key rejection |
| Framing | `pkg/transport/framing_test.go:42–327` | ~10 vectors: small/medium/max-size, boundary, truncation |
| Commissioning | `pkg/commissioning/spake2plus_test.go` + a new wire-vector test | ~8 vectors: full PASE transcript with seeded RNG, error codes, CSR/CertInstall messages |
| Cert renewal | `pkg/commissioning/renewal_messages.go` (no test vectors yet — we add them) | ~6 vectors: each MsgType 30-33, with optional fields present/absent, status codes |

For SPAKE2+, vectors include not just messages but **intermediate values** — `w0`, `w1`, `pA`, `pB`, `Z`, `V`, transcript hash, derived keys. This is essential because the protocol's randomness (random `x`, `y`) means we can't compare ephemeral pA/pB across implementations without seeded RNG.

**Implementation note:** the Go `gen_vectors` program will use a seedable `io.Reader` for randomness, expose it via a flag, so each run produces deterministic output. The Python code accepts the same seed for vector tests.

### CI enforcement

The `gen_vectors` Go program runs in CI on the Go side. If protocol changes alter encoding, the JSON vectors change. The Python side's `tests/vectors/` reads them and asserts. **Diverging encodings cannot land — both sides must update together.**

---

## 7. Phased Build Plan

22 phases. Each is small enough to complete in 1–3 days for a single developer. Each phase is independently mergeable. **No phase is "done" until its interop check passes.**

The conventions in each phase:

- **Goal:** what this phase delivers.
- **Depends on:** prior phases (numbers).
- **Files created/modified:** with rough size estimates.
- **Test layers:** unit / vector / interop, with concrete cases.
- **Acceptance:** the criterion that ends the phase.
- **Interop verification:** the live Go-side check.
- **Refinement contingency:** if interop fails, what's the most likely cause and how to bisect.

---

### Phase 0 — Project Scaffolding

**Goal.** A Python package that builds, tests, and lints; CI green on a no-op test.

**Depends on.** Nothing.

**Files created.**

- `mash-py/pyproject.toml` — packaging, deps, ruff config, mypy config
- `mash-py/Makefile` — `test`, `lint`, `generate`, `conformance` targets
- `mash-py/mash_py/__init__.py` — `__version__`
- `mash-py/tests/test_smoke.py` — imports the package
- `mash-py/.github/workflows/python.yml` (or extend existing CI)

**Test layers.**

- Unit: `tests/test_smoke.py::test_import` — `import mash_py` succeeds.

**Acceptance.** `make test lint` clean on a fresh checkout.

**Interop verification.** None — nothing to interop with yet.

**Refinement contingency.** N/A.

---

### Phase 1 — CBOR Wire Codec (Request/Response/Notification/Control)

**Goal.** Encode and decode all four MASH message classes with byte-identical output to Go's `pkg/wire`.

**Depends on.** Phase 0.

**Background from exploration.**
- Go uses `fxamacker/cbor/v2` with `SortCanonical`, `IndefLengthForbidden`, `NaNDecodeForbidden`, `InfDecodeForbidden` (`mash-go/pkg/wire/codec.go:11–48`).
- Field numbers are integer keys; `keyasint` tags enforce numeric encoding.
- See `tests/vectors/` (Phase 6 above) for the 30 canonical vectors.

**Files created/modified.**

- `mash_py/wire/operations.py` — `Operation`, `Status`, `MessageType` enums (~80 lines)
- `mash_py/wire/payloads.py` — `ReadPayload`, `SubscribePayload`, `SubscribeResponsePayload`, `UnsubscribePayload`, `InvokePayload`, `ErrorPayload`, `ControlMessage`, `Notification` dataclasses (~150 lines)
- `mash_py/wire/codec.py` — `encode_request`, `decode_request`, `encode_response`, `decode_response`, `encode_notification`, `decode_notification`, `encode_control`, `decode_control` (~200 lines)
- `mash_py/wire/peek.py` — `peek_message_type(bytes) -> MessageType` (~80 lines, ports `pkg/wire/codec.go:173–272` algorithm)
- `tools/gen_vectors.go` — Go program emitting `tests/vectors/wire/*.json`
- `tests/unit/wire/test_codec.py`, `test_peek.py` — round-trip tests, NaN/Infinity rejection, duplicate-key rejection
- `tests/vectors/wire/test_against_vectors.py` — load JSON vectors, assert encoding matches and decoding produces equivalent objects

**Test layers.**

| Layer | What |
|---|---|
| Unit | Round-trip every message variant. Reject NaN, Infinity, duplicate keys, string keys. Property test: random integer-keyed maps round-trip identically. |
| Vector | All 30 vectors from `gen_vectors`. Both directions: encode-and-compare-bytes, decode-and-compare-fields. |
| Interop | Deferred to Phase 4 — needs transport. |

**Acceptance.** All 30 vector tests pass. `peek_message_type` correctly classifies every vector without false-routing a Notification as a Response (the most subtle case — see Go classification at codec.go:208).

**Interop verification.** Deferred until phase 4 (TLS connection). At that point, send a Python-encoded `ReadRequest` to the Go device and confirm it parses; receive a Go-encoded `ReadResponse` and confirm we parse it.

**Refinement contingency.** If a vector fails:
- Look first at integer encoding. CBOR encodes integers with the shortest possible representation — `cbor2` and `fxamacker/cbor` agree on canonical mode, but check anyway.
- Look at map key sort order. `cbor2` sorts integer keys; Go's `SortCanonical` does too. Verify by encoding `{10: 1, 1: 2}` and reading the bytes — key 1 must come first.
- Look at byte-string vs text-string. CBOR distinguishes them; `bytes` in Python is a byte string, `str` is text. A field that should be `bytes` but ends up as `str` will encode differently.

---

### Phase 2 — Length-Prefixed Framing

**Goal.** Read and write framed messages on a binary stream with the same 4-byte BE length prefix and 8192-byte cap as Go.

**Depends on.** Phase 0.

**Background.**
- 4-byte big-endian length, max 8192 (DEC-069), tested at `pkg/transport/framing_test.go:42–327`.
- Reject empty messages (length=0), oversized messages (length>8192), truncated reads.

**Files created.**

- `mash_py/wire/framing.py` — `FrameWriter`, `FrameReader`, with both sync and async I/O variants (~150 lines)
- `tests/unit/wire/test_framing.py` — round-trip, boundary acceptance/rejection, truncation
- `tests/vectors/wire/test_framing_vectors.py` — same boundary cases as Go's tests

**Test layers.**

| Layer | What |
|---|---|
| Unit | `len=1` accepted, `len=8192` accepted, `len=0` rejected, `len=8193` rejected, partial length prefix rejected, partial payload rejected. Multiple back-to-back frames in one stream. |
| Vector | Frame a known CBOR message; assert the bytes match Go's framing. |
| Interop | Deferred to Phase 4. |

**Acceptance.** All boundary conditions match Go's behavior.

**Interop verification.** Deferred to Phase 4.

**Refinement contingency.** Endianness mistake is the classic — `struct.pack('>I', n)` is big-endian uint32; `'<I'` is little-endian; pick wrong and length 256 looks like length 16777216.

---

### Phase 3 — Crypto Facade & Certificate Helpers

**Goal.** A `mash_py.crypto` module that exposes everything the rest of the stack needs in language-agnostic terms.

**Depends on.** Phase 0.

**Background.** Used by transport (TLS context creation, Zone CA loading), commissioning (SPAKE2+ primitives, CSR generation), renewal (cert verification, sequence numbers).

**Files created.**

- `mash_py/crypto/ec.py` — P-256 point ops (add, scalar-mult, hash-to-point, encode/decode SEC1 compressed/uncompressed)
- `mash_py/crypto/kdf.py` — `hkdf_sha256(ikm, salt, info, length) -> bytes`
- `mash_py/crypto/mac.py` — `hmac_sha256(key, data) -> bytes`
- `mash_py/crypto/certs.py` — generate self-signed cert, generate CSR, sign CSR with CA, verify cert chain, extract `AuthorityKeyId` / `SubjectKeyId`
- `tests/unit/crypto/test_ec.py`, `test_kdf.py`, `test_mac.py`, `test_certs.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | RFC 5869 HKDF test vectors. RFC 4231 HMAC-SHA256 test vectors. P-256 test vectors (NIST). Generate a CSR; sign it; verify it. Round-trip an X.509 cert via DER. |
| Vector | RFC test vectors are the universal vectors — no MASH-specific layer needed yet. |
| Interop | Deferred until commissioning phases. |

**Acceptance.** All RFC test vectors pass.

**Interop verification.** Deferred.

**Refinement contingency.** Hash-to-point on P-256 is non-trivial; if the M and N points (from `mash-go/pkg/commissioning/spake2plus.go:38–49`) don't match RFC 9383, the implementation is wrong.

---

### Phase 4 — TLS Transport (Two-Port Deployment per DEC-082)

**Goal.** Python `Connection` class for clients (TLS 1.3, ALPN-aware) and a two-listener server pattern (commissioning port + operational port, separate SSLContexts). Wire-conformant per DEC-082; no ClientHello parsing or BIO trickery.

**Depends on.** Phases 1, 2, 3.

**Background.**
- Go's `pkg/transport/connection.go:99–509`, `pkg/transport/tls.go:54–176`.
- Two ALPN values: `mash/1` (operational), `mash-comm/1` (commissioning).
- DEC-082 deployment pattern: two listeners on distinct ports; each serves one ALPN exclusively.
- mDNS SRV records (Phase 6, 18) carry the actual port per service.

**Files created.**

- `mash_py/transport/alpn.py` — `ALPN_OPERATIONAL = "mash/1"`, `ALPN_COMMISSIONING = "mash-comm/1"`
- `mash_py/transport/tls.py` — `make_commissioning_context(self_signed_cert)`, `make_operational_context(zone_ca, op_cert, op_key)` factory functions; each `SSLContext` advertises one ALPN exclusively
- `mash_py/transport/connection.py` — `Connection` async class with `read_loop`, `send`, `close`, control-message handling (Ping/Pong/Close)
- `mash_py/service/listeners.py` — `TwoPortListener` managing both `asyncio.Server` instances with a shared connection-cap counter (DEC-064)
- `tests/unit/transport/test_tls.py`, `test_connection.py`, `test_two_port_listener.py`
- `tests/interop/test_phase4_tls.py` — connect Python client to Go device's port (via mDNS SRV), complete TLS 1.3 handshake with `mash-comm/1` ALPN, exchange `ControlMessage{Ping}` ↔ `Pong`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Loopback Python-server↔Python-client TLS handshake completes for both ALPN values; per-listener ALPN exclusivity (commissioning listener rejects `mash/1`, operational rejects `mash-comm/1`); shared cap counter blocks the (max_zones+2)nd connection regardless of which port. |
| Vector | N/A. |
| Interop | **Live test.** Run `mash-go/cmd/mash-device` with a fresh discriminator. Python client discovers its mDNS SRV port (Phase 6), connects, completes TLS 1.3 handshake with `mash-comm/1` ALPN, sends `ControlMessage{Type: Ping, Sequence: 42}`, receives `Pong`. **Then:** start the Python device with two ports. Run `mash-go/cmd/mash-controller --commission --discriminator 5678`. Go controller dials the SRV-advertised commissioning port; TLS handshake completes; PASE proceeds (Phase 8). |

**Acceptance.** Bidirectional interop ping-pong: Python client → Go device (commissioning) AND Go controller → Python device (commissioning).

**Interop verification.**

```bash
# Python-as-client direction
cd mash-go && go run ./cmd/mash-device --commissioning --discriminator 1234 &
cd mash-py && pytest tests/interop/test_phase4_tls.py::test_python_client_connects_to_go_device -v

# Python-as-device direction
cd mash-py && python -m mash_py.cli.device --discriminator 5678 --setup-code 20202021 &
cd mash-go && go run ./cmd/mash-controller commission --discriminator 5678 --setup-code 20202021
```

**Refinement contingency.** Most common failure: TLS handshake itself. Inspect with:
```bash
openssl s_client -connect localhost:8444 -tls1_3 -alpn mash-comm/1 -showcerts
```
If TLS works manually but Python fails, `cryptography` is fine — Python's `ssl` module is stricter about certificate verification. May need to pass `check_hostname=False` and a custom verify callback that matches the Go device's self-signed cert by `CommonName == discriminator`. If the Go controller fails to connect to the Python device's commissioning port, verify the mDNS SRV record advertises the correct port (use `dns-sd -L MASH-5678 _mash-comm._tcp local`).

**Harness coverage caveat.** Once Phase 4 ships, the Python device runs two listeners on different ports. `mash-go/cmd/mash-test` in its current form takes a single `-target host:port` and dials it for both ALPN values — meaning a `mash-comm/1` connection lands on the operational port, fails TLS, and every commissioning conformance case fails noisily. This is a harness gap, not a wire-protocol gap; Phase 22 owns the fix. Until that change lands, conformance against the Python device is run **with the Python device deployed in single-port mode for testing** (or skipped entirely). Document this exception in `tools/run_conformance.sh` when Phase 22 lands.

---

### Phase 5 — `mash_py.wire.peek` Live Verification

**Goal.** Confirm the Phase 1 classifier handles real Go-emitted messages without ambiguity. This is a "promote unit-test confidence to interop confidence" phase.

**Depends on.** Phase 4.

**Files created.**

- `tests/interop/wire/test_peek_against_go.py` — short script that connects to the Go device's commissioning port and watches incoming CBOR; asserts each frame is correctly classified.

**Test layers.**

- Interop only.

**Acceptance.** Run a sequence of Read/Write/Subscribe operations against a Go device, log every frame's classification. Zero misclassifications.

**Interop verification.** Same setup as Phase 4 plus a few synthetic operations.

**Refinement contingency.** If a Notification is misclassified as a Response, the most likely cause is the order of checks in `peek_message_type` — Go's `codec.go:208` checks `field1 == 0` *first*, before any other check. Ensure the Python port preserves order.

---

### Phase 6 — mDNS Discovery (Browser Only)

**Goal.** A Python controller can browse for commissionable Go devices and resolve them to host:port.

**Depends on.** Phase 0.

**Background.**
- Service constants in `mash-go/pkg/discovery/types.go:8–47`.
- Commissioning service: `_mash-comm._tcp.local`, instance name `MASH-<discriminator>`, TXT `D, cat, serial, brand, model, DN`.
- Operational service: `_mash._tcp.local`, instance name `<DI>-<ZI>`, TXT `ZI, DI, VP, FW, EP`.

**Files created.**

- `mash_py/discovery/types.py` — service constants matching Go
- `mash_py/discovery/browser.py` — async wrapper around `zeroconf.asyncio.AsyncServiceBrowser`
- `tests/unit/discovery/test_browser.py` — uses `zeroconf` test helpers
- `tests/interop/discovery/test_browse_go.py` — start a Go device in commissioning mode, browse with Python, find it

**Test layers.**

| Layer | What |
|---|---|
| Unit | A loopback server advertises a fake `_mash-comm._tcp` record; Python browser finds it and parses TXT records correctly. |
| Vector | N/A. |
| Interop | Start `mash-device --commissioning --discriminator 1234`. Python browser finds it within 2 seconds. TXT records match expected values. |

**Acceptance.** Interop browse succeeds.

**Interop verification.** Standard.

**Refinement contingency.** If browse fails, mDNS often fails on developer machines due to firewall or multicast routing on Wi-Fi. Run with `dns-sd -B _mash-comm._tcp` (macOS) or `avahi-browse _mash-comm._tcp` (Linux) to confirm the Go device's record is on the wire.

---

### Phase 7 — SPAKE2+ Algorithm Core

**Goal.** A Python module that, given a setup code and identities, produces the same intermediate values (w0, w1, pA, pB, transcript hash, shared secret, confirmation MAC) as the Go implementation, byte-identically when seeded with the same RNG.

**Depends on.** Phase 3.

**Background.**
- RFC 9383 SPAKE2+ on P-256 with HKDF-SHA256.
- Go constants: `mash-go/pkg/commissioning/spake2plus.go:38–49`.
  - HKDF info for verifier derivation: `"SPAKE2+-P256-SHA256 w"` (line 90, 154)
  - HKDF info for session key derivation: `"SPAKE2+-P256-SHA256"` (line 254, 417)
  - M point: `886e2f97ace46e55ba9dd7242579f2993b64e16ef3dcab95afd497333d8fa12f5ff355163e43ce224e0b0e65ff02ac8e5c7be09419c785e0ca547d55a12e2d20`
  - N point: `d8bbd6c639c62937b04d997f38c3770719c629d7014d49a24b4f98baa1292b4907d60aa6bfade45008a636337f5168c64d9bd36034808cd564490b1e656edbe7`
- CircuitMatter's `pase.py` provides the algorithmic skeleton; we re-implement using `cryptography` instead of `ecdsa` for performance.

**Files created.**

- `mash_py/commissioning/spake2plus.py` — `Spake2Plus` class with:
  - `derive_w0_w1(setup_code, salt) -> (bytes, bytes)`
  - `client_init(w0, w1) -> (x, pA)` — controller side
  - `server_init(w0, L) -> (y, pB)` — device side
  - `compute_session_keys(transcript) -> (shared_secret, confirmation_key)`
  - `compute_confirmation_mac(key, public_value) -> bytes`
- `tests/unit/commissioning/test_spake2plus.py`
- `tests/vectors/commissioning/test_spake2plus_vectors.py` — load vectors from `tools/gen_vectors`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Round-trip handshake with both parties in Python; final shared secret matches between client and server. RFC 9383 test vectors (Section A) for known fixed inputs. |
| Vector | Use seeded RNG. Setup code `20202021`, identities `"controller"` and `"device"`, seed `0x42`. Assert: w0, w1, pA, pB, Z, V, transcript hash, shared secret, confirmation MAC all match Go. |
| Interop | Deferred to Phase 8 (full PASE message exchange). |

**Acceptance.** Vectors match.

**Interop verification.** Deferred.

**Refinement contingency.** SPAKE2+ has many subtle places to deviate:
- **Encoding of points.** P-256 points must be encoded as 65-byte uncompressed (`0x04 || X || Y`) for transcript inclusion. Go does this in `spake2plus.go:242–251`.
- **Concatenation order in transcript.** Per RFC 9383: `clientIdentity || serverIdentity || pA || pB || Z || V || w0`. Get any wrong, every secret diverges.
- **HKDF salt vs info.** Go uses `clientID || serverID` as salt for the verifier derivation, info as the version string. Don't swap them.

---

### Phase 8 — PASE Message Exchange (Client Side)

**Goal.** A Python controller can drive a complete PASE handshake with a Go device.

**Depends on.** Phases 4, 7.

**Background.**
- Message types 1–4 in `mash-go/pkg/commissioning/messages.go:8–37`.
- Sequence: PASERequest (C→D, MsgType 1) → PASEResponse (D→C, 2) → PASEConfirm (C→D, 3) → PASEComplete (D→C, 4).

**Files created.**

- `mash_py/commissioning/messages.py` — dataclasses + CBOR codecs for MsgTypes 1–4
- `mash_py/commissioning/pase_client.py` — async state machine
- `tests/unit/commissioning/test_pase_messages.py`
- `tests/interop/commissioning/test_pase_against_go_device.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Encode/decode each message; round-trip via codec. State machine: send Request → expect Response → send Confirm → expect Complete. Error handling: ErrorCode != 0 in Complete aborts cleanly. |
| Vector | Load Go-emitted PASE message bytes; decode; assert fields. |
| Interop | **Live PASE.** Start `mash-device --commissioning --discriminator 1234 --setup-code 20202021`. Python: discover via mDNS (Phase 6), connect TLS (Phase 4), drive PASE to completion. Assert shared secret length is 32 bytes. |

**Acceptance.** PASE completes with a Go device. Both sides log "PASE complete."

**Interop verification.**

```bash
cd mash-go && go run ./cmd/mash-device --commissioning --discriminator 1234 --setup-code 20202021 &
cd mash-py && pytest tests/interop/commissioning/test_pase_against_go_device.py -v
```

**Refinement contingency.** If PASE reaches confirmation but Complete has ErrorCode 1 (AuthFailed), the confirmation MAC computation differs. Bisect by:
1. Capture `pA` and `pB` bytes from both sides.
2. Compute `Z` and `V` on both sides; compare.
3. Compute transcript hash on both sides; compare.
4. The first divergence localises the bug.

---

### Phase 9 — CSR & Certificate Install

**Goal.** Python controller can issue a Zone CA-signed operational cert to a Go device after PASE.

**Depends on.** Phases 3, 8.

**Background.**
- MsgTypes 10 (CSRRequest, C→D), 11 (CSRResponse, D→C), 12 (CertInstall, C→D), 13 (CertInstallResponse, D→C).
- Cert exchange happens immediately after PASE on the *same* commissioning TLS connection (`mash-go/pkg/service/device_service_commissioning.go:471`).

**Files created.**

- `mash_py/commissioning/messages.py` — extend with CSR/CertInstall messages
- `mash_py/commissioning/cert_exchange.py` — controller-side flow
- `mash_py/commissioning/cert_store.py` (controller side: Zone CA private key)
- `tests/unit/commissioning/test_cert_exchange.py`
- `tests/interop/commissioning/test_full_commissioning_against_go.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Generate a Zone CA; sign a self-generated CSR; verify the resulting cert chain. CertInstallResponse with non-zero ErrorCode aborts cleanly. |
| Vector | Decode Go-emitted CSRRequest/Response/CertInstall messages. |
| Interop | **End-to-end commissioning.** Python controller commissions a Go device. After Complete, controller sends CSRRequest with random nonce; device returns CSR; controller signs with Zone CA; device installs cert; sends CertInstallResponse{Status: 0}; sends CommissioningComplete (MsgType 20). Device's cert store now contains the Zone CA. |

**Acceptance.** Go device transitions to operational state and starts advertising `_mash._tcp` with the new ZoneID.

**Interop verification.**

```bash
cd mash-go && go run ./cmd/mash-device --commissioning --discriminator 1234 --setup-code 20202021 &
cd mash-py && python -m mash_py.cli.controller commission --discriminator 1234 --setup-code 20202021
# Then verify mDNS:
dns-sd -B _mash._tcp
# Should see: <DI>-<ZI>
```

**Refinement contingency.** If CSR signing fails, the most common cause is X.509 extension mismatch. The device expects:
- Subject CN: device's ID
- AuthorityKeyId: matches Zone CA's SubjectKeyId
- KeyUsage: `digitalSignature, keyAgreement`
- ExtKeyUsage: `clientAuth, serverAuth`
Check `mash-go/pkg/cert/generate.go` for the exact extension set.

---

### Phase 10 — Operational Connection (Post-Commissioning)

**Goal.** After commissioning, Python controller reconnects to the Go device on the operational port with mTLS using the issued cert, and exchanges a successful Read on DeviceInfo.

**Depends on.** Phases 4, 9.

**Background.**
- The commissioning TLS connection closes after CertInstallResponse (`device_service_commissioning.go:626`).
- A new TLS connection is opened with `mash/1` ALPN, presenting the operational cert. The device verifies the cert against its stored Zone CA.

**Files created.**

- `mash_py/transport/tls.py` — extend with operational context factory
- `mash_py/service/controller.py` — `Commission` + `Reconnect operational` flow
- `tests/interop/transport/test_operational_connection.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Operational SSLContext loads cert + key + CA correctly. |
| Vector | N/A. |
| Interop | Commission the Go device; immediately reconnect on the operational port; send `Request{MessageID: 1, Operation: Read, EndpointID: 0, FeatureID: 0x01}` (DeviceInfo); receive `Response{MessageID: 1, Status: Success, Payload: {... DeviceInfo attributes ...}}`. |

**Acceptance.** Read succeeds; payload contains expected vendor/product/serial fields.

**Interop verification.** As above.

**Refinement contingency.** If mTLS handshake fails, Python is verifying the device's cert against system CAs. Pass the Zone CA to the SSLContext via `load_verify_locations` and disable hostname check — the device's cert CN is its DeviceID, not a hostname.

---

### Phase 11 — Interaction Model Client (Read/Write/Subscribe/Invoke)

**Goal.** Full request/response correlation for the controller side, with subscription notification dispatch.

**Depends on.** Phases 1, 2, 10.

**Background.**
- Go's `pkg/interaction/` package handles MessageID matching for pending calls.
- Notifications (MessageID = 0) are routed by SubscriptionID to registered handlers.

**Files created.**

- `mash_py/interaction/client.py` — `InteractionClient` with `read`, `write`, `subscribe`, `invoke` methods returning awaitables
- `tests/unit/interaction/test_client.py`
- `tests/interop/interaction/test_against_go_device.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Pending call map: send Request, receive Response, future resolves. Timeout: pending call with no Response after deadline raises. Notification routing: register subscription handler; receive Notification with matching ID; handler called. |
| Vector | N/A. |
| Interop | Read DeviceInfo; Write DiscriminatorRevocation flag; Subscribe to Status.OperatingState; Invoke `RemoveZone`; verify all responses come back correctly correlated. |

**Acceptance.** All four operations work against a Go device with various endpoint/feature combinations.

**Interop verification.** Standard.

**Refinement contingency.** If Notifications are dropped, check that the Python side is *reading* continuously — Go sends notifications proactively, not in response to a request. The read loop must be running concurrently with the request/response flow.

---

### Phase 12 — Code Generation (Python from YAML)

**Goal.** A `codegen` tool that reads `docs/features/*.yaml` and emits Python files in `mash_py/features/` and `mash_py/model/`.

**Depends on.** Phase 0 (and YAML discipline; not on prior runtime phases).

**Background.**
- YAML schema in `docs/features/<feature>/1.0.yaml` (see exploration report Phase 4 part 1).
- Go generator templates: `mash-go/cmd/mash-featgen/templates.go`.
- Determinism: iterate `ver.FeatureTypes` slice (not `featureVersions` map). Same discipline applies in Python.
- Per Decision C: separate generator, Jinja2.

**Files created.**

- `codegen/__main__.py` — CLI: `python -m codegen --features-dir docs/features --output mash_py/`
- `codegen/specparse.py` — YAML loaders for `features/*.yaml`, `protocol-versions.yaml`, `_shared/1.0.yaml`, `usecases/1.0/*.yaml`
- `codegen/helpers.py` — `py_type_name`, `py_enum_const`, `py_attribute_id`, etc.
- `codegen/templates/feature.py.j2`, `enums.py.j2`, `shared.py.j2`, `usecase.py.j2`
- `mash_py/features/*_gen.py` (output, committed)
- `mash_py/model/_gen.py`, `mash_py/model/shared_gen.py` (output, committed)
- `mash_py/usecase/definitions_gen.py` (output, committed)
- `tests/unit/codegen/test_specparse.py`, `test_generate.py`
- A CI job that runs `python -m codegen` and fails if the diff is non-empty (catches drift).

**Test layers.**

| Layer | What |
|---|---|
| Unit | Parse a feature YAML; emit Python; the result imports cleanly. Property test: every attribute in YAML produces an attribute constant + getter+setter pair in the generated module. |
| Vector | Generate `measurement_gen.py`; assert it contains `MEASUREMENT_ATTR_AC_ACTIVE_POWER = 1`, `class Measurement(Feature):`, etc. |
| Interop | N/A — codegen is a build-time tool. |

**Acceptance.** Generated Python imports cleanly, has stable output (running twice produces identical files), and matches Go's symbol coverage (every attribute in `measurement_gen.go` has a counterpart in `measurement_gen.py`).

**Interop verification.** N/A directly. Indirect: Phase 13 uses generated code for live Read/Write tests.

**Refinement contingency.** If generation is non-deterministic, the cause is iterating dict-keyed structures without a sort. Mirror Go's lesson — iterate the *list of features* declared in `protocol-versions.yaml`, not the dict that maps name→version.

---

### Phase 13 — Device Model (Endpoints, Features, Attributes)

**Goal.** A Python device can hold an endpoint hierarchy with features and attributes, with values readable and writable through the in-memory API.

**Depends on.** Phase 12.

**Files created.**

- `mash_py/model/feature.py` — `Feature`, `Attribute`, `Command` base classes
- `mash_py/model/endpoint.py` — `Endpoint`
- `mash_py/model/device.py` — `Device`
- Generated files used as-is.
- `tests/unit/model/test_feature.py`, `test_device.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Construct a Device with Endpoint 0 (DeviceInfo) and Endpoint 1 (EVCharger w/ Measurement, EnergyControl, Status). Read attributes in-memory; write attributes; observers fire. |
| Vector | N/A. |
| Interop | Deferred to Phase 14. |

**Acceptance.** All generated feature classes instantiate correctly. In-memory R/W works.

**Interop verification.** Deferred.

**Refinement contingency.** If a generated feature crashes on instantiation, the codegen template is wrong — fix in Jinja2, regenerate, retest.

---

### Phase 14 — PASE Server (Device Side)

**Goal.** A Python device can receive PASE handshake from a Go controller and complete it.

**Depends on.** Phases 4, 7, 13.

**Files created.**

- `mash_py/commissioning/pase_server.py` — async state machine
- `tests/interop/commissioning/test_pase_server_with_go_controller.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Round-trip Python client + Python server (loopback); shared secret matches. |
| Vector | Replay Go-emitted PASE messages; verify Python server response is byte-identical to what Go server would send (using the seeded RNG). |
| Interop | **Reverse PASE.** Start Python device on port 8444 with discriminator 5678 and setup code 20202021. Run `mash-go/cmd/mash-controller --commission --discriminator 5678 --setup-code 20202021`. PASE completes. |

**Acceptance.** Bidirectional commissioning works (Python↔Go in both directions).

**Interop verification.** As above.

**Refinement contingency.** Same SPAKE2+ debug technique as Phase 8.

---

### Phase 15 — Operational Server (Device Side)

**Goal.** Python device responds correctly to Read/Write/Subscribe/Invoke from a Go controller after commissioning.

**Depends on.** Phases 11, 13, 14.

**Files created.**

- `mash_py/interaction/server.py` — request dispatcher: routes incoming Request to the appropriate Endpoint/Feature handler; encodes Response.
- `mash_py/service/device.py` — extend with operational accept loop.
- `tests/interop/test_full_device_lifecycle.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Dispatcher routes Read/Write/Invoke to the right Feature based on EndpointID + FeatureID; returns appropriate Status for invalid endpoint/feature/attribute/command. |
| Vector | N/A. |
| Interop | Commission Python device with Go controller; controller reads DeviceInfo; controller writes a writable attribute; controller invokes a command; all succeed. |

**Acceptance.** Full bidirectional lifecycle works.

**Interop verification.** Standard.

**Refinement contingency.** Status code mismatches are common — Go uses specific codes (1 InvalidEndpoint, 2 InvalidFeature, 3 InvalidAttribute, 4 InvalidCommand). Get them right per `pkg/wire/status.go`.

---

### Phase 16 — Subscriptions & Notifications

**Goal.** Python device emits notifications when subscribed attributes change. Python controller receives and routes them.

**Depends on.** Phase 15.

**Files created.**

- `mash_py/interaction/subscription.py` — `SubscriptionManager` (per device), `NotificationDispatcher` (per zone session)
- Per Go's `pkg/service/dispatch/notification_dispatcher.go:82–89`: a 100ms polling loop checking the model for changed attributes, batching Notifications.
- `tests/interop/interaction/test_subscriptions.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Subscribe to attributes; mutate them; expect notifications within MaxInterval. Coalescing: rapid changes within MinInterval coalesce to one notification. |
| Vector | N/A. |
| Interop | Go controller subscribes to Status.OperatingState on Python device. Python device transitions OperatingState. Go controller receives Notification. |

**Acceptance.** Notifications flow in both directions (Python device → Go controller, Go device → Python controller from Phase 11).

**Interop verification.** Standard.

**Refinement contingency.** Watch for the "write-before-notify" ordering rule (DEC-071): when handling a Write, the device must dispatch notifications *before* sending the Write response. Implement as: write → notify subscribers synchronously → send response. Otherwise the controller can race and receive the response before the notification, which violates ordering invariants.

---

### Phase 17 — TestControl Feature & Trigger Handlers

**Goal.** Python device implements TestControl (feature 0x0A on endpoint 0) so that `mash-test`'s trigger commands work.

**Depends on.** Phase 15.

**Background.**
- TestControl spec in `mash-go/pkg/features/test_control.go` (handwritten — *not* generated).
- Trigger opcodes in the same file: domain (16 bits) + 16 bits zero + 16 bits zero + trigger (16 bits).
- enableKey: 128-bit hex string default `00112233445566778899aabbccddeeff`.

**Files created.**

- `mash_py/features/test_control.py` — handwritten implementation
- `tests/interop/test_trigger_test_event.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | TriggerTestEvent invocation with correct/incorrect enableKey returns success/error. Each opcode fires the right effect (e.g., `TriggerEnterCommissioningMode` opens commissioning window). |
| Vector | N/A. |
| Interop | Go controller invokes `TriggerTestEvent(enableKey, opcode=TriggerEnterCommissioning)` on Python device. Python device opens commissioning window. Go controller verifies via mDNS that `_mash-comm._tcp` is now advertised. |

**Acceptance.** All opcodes the test suite uses (per the trigger table in `pkg/features/test_control.go:9–66`) work.

**Interop verification.** Standard.

**Refinement contingency.** If a trigger has no observable effect, the device's state machine is wired wrong — fixing the trigger handler should be a one-line change in most cases.

---

### Phase 18 — mDNS Advertiser

**Goal.** Python device advertises commissioning + operational mDNS records correctly.

**Depends on.** Phase 6 (browser uses same primitives).

**Files created.**

- `mash_py/discovery/advertiser.py`
- `tests/interop/discovery/test_advertise.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Advertiser publishes a record; loopback browser finds it; TXT records parse correctly. |
| Vector | N/A. |
| Interop | Python device advertises commissioning record; Go controller browses and finds it. After commissioning, Python device transitions to operational record; Go controller finds the new record. |

**Acceptance.** Both directions of mDNS interop work (Python device discovered by Go controller, Go device discovered by Python controller).

**Interop verification.** Standard.

**Refinement contingency.** mDNS instance-name format is `MASH-<discriminator>` (commissioning) or `<DI>-<ZI>` (operational). If mDNS browse finds the record but the harness can't parse the instance name, check the format.

---

### Phase 19 — Certificate Renewal (MsgTypes 30–33)

**Goal.** Both directions of in-session cert renewal work.

**Depends on.** Phases 9, 11, 15.

**Background.**
- MsgTypes 30 (CertRenewalRequest, C→D), 31 (CertRenewalCSR, D→C), 32 (CertRenewalInstall, C→D), 33 (CertRenewalAck, D→C).
- Nonce binding (DEC-047): device returns SHA256(nonce)[0:16] in the CSR message.
- 7-day renewal window (DEC-077).

**Files created.**

- `mash_py/commissioning/renewal_messages.py`
- `mash_py/commissioning/renewal.py` — controller-side and device-side state machines
- `tests/interop/commissioning/test_cert_renewal.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Round-trip every MsgType. Nonce hash validation. Sequence number monotonicity. |
| Vector | N/A — vectors generated in Phase 6. |
| Interop | Commission Python↔Go in either direction; controller initiates renewal; device returns CSR with nonce hash; controller signs and installs; device acks. After renewal, operational connection survives (no TLS reconnection per DEC-076). |

**Acceptance.** Renewal completes both directions.

**Interop verification.** Standard.

**Refinement contingency.** Watch the nonce hash format: SHA256 of nonce, *truncated to 16 bytes*, not full 32. Ref: `mash-go/pkg/commissioning/renewal_messages.go:14–36`.

---

### Phase 20 — Failsafe & Multi-Zone Resolution

**Goal.** Python device supports multiple zones (max 1 GRID + 1 LOCAL + 1 TEST per DEC-043), with limit/setpoint resolution, failsafe gating, and **per-zone TLS server cert selection**.

**Depends on.** Phase 15, Phase 4 (two-port listeners).

**Background.**
- Zone resolution rules: `mash-go/pkg/zone/zone.go:135–232`.
- Failsafe gate: DEC-073.
- **Per-zone server cert selection.** A device commissioned to multiple zones holds one operational cert per Zone CA. When a controller from Zone A reconnects, the device must present Zone A's cert. Go uses `tls.Config.GetCertificate` driven by SNI (`mash-go/pkg/service/device_service_lifecycle.go:242–255`); controllers send the device ID as SNI; the device looks up the cert by SNI match. Post-handshake, the device identifies the zone cryptographically by matching the peer cert's `AuthorityKeyId` against each Zone CA's `SubjectKeyId` (`device_service_zone.go:34–52`).

**Python equivalent.** `SSLContext.sni_callback(ssl_socket, server_name, ssl_context)` fires after SNI is parsed; we swap `ssl_socket.context` to a per-zone `SSLContext` whose cert chain matches the requested device ID. This is stdlib-supported; no BIO trickery needed.

**Files created.**

- `mash_py/zone/zone.py` — `MultiZoneValue`, `ResolveLimits`, `ResolveSetpoints`
- `mash_py/zone/pool.py` — zone state container
- `mash_py/service/failsafe.py` — failsafe timer per zone
- `mash_py/transport/tls.py` — extend `make_operational_context` to accept a per-zone cert dict and register an `sni_callback` for per-zone cert selection (controller sends device ID as SNI; device looks up the matching zone's operational cert)
- `mash_py/service/device.py` — extend dispatch path to identify zone by `AuthorityKeyId` post-handshake (mirrors `device_service_zone.go:34–52`)
- `tests/interop/test_multi_zone.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Most-restrictive limit resolution: GRID limit 1000W, LOCAL limit 500W → effective 500W. Highest-priority setpoint: GRID setpoint 0W, LOCAL 1000W → effective 0W. TEST zone observer-only. |
| Vector | N/A. |
| Interop | Commission Python device with two Go controllers (GRID and LOCAL). Both write limits. Python device's effective limit matches resolution rules. |

**Acceptance.** Resolution matches Go behavior.

**Interop verification.** Requires running two Go controllers; coordinate via different setup-code/discriminator pairs.

**Refinement contingency.** Sign convention is the trap (consumption +, production -). Watch limit signs.

---

### Phase 21 — Test Harness (Python Runner Subset)

**Goal.** A Python `mash-py-test` CLI that loads YAML test cases from `mash-go/testdata/cases/`, drives a Go device, and reports pass/fail. Ports the *minimum useful subset* of `mash-go/internal/testharness/runner/`.

**Depends on.** Phases 6, 11, 18.

**Files created.**

- `mash_py/testharness/loader.py` — port of YAML schema parsing
- `mash_py/testharness/runner.py` — subset: discover, commission, run steps, teardown
- `mash_py/testharness/handlers/` — handler implementations for `connect`, `commission`, `read`, `write`, `invoke`, `subscribe`, `wait_notification`, `trigger_test_event`, `verify_commissioning_state`, `compare`, `evaluate`
- `mash_py/testharness/pics.py`
- `mash_py/cli/test.py`

**Test layers.**

| Layer | What |
|---|---|
| Unit | Each handler implementation tested in isolation against a mock device. |
| Vector | N/A. |
| Interop | Run a small subset of `mash-go/testdata/cases/commissioning-*.yaml` against a Go device using `mash-py-test`. Compare results to running `mash-go/cmd/mash-test` against the same device. |

**Acceptance.** At least 5 test cases pass identically under Go and Python harnesses.

**Interop verification.** Standard.

**Refinement contingency.** Handler implementations may diverge in subtle ways from Go's runner. Compare wire logs side-by-side.

---

### Phase 22 — Conformance Gate (mash-test against Python device)

**Goal.** The Go `mash-test` binary, run against a Python device, passes a target subset of conformance tests.

**Depends on.** Phases 14–20.

**Prerequisite (mash-go harness change — DEC-082 compliance).**

Before Phase 22 acceptance can be reached, `mash-go/cmd/mash-test` must become deployment-shape-agnostic. Today the runner takes a single `-target host:port` and dials it for both ALPN values (`internal/testharness/runner/runner.go:825-831`). This works against a single-port device (mash-go) by accident — both ALPNs share the same TCP port — but bypasses DEC-082's "mDNS SRV port is authoritative" contract. Against a two-port device (mash-py from Phase 4 onward), commissioning connections would dial the operational port and every commissioning conformance case would fail.

The fix is a small mash-go PR landed *as part of Phase 22's first commit*:

- Add `-srv-resolve` flag (or a `--target mdns:<instance-name>` form) that opts the runner into per-ALPN mDNS SRV resolution.
- When opted in, the runner browses `_mash._tcp` for `mash/1` connections and `_mash-comm._tcp` for `mash-comm/1` connections, then dials the SRV-advertised host:port per service.
- Default behaviour stays as today (`-target host:port` for both ALPNs) so existing CI invocations don't break.
- Phase 22's `tools/run_conformance.sh` invokes mash-test with the SRV-resolve flag enabled.

This change covers single-port and two-port deployments with one harness path, validates DEC-082's discovery contract end-to-end, and creates no per-implementation harness branching. mash-go's own conformance run can also flip on the flag once mDNS reliability on the CI environment is acceptable; that flip is out of scope for this phase.

**Target subset (defined explicitly to set scope):**
- All `commissioning-*.yaml` cases
- All `base-protocol-*.yaml` cases
- All `interaction-*.yaml` cases (Read/Write/Subscribe/Invoke)
- All cases tagged `mvp` or untagged (default)

**Excluded (Phase 22 scope):**
- `env:multi-device` cases (require multi-device orchestration)
- Any `slow` cases (CI gate would balloon)

**Files created (mash-py).**

- `tests/conformance/test_against_go_runner.py` — pytest wrapper that invokes `mash-go/cmd/mash-test` against a freshly started Python device
- `tools/run_conformance.sh`

**Files modified (mash-go).**

- `internal/testharness/runner/runner.go` — add `-srv-resolve` flag and per-ALPN SRV resolution path (~80 LOC)
- `cmd/mash-test/main.go` — wire the flag through
- `internal/testharness/runner/runner_test.go` — unit-test the SRV-resolve path against a mock browser

**Test layers.**

- Conformance only.

**Acceptance.** Go's `mash-test --target mdns:MASH-1234 --srv-resolve` passes the target subset against a Python device deployed in two-port mode.

**Interop verification.**

```bash
# Terminal 1: Python device in two-port mode (operational 8443, commissioning 8444)
cd mash-py && python -m mash_py.cli.device --discriminator 1234

# Terminal 2
cd mash-go && go run ./cmd/mash-test \
  --target mdns:MASH-1234 --srv-resolve \
  --setup-code 20202021 --filter "TC-COMM-*,TC-IM-*"
```

**Refinement contingency.** Each failing test points to a specific protocol gap. Address by re-opening the relevant phase. Don't add ad-hoc patches in Phase 22 — fix at the layer that owns the gap.

If `-srv-resolve` itself misbehaves (mDNS browse times out, wrong port resolved), the failure surface is mash-go's harness, not mash-py's protocol. Reproduce with `mash-go/cmd/mash-test` against a fresh `mash-go` device first to isolate; mash-go is single-port, so SRV resolution must yield the same port for both ALPNs and any divergence is a harness bug.

---

## 8. Conformance Gate

**The conformance criterion is binary and lives at `mash-go`'s mash-test.** A Python implementation that doesn't pass `mash-test` is not done, regardless of how green its own unit suite is.

**Three conformance tiers:**

| Tier | Definition | Phase complete when |
|---|---|---|
| **MVP** | Single-zone commissioning + Read/Write on DeviceInfo | Phase 17 done |
| **Standard** | All non-multi-device, non-slow cases pass | Phase 22 done |
| **Full** | Including multi-device and slow cases | Stretch beyond this plan |

The plan covers MVP and Standard. Full is explicitly out of scope until Standard is rock-solid.

---

## 9. CI / Tooling

A new GitHub Actions workflow at `.github/workflows/python.yml`:

```yaml
jobs:
  unit:
    # Just Python unit tests on each push.
  vectors:
    needs: [generate-vectors]  # Go side regenerates JSON vectors
    # Python loads vectors and asserts.
  interop:
    needs: [unit]
    # Build mash-go binaries; run Python tests that spawn them.
  conformance:
    needs: [interop]
    # Phase 22 — full mash-test run against Python device.
    timeout-minutes: 15
```

`generate-vectors` runs the Go-side `tools/gen_vectors.go` and commits any changes to `tests/vectors/` via a bot — drift becomes visible in PRs.

`make conformance` is the developer-side equivalent for local runs.

---

## 10. Risks & Mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Two-port deployment violates a Go-spec convention | None (resolved by DEC-082) | — | Was a concern under DEC-067-only; DEC-082 makes two-port first-class conformant. |
| Shared connection-cap counter (DEC-064) bug between two listeners | Low | Medium | Phase 4 unit tests assert (max_zones+2)nd connection rejected regardless of which port it lands on; covers race with `asyncio.Lock` around the counter. |
| SPAKE2+ port has subtle deviation that only surfaces under specific seeds | Medium | High | Phase 7 vector tests use multiple seeds; vectors include all intermediate values, not just final secret |
| Codegen drift between Go and Python emitters | Low (with CI gate) | High | CI job that diffs spec coverage |
| `mash-test` discovery via mDNS fails in CI environment | Medium | Medium | Allow `--target host:port` bypass for CI; default to mDNS for dev |
| `cryptography` package can't be installed in some target environments | Low | High | Phase-2 swap to pure-Python primitives (Decision B) is pre-planned |
| Asyncio race conditions in the read loop | Medium | Medium | Mirror Go's lock discipline (capture references under lock; release before slow operations) — see CLAUDE.md project memory on DeviceService lock discipline |
| Python performance on ESP32 (CircuitPython) prohibits real workloads | Low (Phase 1) | Low (Phase 2 stretch) | Defer; benchmark when phase 2 begins |

---

## 11. Open Questions for the User

These need answers before phase 1 starts:

1. **Decision A (ALPN routing).** ✅ Resolved by DEC-082 (2026-04-25): mash-py uses two-port deployment per the new spec-conformant pattern.
2. **Decision B (CircuitPython).** Confirm "phase 1 uses `cryptography`, phase 2 swaps for CircuitPython" approach?
3. **Decision C (codegen).** Confirm separate Python+Jinja2 generator (rejecting the `--lang python` flag idea)?
4. **Decision D (asyncio).** Any objection to asyncio (vs. threads, trio)?
5. **Decision E (SPAKE2+ source).** Confirm CircuitMatter-as-reference (MIT, attribution preserved)?
6. **Scope of phase 22.** Is the "Standard" tier (all non-multi-device, non-slow) the right MVP target, or should phase 22 aim narrower (only commissioning + IM)?
7. **Repository discipline.** New `mash-py/` peer to `mash-go/` in the same repo? Or a separate `mash-python` repo? The plan assumes the former.
8. **Performance bar.** Is there a target throughput (e.g., requests/sec) the Python implementation must hit, or is "works correctly" enough for now?

---

## Approval Trail

This plan is the contract. Once approved:

- Each phase is implemented top-to-bottom with TDD discipline.
- Each phase ends with the documented interop verification before the next begins.
- Plan changes during implementation are made by editing this file in the same PR as the implementation, not by silently absorbing surprises.

---

*End of plan.*
