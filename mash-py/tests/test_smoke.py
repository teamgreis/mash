"""Phase 0 smoke test: package imports and exposes its version."""

from __future__ import annotations


def test_import() -> None:
    import mash_py

    assert mash_py.__version__ == "0.0.1"


def test_version_is_string() -> None:
    import mash_py

    assert isinstance(mash_py.__version__, str)
    assert len(mash_py.__version__) > 0
