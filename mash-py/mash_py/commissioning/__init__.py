"""MASH commissioning protocol -- Python port (Phases 7-9, 11).

Phase 7D scope (DEC-084 SPAKE2+):
- :mod:`mash_py.commissioning.mhf` — Memory-Hard KDF (PBKDF2 v1).
- :mod:`mash_py.commissioning.spake2plus` — algorithm + state machines.

Future phases:
- Phase 8: PASE message exchange (CBOR codecs + state machine driver).
- Phase 9: CSR / certificate install.
- Phase 11: Pairing window helpers.

The public surface re-exports the SPAKE2+ primitives and state machines
so call sites do not need to know the module layout.
"""

from __future__ import annotations

from mash_py.commissioning.mhf import MHF_DKLEN, MHF_ITERATIONS, pbkdf2_mhf
from mash_py.commissioning.spake2plus import (
    CONFIRM_KEY_SIZE,
    CONFIRM_MAC_SIZE,
    CONTROLLER_IDENTITY,
    DEVICE_IDENTITY,
    INFO_K_CONFIRM,
    INFO_K_ENCRYPT,
    INFO_K_MAIN,
    INFO_KCA_KCB,
    POINT_SIZE,
    SALT_LEN,
    SALT_PREFIX,
    SCALAR_SIZE,
    SHARED_SECRET_SIZE,
    ConfirmationFailedError,
    IdentityElementError,
    InvalidPointError,
    SPAKE2PlusClient,
    SPAKE2PlusError,
    SPAKE2PlusServer,
    Verifier,
    client_public,
    compose_transcript,
    compute_L,
    confirm_macs,
    confirmation_keys,
    derive_salt,
    derive_w0_w1,
    generate_verifier,
    k_shared,
    key_chain,
    random_ephemeral,
    scalar_to_bytes,
    server_public,
    validate_point,
)

__all__ = [
    "CONFIRM_KEY_SIZE",
    "CONFIRM_MAC_SIZE",
    "CONTROLLER_IDENTITY",
    "DEVICE_IDENTITY",
    "INFO_KCA_KCB",
    "INFO_K_CONFIRM",
    "INFO_K_ENCRYPT",
    "INFO_K_MAIN",
    "MHF_DKLEN",
    "MHF_ITERATIONS",
    "POINT_SIZE",
    "SALT_LEN",
    "SALT_PREFIX",
    "SCALAR_SIZE",
    "SHARED_SECRET_SIZE",
    "ConfirmationFailedError",
    "IdentityElementError",
    "InvalidPointError",
    "SPAKE2PlusClient",
    "SPAKE2PlusError",
    "SPAKE2PlusServer",
    "Verifier",
    "client_public",
    "compose_transcript",
    "compute_L",
    "confirm_macs",
    "confirmation_keys",
    "derive_salt",
    "derive_w0_w1",
    "generate_verifier",
    "k_shared",
    "key_chain",
    "pbkdf2_mhf",
    "random_ephemeral",
    "scalar_to_bytes",
    "server_public",
    "validate_point",
]
