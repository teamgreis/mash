"""``mash-py-test`` — YAML-driven Python test runner against a MASH device.

A minimal, mash-test-shaped CLI for the Phase-21 acceptance subset. Loads
YAML test cases (default: ``mash-go/testdata/cases``), filters by file /
tag / pattern, and runs each case against a live device using the
handlers in :mod:`mash_py.testharness.handlers`.

Usage example::

    mash-py-test --setup-code 20202021 --discriminator 3072 \\
        --filter "TC-COMM-001" --tests mash-go/testdata/cases

Spec-vs-Go: the CLI surface mirrors mash-go's flags so the same
invocation shape works against both runners (Phase 22 uses this for
side-by-side comparison). Where a flag has no Phase-21 equivalent (e.g.
``-strict-lifecycle``) we accept it but log a "not implemented" note
instead of failing — that keeps shell-level invocations portable.
"""

from __future__ import annotations

import argparse
import asyncio
import sys
from pathlib import Path
from typing import Sequence

from mash_py.testharness.handlers import register_default_handlers
from mash_py.testharness.loader import (
    LoadError,
    filter_by_pattern,
    filter_by_tags,
    load_test_directory,
)
from mash_py.testharness.pics import filter_test_cases, load_pics
from mash_py.testharness.runner import Runner, RunnerConfig


def _build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="mash-py-test",
        description="Phase-21 Python test runner for MASH conformance YAML cases.",
    )
    p.add_argument(
        "--target",
        default="",
        help="host:port of device under test (mDNS-resolved if empty)",
    )
    p.add_argument(
        "--tests",
        default="mash-go/testdata/cases",
        help="directory containing YAML test cases (default: %(default)s)",
    )
    p.add_argument("--filter", default="", help="glob filter on test case ID")
    p.add_argument(
        "--files",
        default="",
        help="comma-separated glob filter on YAML filename stems",
    )
    p.add_argument("--tags", default="", help="include only tests with these tags (csv)")
    p.add_argument(
        "--exclude-tags",
        default="",
        help="exclude tests with these tags (csv)",
    )
    p.add_argument(
        "--setup-code",
        default="20202021",
        help="PASE setup code (default: %(default)s)",
    )
    p.add_argument(
        "--discriminator",
        type=int,
        default=3072,
        help="device discriminator (default: %(default)s)",
    )
    p.add_argument("--pics", default="", help="path to PICS file (optional)")
    p.add_argument(
        "--timeout",
        type=float,
        default=30.0,
        help="per-test timeout in seconds (default: %(default)s)",
    )
    p.add_argument("--verbose", action="store_true", help="print per-step traces on success")
    p.add_argument(
        "--list",
        action="store_true",
        help="list matched cases and exit (no execution)",
    )
    return p


def main(argv: Sequence[str] | None = None) -> int:
    args = _build_parser().parse_args(argv)

    test_dir = Path(args.tests)
    if not test_dir.is_dir():
        print(f"error: tests directory not found: {test_dir}", file=sys.stderr)
        return 2

    try:
        cases = load_test_directory(test_dir, files_filter=args.files)
    except LoadError as exc:
        print(f"error loading tests: {exc}", file=sys.stderr)
        return 2

    cases = filter_by_pattern(cases, args.filter)
    cases = filter_by_tags(cases, include=args.tags, exclude=args.exclude_tags)

    pics = None
    if args.pics:
        try:
            pics = load_pics(args.pics)
        except LoadError as exc:
            print(f"error loading PICS: {exc}", file=sys.stderr)
            return 2
        cases = filter_test_cases(cases, pics)

    if not cases:
        print("no matching test cases", file=sys.stderr)
        return 0

    if args.list:
        for tc in cases:
            print(f"{tc.id}\t{tc.name}\t[{', '.join(tc.tags)}]")
        return 0

    config = RunnerConfig(
        target=args.target,
        discriminator=args.discriminator,
        setup_code=args.setup_code,
        pics=pics,
        timeout_s=args.timeout,
        verbose=args.verbose,
    )

    runner = Runner(config)
    register_default_handlers(runner)

    print(
        f"mash-py-test: {len(cases)} case(s) "
        f"target={args.target or 'mDNS'} setup-code={args.setup_code}",
    )

    suite = asyncio.run(runner.run_suite(cases))
    return 0 if suite.fail_count == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
