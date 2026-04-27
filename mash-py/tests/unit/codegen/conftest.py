"""Test fixtures for ``codegen`` unit tests.

The fixtures resolve the spec input directories from the repo layout
without making the tests aware of relative-path arithmetic. ``pyproject``
and the codegen ``Makefile`` target both pass ``../docs/...``; tests do
the same lookup based on this file's location.
"""

from __future__ import annotations

from pathlib import Path

import pytest


@pytest.fixture(scope="session")
def repo_root() -> Path:
    # tests/unit/codegen/conftest.py -> mash-py -> repo root
    return Path(__file__).resolve().parents[3].parent


@pytest.fixture(scope="session")
def features_dir(repo_root: Path) -> Path:
    return repo_root / "docs" / "features"


@pytest.fixture(scope="session")
def usecases_dir(repo_root: Path) -> Path:
    return repo_root / "docs" / "usecases" / "1.0"


@pytest.fixture(scope="session")
def shared_path(features_dir: Path) -> Path:
    return features_dir / "_shared" / "1.0.yaml"


@pytest.fixture(scope="session")
def mash_py_root(repo_root: Path) -> Path:
    return repo_root / "mash-py" / "mash_py"
