#!/usr/bin/env python3
"""Generate mash-go/testdata/spake2plus_vectors.json from the DEC-084 oracle.

Run from repo root:
    python3 tools/spake2plus_oracle/gen_vectors.py

The output is checked in. Regenerate only when DEC-084 changes; the file is
the spec-of-record for SPAKE2+ algorithmic conformance and a 7C/7D refactor
must NOT regenerate it under the assumption that "the oracle changed too" --
that loop would silently track the implementation. If a vector changes, the
DEC must change in the same commit.

Schema (version 1.0):
    {
      "schema_version": "1.0",
      "spec": "DEC-084",
      "vectors": [
        {
          "id": "<vector name from Phase 7B table>",
          "description": "<one-line human readable>",
          "inputs": { ... hex/string/int fields ... },
          "outputs": { ... hex/string/int fields ... } | "expected_error": "<reason>"
        },
        ...
      ]
    }

Fields are hex-encoded bytes (no 0x prefix), strings, or ints. Big-endian where
the algorithm specifies big-endian; little-endian length prefixes inside TT are
visible only in the transcript hex. The 65-byte SEC1 uncompressed encoding is
identifiable by the leading 04.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

# Allow running as a script from anywhere by prepending the script directory
# to sys.path. The oracle module deliberately doesn't ship a setup.py.
sys.path.insert(0, str(Path(__file__).resolve().parent))

import oracle  # noqa: E402

# Output goes here, relative to the repo root.
OUTPUT = Path(__file__).resolve().parents[2] / "mash-go" / "testdata" / "spake2plus_vectors.json"

# Canonical inputs used across many vectors. These aren't secret; they're
# selected to be easy to spot in hex dumps (0x42, 0x84) and to match Matter's
# canonical setup code 20202021 used in test data across the ecosystem.
SETUP_CODE = "20202021"
DISCRIMINATOR = 3840
SALT_ZEROS = b"\x00" * 16   # the Phase 7B mhf_basic vector salt
ITERATIONS = 100_000
X_FIXED = b"\x42" * 32       # 0x42... fixed scalar, per Phase 7B table
Y_FIXED = b"\x84" * 32       # 0x84...

POINT_INFINITY_ENCODED = b"\x04" + b"\x00" * 64

# An off-curve point: take a valid x but tweak y by +1.
def _off_curve_point() -> bytes:
    return (
        b"\x04"
        + oracle.GX.to_bytes(32, "big")
        + ((oracle.GY + 1) % oracle.P).to_bytes(32, "big")
    )


def _hex(b: bytes) -> str:
    return b.hex()


def _scalar_hex(s: int) -> str:
    return s.to_bytes(32, "big").hex()


def _x_int(seed: bytes) -> int:
    return int.from_bytes(seed, "big") % oracle.N


def vec(id: str, description: str, inputs: dict, outputs: dict | None = None,
        expected_error: str | None = None) -> dict:
    v: dict = {"id": id, "description": description, "inputs": inputs}
    if outputs is not None:
        v["outputs"] = outputs
    if expected_error is not None:
        v["expected_error"] = expected_error
    return v


def build_vectors() -> list[dict]:
    out: list[dict] = []

    # 1. mhf_basic --------------------------------------------------------
    mhf_out_basic = oracle.mhf(SETUP_CODE, SALT_ZEROS, iterations=ITERATIONS)
    out.append(vec(
        "mhf_basic",
        "PBKDF2-HMAC-SHA256(setup_code, all-zero salt, 100k iters, 80B output).",
        inputs={
            "setup_code": SETUP_CODE,
            "salt_hex": _hex(SALT_ZEROS),
            "iterations": ITERATIONS,
            "dklen": oracle.PBKDF2_DKLEN,
        },
        outputs={"mhf_out_hex": _hex(mhf_out_basic)},
    ))

    # 2. derive_w0_w1 -----------------------------------------------------
    w0, w1 = oracle.derive_w0_w1(mhf_out_basic)
    out.append(vec(
        "derive_w0_w1",
        "Split 80B MHF output into two 40B halves; reduce each modulo P-256 order n.",
        inputs={"mhf_out_hex": _hex(mhf_out_basic)},
        outputs={"w0_hex": _scalar_hex(w0), "w1_hex": _scalar_hex(w1)},
    ))

    # 3. compute_L --------------------------------------------------------
    L_pt = oracle.compute_L(w1)
    out.append(vec(
        "compute_L",
        "L = w1 * G (verifier point, SEC1 uncompressed 65 bytes).",
        inputs={"w1_hex": _scalar_hex(w1)},
        outputs={"L_hex": _hex(oracle.point_to_bytes(L_pt))},
    ))

    # 4. client_init_fixed_x ---------------------------------------------
    x_int = _x_int(X_FIXED)
    pA_pt = oracle.client_public(w0, x_int)
    out.append(vec(
        "client_init_fixed_x",
        "pA = x*G + w0*M with fixed x = 0x42 repeated 32 bytes (mod n).",
        inputs={"w0_hex": _scalar_hex(w0), "x_hex": _hex(X_FIXED)},
        outputs={"pA_hex": _hex(oracle.point_to_bytes(pA_pt))},
    ))

    # 5. server_init_fixed_y ---------------------------------------------
    y_int = _x_int(Y_FIXED)
    pB_pt = oracle.server_public(w0, y_int)
    out.append(vec(
        "server_init_fixed_y",
        "pB = y*G + w0*N with fixed y = 0x84 repeated 32 bytes (mod n).",
        inputs={
            "w0_hex": _scalar_hex(w0),
            "L_hex": _hex(oracle.point_to_bytes(L_pt)),
            "y_hex": _hex(Y_FIXED),
        },
        outputs={"pB_hex": _hex(oracle.point_to_bytes(pB_pt))},
    ))

    # 6. transcript -------------------------------------------------------
    Z_pt, V_pt = oracle.controller_shared(w0, w1, x_int, pB_pt)
    TT = oracle.transcript(
        oracle.CONTROLLER_IDENTITY,
        oracle.DEVICE_IDENTITY,
        (oracle.M_X, oracle.M_Y),
        (oracle.N_X, oracle.N_Y),
        pA_pt,
        pB_pt,
        Z_pt,
        V_pt,
        w0,
    )
    out.append(vec(
        "transcript",
        "RFC 9383 §3.3 length-prefixed transcript TT.",
        inputs={
            "controller_identity": oracle.CONTROLLER_IDENTITY.decode(),
            "device_identity": oracle.DEVICE_IDENTITY.decode(),
            "M_hex": _hex(oracle.point_to_bytes((oracle.M_X, oracle.M_Y))),
            "N_hex": _hex(oracle.point_to_bytes((oracle.N_X, oracle.N_Y))),
            "pA_hex": _hex(oracle.point_to_bytes(pA_pt)),
            "pB_hex": _hex(oracle.point_to_bytes(pB_pt)),
            "Z_hex": _hex(oracle.point_to_bytes(Z_pt)),
            "V_hex": _hex(oracle.point_to_bytes(V_pt)),
            "w0_hex": _scalar_hex(w0),
        },
        outputs={"TT_hex": _hex(TT), "TT_len": len(TT)},
    ))

    # 7. k_shared ---------------------------------------------------------
    K_sh = oracle.k_shared(TT)
    out.append(vec(
        "k_shared",
        "K_shared = SHA-256(TT).",
        inputs={"TT_hex": _hex(TT)},
        outputs={"K_shared_hex": _hex(K_sh)},
    ))

    # 8. key_chain --------------------------------------------------------
    K_main, K_confirm, K_encrypt = oracle.key_chain(K_sh)
    out.append(vec(
        "key_chain",
        "Three-key HKDF chain from K_shared with MASH-specific info strings.",
        inputs={"K_shared_hex": _hex(K_sh)},
        outputs={
            "K_main_hex": _hex(K_main),
            "K_confirm_hex": _hex(K_confirm),
            "K_encrypt_hex": _hex(K_encrypt),
        },
    ))

    # 9. confirmation_keys -----------------------------------------------
    KcA, KcB = oracle.confirmation_keys(K_confirm)
    out.append(vec(
        "confirmation_keys",
        "RFC 9383 §3.4: KcA||KcB = HKDF(K_confirm, \"ConfirmationKeys\", 32).",
        inputs={"K_confirm_hex": _hex(K_confirm)},
        outputs={"KcA_hex": _hex(KcA), "KcB_hex": _hex(KcB)},
    ))

    # 10. confirm_macs ----------------------------------------------------
    confirmA, confirmB = oracle.confirm_macs(KcA, KcB, pA_pt, pB_pt)
    out.append(vec(
        "confirm_macs",
        "confirmA = HMAC-SHA256(KcA, pB); confirmB = HMAC-SHA256(KcB, pA).",
        inputs={
            "KcA_hex": _hex(KcA),
            "KcB_hex": _hex(KcB),
            "pA_hex": _hex(oracle.point_to_bytes(pA_pt)),
            "pB_hex": _hex(oracle.point_to_bytes(pB_pt)),
        },
        outputs={"confirmA_hex": _hex(confirmA), "confirmB_hex": _hex(confirmB)},
    ))

    # 11. full_handshake --------------------------------------------------
    # Use a derived salt (not the all-zero salt) so this vector also covers
    # §2.3's salt-derivation rule. This is the canonical end-to-end vector.
    hs = oracle.full_handshake(SETUP_CODE, DISCRIMINATOR, x_int, y_int)
    out.append(vec(
        "full_handshake",
        "End-to-end DEC-084 handshake from setup code + discriminator + fixed x,y.",
        inputs={
            "setup_code": SETUP_CODE,
            "discriminator": DISCRIMINATOR,
            "x_hex": _hex(X_FIXED),
            "y_hex": _hex(Y_FIXED),
            "iterations": ITERATIONS,
        },
        outputs={
            "salt_hex": _hex(hs.salt),
            "mhf_out_hex": _hex(hs.mhf_out),
            "w0_hex": _scalar_hex(hs.w0),
            "w1_hex": _scalar_hex(hs.w1),
            "L_hex": _hex(oracle.point_to_bytes(hs.L)),
            "pA_hex": _hex(oracle.point_to_bytes(hs.pA)),
            "pB_hex": _hex(oracle.point_to_bytes(hs.pB)),
            "Z_hex": _hex(oracle.point_to_bytes(hs.Z)),
            "V_hex": _hex(oracle.point_to_bytes(hs.V)),
            "TT_hex": _hex(hs.TT),
            "K_shared_hex": _hex(hs.K_shared),
            "K_main_hex": _hex(hs.K_main),
            "K_confirm_hex": _hex(hs.K_confirm),
            "K_encrypt_hex": _hex(hs.K_encrypt),
            "KcA_hex": _hex(hs.KcA),
            "KcB_hex": _hex(hs.KcB),
            "confirmA_hex": _hex(hs.confirmA),
            "confirmB_hex": _hex(hs.confirmB),
        },
    ))

    # 12. point_validation (identity element) -----------------------------
    out.append(vec(
        "point_validation_identity",
        "Receiving the identity element as pA MUST be rejected (DEC-084 §2.5).",
        inputs={"point_hex": _hex(POINT_INFINITY_ENCODED)},
        expected_error="point at infinity / identity element",
    ))

    # 13. point_validation (off curve) ------------------------------------
    out.append(vec(
        "point_validation_off_curve",
        "Receiving an off-curve point as pA MUST be rejected (DEC-084 §2.5).",
        inputs={"point_hex": _hex(_off_curve_point())},
        expected_error="point not on curve",
    ))

    return out


def main() -> int:
    vectors = build_vectors()
    payload = {
        "schema_version": "1.0",
        "spec": "DEC-084",
        "spec_section": "docs/testing/behavior/commissioning-pase.md §2 (DEC-084)",
        "generated_by": "tools/spake2plus_oracle/gen_vectors.py",
        "note": (
            "Algorithmic conformance vectors. mash-go and mash-py SPAKE2+ "
            "implementations MUST reproduce every output byte-for-byte. "
            "Regenerate only when DEC-084 changes."
        ),
        "vectors": vectors,
    }
    OUTPUT.parent.mkdir(parents=True, exist_ok=True)
    OUTPUT.write_text(json.dumps(payload, indent=2, sort_keys=False) + "\n")
    print(f"wrote {len(vectors)} vectors to {OUTPUT}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
