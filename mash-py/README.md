# mash-py

Python reference implementation of the [MASH protocol](../docs/), peer to [mash-go](../mash-go/).

The Go implementation is canonical; this package agrees with it byte-for-byte on
the wire. See [`mash-py-tdd-plan.md`](../mash-py-tdd-plan.md) for the phased build
plan, architectural decisions, and conformance gate.

## Status

Phase 0 — scaffolding only. No protocol code yet.

## Quick start

```sh
make install     # create venv and install dev deps
make test        # run unit tests
make lint        # ruff check
make typecheck   # mypy
```

## Layout

See the plan document. Briefly:

- `mash_py/wire/`         — CBOR codec and length-prefixed framing
- `mash_py/transport/`    — TLS 1.3, ClientHello dispatcher, async Connection
- `mash_py/crypto/`       — facade over `cryptography` (P-256, HKDF, HMAC, X.509)
- `mash_py/commissioning/`— PASE (SPAKE2+), CSR/cert install, in-session renewal
- `mash_py/interaction/`  — Read/Write/Subscribe/Invoke client + server
- `mash_py/model/`        — Device/Endpoint/Feature (partial codegen)
- `mash_py/features/`     — feature implementations (mostly codegen)
- `mash_py/discovery/`    — mDNS advertise + browse via `zeroconf`
- `mash_py/service/`      — Device/Controller orchestration
- `mash_py/testharness/`  — subset of mash-go's conformance runner
- `codegen/`              — Python+Jinja2 generator reading `../docs/features/*.yaml`

## Design decisions

- **Single port (8443) with ClientHello dispatcher.** Aligned with DEC-067. See plan §2 Decision A.
- **`cryptography` for Phase 1; CircuitPython compat as Phase 2 stretch.** See plan §2 Decision B.
- **Separate Python+Jinja2 codegen** (not a `--lang python` flag on mash-featgen). See plan §2 Decision C.
- **asyncio throughout.** See plan §2 Decision D.
- **SPAKE2+ algorithm core lifted from Adafruit CircuitMatter** (MIT, RFC 9383). See plan §2 Decision E.
