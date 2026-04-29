"""Auto-PICS base-load tests for mash-py-test (F-3)."""

from __future__ import annotations

from pathlib import Path

import pytest

from mash_py.testharness.auto_pics import (
    DEFAULT_BASE_PICS_BASENAME,
    find_base_pics_path,
    load_base_pics,
)
from mash_py.testharness.loader import LoadError


def _make_pics_layout(tmp_path: Path) -> Path:
    """Create a mash-go-style testdata layout: cases/ + pics/protocol-common.yaml."""
    cases = tmp_path / "cases"
    cases.mkdir()
    pics_dir = tmp_path / "pics"
    pics_dir.mkdir()
    (pics_dir / DEFAULT_BASE_PICS_BASENAME).write_text(
        "device:\n"
        "  vendor: Test\n"
        "items:\n"
        "  MASH.S: 1\n"
        "  MASH.S.COMM: true\n"
        "  MASH.S.TLS.MUTUAL_AUTH: 1\n"
    )
    return cases


def test_find_base_pics_uses_sibling_pics_dir(tmp_path: Path) -> None:
    cases = _make_pics_layout(tmp_path)
    found = find_base_pics_path(cases)
    assert found is not None
    assert found.name == DEFAULT_BASE_PICS_BASENAME


def test_find_base_pics_returns_none_when_missing(tmp_path: Path) -> None:
    cases = tmp_path / "cases"
    cases.mkdir()
    assert find_base_pics_path(cases) is None


def test_load_base_pics_returns_pics_with_items(tmp_path: Path) -> None:
    cases = _make_pics_layout(tmp_path)
    pf = load_base_pics(cases)
    assert pf is not None
    assert pf.items.get("MASH.S.COMM") is True
    assert pf.items.get("MASH.S.TLS.MUTUAL_AUTH") == 1


def test_load_base_pics_returns_none_when_no_layout(tmp_path: Path) -> None:
    cases = tmp_path / "cases"
    cases.mkdir()
    # No pics directory at all -> soft fallback.
    assert load_base_pics(cases) is None


def test_load_base_pics_override_required_to_exist(tmp_path: Path) -> None:
    cases = tmp_path / "cases"
    cases.mkdir()
    nonexistent = tmp_path / "missing.yaml"
    # Explicit override is hard: missing file must surface as LoadError so
    # operators don't silently get "no PICS" when they meant a specific file.
    with pytest.raises(LoadError):
        load_base_pics(cases, override=str(nonexistent))


def test_load_base_pics_override_succeeds_for_existing_file(tmp_path: Path) -> None:
    custom = tmp_path / "custom.yaml"
    custom.write_text("items:\n  MY.PICS.KEY: true\n")
    cases = tmp_path / "cases"
    cases.mkdir()
    pf = load_base_pics(cases, override=str(custom))
    assert pf is not None
    assert pf.items.get("MY.PICS.KEY") is True
