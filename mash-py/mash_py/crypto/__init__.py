"""MASH crypto facade.

Ports of cryptographic primitives used by mash-go's transport, commissioning,
and renewal layers. The goal is a small, language-agnostic surface so the rest
of the Python stack never touches ``cryptography.hazmat.*`` directly; a future
back-end swap (for example, CircuitPython's ``adafruit_hashlib`` /
``adafruit_rsa`` ecosystem — Decision B in mash-py-tdd-plan.md) only needs
this module re-implemented.

Public surface:

- :mod:`mash_py.crypto.kdf` — HKDF-SHA256 (RFC 5869)
- :mod:`mash_py.crypto.mac` — HMAC-SHA256 (RFC 4231)
- :mod:`mash_py.crypto.ec`  — P-256 point arithmetic + SEC1 codec +
  hash-to-point used by SPAKE2+ (RFC 9383)
- :mod:`mash_py.crypto.certs` — X.509 helpers: self-signed cert, CSR
  generate/sign, chain verify, key-id extraction

The Go reference for everything in this package is
``mash-go/pkg/commissioning/spake2plus.go`` and the standard-library imports
it pulls in (``crypto/hmac``, ``crypto/sha256``, ``crypto/elliptic``,
``golang.org/x/crypto/hkdf``).
"""

from __future__ import annotations

from mash_py.crypto.ec import (
    P256_FIELD_PRIME,
    P256_GENERATOR_X,
    P256_GENERATOR_Y,
    P256_ORDER,
    SPAKE2_M,
    SPAKE2_N,
    Point,
    decode_point,
    encode_point_compressed,
    encode_point_uncompressed,
    hash_to_curve_p256,
    is_on_curve,
    point_add,
    scalar_base_mult,
    scalar_mult,
)
from mash_py.crypto.kdf import hkdf_sha256
from mash_py.crypto.mac import hmac_sha256, hmac_sha256_verify

__all__ = [
    "P256_FIELD_PRIME",
    "P256_GENERATOR_X",
    "P256_GENERATOR_Y",
    "P256_ORDER",
    "SPAKE2_M",
    "SPAKE2_N",
    "Point",
    "decode_point",
    "encode_point_compressed",
    "encode_point_uncompressed",
    "hash_to_curve_p256",
    "hkdf_sha256",
    "hmac_sha256",
    "hmac_sha256_verify",
    "is_on_curve",
    "point_add",
    "scalar_base_mult",
    "scalar_mult",
]
