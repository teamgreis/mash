"""Cross-implementation byte-level vectors for the DEC-085 commissioning
cert-install envelope.

Vectors are emitted by ``mash-py/tools/print_cert_install_vectors/main.go``
(which uses mash-go's ``commissioning.EncodePASEMessage`` directly) and
checked into ``mash-go/testdata/cert_install_vectors.json``. Each vector
gets two assertions:

1. **Decode-equivalence:** ``mash_py.commissioning.decode(cbor_hex)``
   yields a typed dataclass whose fields match the JSON ``fields`` map.
   Catches any case where mash-py's decoder reads a Go-emitted byte
   string into the wrong field set.

2. **Encode-equivalence:** building the same dataclass in Python and
   running ``encode()`` produces the exact same ``cbor_hex``. Catches
   any case where mash-py's encoder produces a logically-equal but
   byte-different CBOR encoding (key order, integer width, etc.).

Together these are a tight cross-impl byte pin: both runtimes must agree
on the canonical encoding for any logically-equal message. Encoder
drift on either side breaks the test.

Spec refs: ``docs/decision-log.md`` DEC-085, ``docs/message-types.md §3``,
``docs/testing/behavior/commissioning-pase.md §4.3``.

Regenerating vectors
--------------------

If the wire format changes legitimately (a new DEC), regenerate with::

    cd mash-py/tools
    go run ./print_cert_install_vectors > ../../mash-go/testdata/cert_install_vectors.json

Then re-run this test to confirm mash-py keeps up.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from mash_py.commissioning import (
    CertInstall,
    CertInstallResponse,
    CommissioningComplete,
    CSRRequest,
    CSRResponse,
    decode_message,
    encode_message,
)

REPO_ROOT = Path(__file__).resolve().parents[4]
VECTORS_PATH = REPO_ROOT / "mash-go" / "testdata" / "cert_install_vectors.json"


def _load_vectors() -> list[dict]:
    if not VECTORS_PATH.exists():
        pytest.skip(
            f"vectors not present at {VECTORS_PATH}; "
            f"regenerate via `cd mash-py/tools && go run ./print_cert_install_vectors > ../../mash-go/testdata/cert_install_vectors.json`"
        )
    with VECTORS_PATH.open(encoding="utf-8") as fh:
        doc = json.load(fh)
    return list(doc["vectors"])


def _build_message_from_fields(name: str, fields: dict, msg_type: int):
    """Reconstruct the typed dataclass from the JSON ``fields`` map.

    Each branch knows the exact field names emitted by the Go helper
    (see ``mash-py/tools/print_cert_install_vectors/main.go``) and maps
    them onto mash-py's dataclass constructors.
    """
    if msg_type == 10:  # MSG_CSR_REQUEST
        return CSRRequest(nonce=bytes.fromhex(fields["nonce_hex"]))
    if msg_type == 11:  # MSG_CSR_RESPONSE
        return CSRResponse(
            csr=bytes.fromhex(fields["csr_hex"]),
            error_code=fields["error_code"],
        )
    if msg_type == 12:  # MSG_CERT_INSTALL
        return CertInstall(
            operational_cert=bytes.fromhex(fields["operational_cert_hex"]),
            ca_cert=bytes.fromhex(fields["ca_cert_hex"]),
            zone_type=fields["zone_type"],
            zone_priority=fields["zone_priority"],
        )
    if msg_type == 13:  # MSG_CERT_INSTALL_RESPONSE
        return CertInstallResponse(error_code=fields["error_code"])
    if msg_type == 20:  # MSG_COMMISSIONING_COMPLETE
        return CommissioningComplete()
    pytest.fail(f"unknown msg_type {msg_type} in vector {name!r}")


# Materialise the vector list at import time so each test gets a clean,
# parameterised id (the vector name) rather than a shared fixture.
_VECTORS = _load_vectors() if VECTORS_PATH.exists() else []


@pytest.mark.parametrize(
    "vector",
    _VECTORS,
    ids=[v["name"] for v in _VECTORS] if _VECTORS else None,
)
def test_decode_equivalence(vector: dict) -> None:
    """mash-py's decoder reads Go-emitted bytes into the expected fields."""
    cbor_bytes = bytes.fromhex(vector["cbor_hex"])
    decoded = decode_message(cbor_bytes)
    expected = _build_message_from_fields(
        vector["name"], vector["fields"], vector["msg_type"]
    )
    assert decoded == expected, (
        f"vector {vector['name']!r} ({vector['go_struct']}): "
        f"decoded fields differ from expected"
    )


@pytest.mark.parametrize(
    "vector",
    _VECTORS,
    ids=[v["name"] for v in _VECTORS] if _VECTORS else None,
)
def test_encode_equivalence(vector: dict) -> None:
    """mash-py's encoder reproduces Go-emitted bytes exactly.

    This is the strict half of the byte pin. Decode-equivalence alone
    would let mash-py silently emit a different byte ordering that round-
    trips through its own decoder but breaks against mash-go (or against
    any other implementation that reads the canonical form). Asserting
    byte equality both ways closes that gap.
    """
    msg = _build_message_from_fields(
        vector["name"], vector["fields"], vector["msg_type"]
    )
    encoded = encode_message(msg)
    expected_bytes = bytes.fromhex(vector["cbor_hex"])
    assert encoded == expected_bytes, (
        f"vector {vector['name']!r} ({vector['go_struct']}): "
        f"mash-py encoded {encoded.hex()!r}; "
        f"mash-go produced {vector['cbor_hex']!r}"
    )
