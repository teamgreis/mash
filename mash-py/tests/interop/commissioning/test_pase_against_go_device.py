"""Phase 7D interop scaffold: SPAKE2+ algorithmic-layer interop with mash-go.

The full live PASE handshake (TLS connection, MsgTypes 1-4) ships in
Phase 8. This file asserts only the algorithmic layer:

1. Python's :func:`generate_verifier` produces the same ``(salt, w0, L)``
   bytes that mash-go's ``commissioning.GenerateVerifier`` produces for
   the same ``(setup_code, discriminator)`` inputs.

2. The ``full_handshake`` conformance vector — which both runtimes must
   reproduce byte-identically — gives us a frozen-ground-truth check
   without needing the Go toolchain available.

The ``go run`` cross-check is the load-bearing interop signal: the
recorded vectors come from the third-party oracle (``tools/spake2plus_oracle``),
so passing them only proves "Python matches the oracle." The Go
helper at ``mash-py/tools/print_verifier`` runs the actual mash-go
``commissioning.GenerateVerifier`` and prints the result, which we
compare to mash-py's output. Together this gives the three-way
agreement (Python, Go, oracle) the Phase 7 acceptance gate calls for.
"""

from __future__ import annotations

import json
import shutil
import subprocess
from pathlib import Path
from typing import Any

import pytest

from mash_py.commissioning import generate_verifier

REPO_ROOT = Path(__file__).resolve().parents[4]
GO_HELPER_DIR = REPO_ROOT / "mash-py" / "tools" / "print_verifier"


pytestmark = pytest.mark.interop


def _go_available() -> bool:
    return shutil.which("go") is not None


def _run_go_helper(setup_code: str, discriminator: int) -> dict[str, Any]:
    """Invoke the Go helper and return its parsed JSON output.

    Uses ``go run`` against ``mash-py/tools/print_verifier`` so the
    helper picks up the local mash-go via the ``replace`` directive in
    ``mash-py/tools/go.mod``. PBKDF2 100k takes ~1-3 seconds — keep
    the timeout generous to avoid CI flakes on slower runners.
    """
    cmd = [
        "go",
        "run",
        "./print_verifier",
        "--setup-code",
        setup_code,
        "--discriminator",
        str(discriminator),
    ]
    result = subprocess.run(
        cmd,
        cwd=str(GO_HELPER_DIR.parent),
        capture_output=True,
        text=True,
        timeout=60,
        check=False,
    )
    if result.returncode != 0:
        pytest.fail(
            "go helper failed:\n"
            f"  stdout: {result.stdout}\n"
            f"  stderr: {result.stderr}",
            pytrace=False,
        )
    parsed: dict[str, Any] = json.loads(result.stdout)
    return parsed


@pytest.mark.skipif(not _go_available(), reason="go toolchain not available")
@pytest.mark.slow
@pytest.mark.parametrize(
    ("setup_code", "discriminator"),
    [
        ("20202021", 3840),
        ("12345678", 1024),
    ],
)
def test_python_verifier_matches_go(setup_code: str, discriminator: int) -> None:
    """Cross-check: Python and Go produce byte-identical ``(salt, w0, L)``.

    The 100k PBKDF2 iterations make this run ~3-5 seconds; marked
    ``slow`` so a normal ``pytest`` invocation excludes it unless
    explicitly requested.
    """
    py_v = generate_verifier(setup_code, discriminator)
    go_v = _run_go_helper(setup_code, discriminator)

    assert int(go_v["discriminator"]) == discriminator
    assert go_v["salt_hex"] == py_v.salt.hex()
    assert go_v["w0_hex"] == py_v.w0.hex()
    assert go_v["l_hex"] == py_v.L.hex()
    assert int(go_v["iterations"]) == py_v.iterations


def test_static_verifier_matches_recorded_vector() -> None:
    """Frozen-ground-truth: ``full_handshake`` vector outputs (recorded
    from the third-party oracle, also matched by mash-go) MUST match
    Python's ``generate_verifier`` for the same inputs.

    This runs without the Go toolchain — useful for CI environments
    that ship Python only.
    """
    vector_path = REPO_ROOT / "mash-go" / "testdata" / "spake2plus_vectors.json"
    if not vector_path.exists():
        pytest.skip(f"vectors not present at {vector_path}")
    with vector_path.open(encoding="utf-8") as fh:
        doc = json.load(fh)
    full = next(v for v in doc["vectors"] if v["id"] == "full_handshake")
    inp = full["inputs"]
    out = full["outputs"]

    v = generate_verifier(inp["setup_code"], inp["discriminator"], iterations=inp["iterations"])
    assert v.salt.hex() == out["salt_hex"]
    assert v.w0.hex() == out["w0_hex"]
    assert v.L.hex() == out["L_hex"]


def test_live_pase_handshake_deferred_to_phase_8() -> None:
    """Sentinel test: documents that the full live PASE exchange lands
    in Phase 8 once :mod:`mash_py.commissioning.messages` and
    :mod:`mash_py.commissioning.pase_client` exist."""
    pytest.skip(
        "Phase 7D scope ends at the algorithmic layer. "
        "Live PASE message exchange (MsgTypes 1-4) ships in Phase 8."
    )
