"""Auto-PICS for mash-py-test.

The Go runner's ``runAutoPICS`` (``mash-go/internal/testharness/runner/auto_pics.go``)
loads a base protocol-level PICS file from ``testdata/pics/protocol-common.yaml``
and merges device-specific items (endpoints, features, attributes, use cases)
discovered at runtime by reading DeviceInfo and the global feature attributes
(``attributeList`` / ``commandList`` / ``featureMap``).

This module ports the **base-load half**. Without it, mash-py-test runs every
YAML case in the corpus regardless of whether the device claims the relevant
protocol-level PICS items, which inflates wall time and obscures real test
signal in the failure pile (Phase-21 follow-up note F-3 in
``conformance-status.md``).

Device-side discovery (commissioning + walking endpoints) remains a follow-up
since it requires running PASE / cert-exchange before tests start, which
restructures the CLI's "load cases, run, exit" flow. The base-only load
already eliminates the bulk of false-positive "no matching PICS" failures
because most protocol-level requirements (TLS, transport, discovery,
security) are device-independent.

The default base path is ``../mash-go/testdata/pics/protocol-common.yaml``
relative to ``--tests``; pass ``--pics-base`` to override.
"""

from __future__ import annotations

from pathlib import Path

from mash_py.testharness.loader import LoadError, PICSFile
from mash_py.testharness.pics import load_pics

DEFAULT_BASE_PICS_BASENAME = "protocol-common.yaml"


def find_base_pics_path(tests_dir: Path) -> Path | None:
    """Locate the base protocol-common.yaml relative to a tests directory.

    The mash-go corpus puts ``testdata/cases/`` and ``testdata/pics/`` as
    siblings; mirror that layout. Returns None when the file isn't found
    so callers can treat "no auto-PICS available" as a soft fallback.
    """
    candidates = [
        tests_dir.parent / "pics" / DEFAULT_BASE_PICS_BASENAME,
        tests_dir / "pics" / DEFAULT_BASE_PICS_BASENAME,
    ]
    for p in candidates:
        if p.is_file():
            return p
    return None


def load_base_pics(tests_dir: Path, override: str | None = None) -> PICSFile | None:
    """Load the protocol-common base PICS, or None when not found.

    ``override`` is the user-supplied path (``--pics-base``); when set we
    insist the file exists and surface ``LoadError`` if not. Without an
    override, missing files are silently treated as "no auto-PICS"; that's
    the right behaviour when the user is running mash-py-test against
    YAML they wrote outside the mash-go corpus tree.
    """
    if override:
        return load_pics(override)
    path = find_base_pics_path(tests_dir)
    if path is None:
        return None
    return load_pics(path)


__all__ = [
    "DEFAULT_BASE_PICS_BASENAME",
    "find_base_pics_path",
    "load_base_pics",
]
