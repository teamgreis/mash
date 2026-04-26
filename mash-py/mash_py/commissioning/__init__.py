"""MASH commissioning protocol -- Python port (Phases 7-9, 11).

Phase scope:
- :mod:`mash_py.commissioning.mhf` — Memory-Hard KDF (PBKDF2 v1).
- :mod:`mash_py.commissioning.spake2plus` — algorithm + state machines.
- :mod:`mash_py.commissioning.messages` — PASE message dataclasses + CBOR codec (Phase 8).
- :mod:`mash_py.commissioning.pase_client` — controller-side handshake driver (Phase 8).

Future phases:
- Phase 9: CSR / certificate install.
- Phase 11: Pairing window helpers.

The public surface re-exports the SPAKE2+ primitives, the PASE message
dataclasses, and the controller state machine so call sites do not need
to know the module layout.
"""

from __future__ import annotations

from mash_py.commissioning.messages import (
    ERR_CODE_AUTH_FAILED,
    ERR_CODE_BUSY,
    ERR_CODE_INTERNAL_ERROR,
    ERR_CODE_SUCCESS,
    ERR_CODE_TIMEOUT,
    MSG_COMMISSIONING_ERROR,
    MSG_PASE_COMPLETE,
    MSG_PASE_CONFIRM,
    MSG_PASE_REQUEST,
    MSG_PASE_RESPONSE,
    CommissioningError,
    InvalidMessageError,
    MessageError,
    PASEComplete,
    PASEConfirm,
    PASEMessage,
    PASERequest,
    PASEResponse,
)
from mash_py.commissioning.messages import decode as decode_message
from mash_py.commissioning.messages import encode as encode_message
from mash_py.commissioning.messages import error_code_string, peek_msg_type
from mash_py.commissioning.mhf import MHF_DKLEN, MHF_ITERATIONS, pbkdf2_mhf
from mash_py.commissioning.pase_client import (
    PASEAuthFailedError,
    PASEBusyError,
    PASEClient,
    PASEClientError,
    PASEProtocolError,
)
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
    "ERR_CODE_AUTH_FAILED",
    "ERR_CODE_BUSY",
    "ERR_CODE_INTERNAL_ERROR",
    "ERR_CODE_SUCCESS",
    "ERR_CODE_TIMEOUT",
    "INFO_KCA_KCB",
    "INFO_K_CONFIRM",
    "INFO_K_ENCRYPT",
    "INFO_K_MAIN",
    "MHF_DKLEN",
    "MHF_ITERATIONS",
    "MSG_COMMISSIONING_ERROR",
    "MSG_PASE_COMPLETE",
    "MSG_PASE_CONFIRM",
    "MSG_PASE_REQUEST",
    "MSG_PASE_RESPONSE",
    "POINT_SIZE",
    "SALT_LEN",
    "SALT_PREFIX",
    "SCALAR_SIZE",
    "SHARED_SECRET_SIZE",
    "CommissioningError",
    "ConfirmationFailedError",
    "IdentityElementError",
    "InvalidMessageError",
    "InvalidPointError",
    "MessageError",
    "PASEAuthFailedError",
    "PASEBusyError",
    "PASEClient",
    "PASEClientError",
    "PASEComplete",
    "PASEConfirm",
    "PASEMessage",
    "PASEProtocolError",
    "PASERequest",
    "PASEResponse",
    "SPAKE2PlusClient",
    "SPAKE2PlusError",
    "SPAKE2PlusServer",
    "Verifier",
    "client_public",
    "compose_transcript",
    "compute_L",
    "confirm_macs",
    "confirmation_keys",
    "decode_message",
    "derive_salt",
    "derive_w0_w1",
    "encode_message",
    "error_code_string",
    "generate_verifier",
    "k_shared",
    "key_chain",
    "pbkdf2_mhf",
    "peek_msg_type",
    "random_ephemeral",
    "scalar_to_bytes",
    "server_public",
    "validate_point",
]
