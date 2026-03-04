#!/usr/bin/env python3
"""Validate serper_toolkit/data/country_aliases.json."""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path
from typing import Any

DATA_PATH = Path("serper_toolkit/data/country_aliases.json")
ISO2_RE = re.compile(r"^[A-Z]{2}$")


def fail(message: str) -> None:
    print(f"ERROR: {message}")
    raise SystemExit(1)


def main() -> int:
    if not DATA_PATH.exists():
        fail(f"Data file does not exist: {DATA_PATH}")

    try:
        payload: Any = json.loads(DATA_PATH.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        fail(f"Invalid JSON at {DATA_PATH}: {exc}")

    if not isinstance(payload, dict):
        fail("Top-level JSON must be an object")

    total_countries = 0
    total_aliases = 0
    duplicate_aliases = 0

    for code, aliases in payload.items():
        total_countries += 1

        if not isinstance(code, str) or not ISO2_RE.fullmatch(code):
            fail(f"Invalid country code {code!r}: expected 2 uppercase letters")

        if not isinstance(aliases, list) or not aliases:
            fail(f"Country {code}: aliases must be a non-empty list")

        seen = set()
        for idx, alias in enumerate(aliases):
            if not isinstance(alias, str):
                fail(f"Country {code}: alias at index {idx} must be string")

            normalized = alias.strip()
            if not normalized:
                fail(f"Country {code}: alias at index {idx} must be non-empty")

            total_aliases += 1
            if normalized in seen:
                duplicate_aliases += 1
                fail(f"Country {code}: duplicate alias {normalized!r}")
            seen.add(normalized)

    print(
        "Validation succeeded: "
        f"countries={total_countries}, aliases={total_aliases}, duplicates={duplicate_aliases}"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
