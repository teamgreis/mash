"""CLI entry point: ``python -m codegen --features-dir ... --usecases-dir ... --output ...``.

Mirrors the argument shape used in mash-py's ``Makefile`` (``make
generate`` runs this module against ``../docs/features`` /
``../docs/usecases/1.0`` / ``mash_py/``).
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

from codegen.generate import generate_all, write_outputs


def _arg_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="python -m codegen",
        description="Regenerate mash_py/features/, mash_py/model/, and "
        "mash_py/usecase/definitions_gen.py from the YAML spec.",
    )
    parser.add_argument(
        "--features-dir",
        required=True,
        type=Path,
        help="Path to docs/features/ (the directory containing protocol-versions.yaml)",
    )
    parser.add_argument(
        "--usecases-dir",
        required=True,
        type=Path,
        help="Path to docs/usecases/<version>/",
    )
    parser.add_argument(
        "--output",
        required=True,
        type=Path,
        help="Path to mash_py/ (generated files land in features/, model/, usecase/)",
    )
    parser.add_argument(
        "--version",
        default="1.0",
        help="Spec version to generate (default: 1.0)",
    )
    parser.add_argument(
        "--check",
        action="store_true",
        help="Compare against committed files; exit 1 if any drift.",
    )
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _arg_parser().parse_args(argv)

    outputs = generate_all(
        features_dir=args.features_dir,
        usecases_dir=args.usecases_dir,
        version=args.version,
    )

    if args.check:
        drifted: list[str] = []
        for gf in outputs:
            dest = args.output / gf.rel_path
            if not dest.exists():
                drifted.append(f"missing: {gf.rel_path}")
                continue
            if dest.read_text(encoding="utf-8") != gf.content:
                drifted.append(f"drift:   {gf.rel_path}")
        if drifted:
            print("Generated output is out of date:", file=sys.stderr)
            for line in drifted:
                print(f"  {line}", file=sys.stderr)
            print("Run `make generate` to refresh.", file=sys.stderr)
            return 1
        print(f"OK: {len(outputs)} generated files match.")
        return 0

    written = write_outputs(outputs, args.output)
    for p in written:
        print(f"  generated {p}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
