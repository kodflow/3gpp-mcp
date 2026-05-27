#!/usr/bin/env python3
"""Emit a deterministic per-file sha256 manifest for the features tree.

Schema v2 (this version) adds an optional ``previous_hashes`` map per
relative path so the postStart 3-way sync can recognise stale-but-clean
workspaces — when the consumer's tracked+clean file matches a previously
shipped hash, the file is simply lagging behind the image build commit
and a silent fast-forward overwrite is safe.

Used by CI (.github/workflows/docker-images.yml) to produce
``image-template-files.json``, embedded into the image at
``/etc/devcontainer-template/.template-files.json``. Consumed at runtime by
``images/hooks/shared/sync-features.sh::_sync_via_manifest``.

Tracking issues: #334 (manifest origin), #367 (previous_hashes follow-up).

Schema v2 layout::

    {
      "version": 2,
      "commit": "abc1234",
      "generated_at": "2026-05-20T00:00:00Z",
      "files": {"path/to/file": "sha256:..."},
      "previous_hashes": {"path/to/file": ["sha256:...older", ...]}
    }

``previous_hashes`` is forward compatible: consumers reading v1 ignore the
extra field. ``files`` retains the v1 layout exactly. The previous-hash
list is bounded to ``PREV_HISTORY_CAP`` (default 8 generations) per file.

Usage::

    build-features-manifest.py <features_dir> <commit_short> <iso8601_utc>
                               [--prev-manifest <path>]
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
from pathlib import Path

PREV_HISTORY_CAP = 8


def build_manifest(
    root: Path,
    commit: str,
    generated_at: str,
    prev_manifest: dict | None = None,
) -> dict:
    files: dict[str, str] = {}
    previous_hashes: dict[str, list[str]] = {}

    for path in sorted(root.rglob("*")):
        if not path.is_file():
            continue
        rel = path.relative_to(root).as_posix()
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        files[rel] = f"sha256:{digest}"

    # Defensive shape validation: a malformed prev_manifest (e.g. a top-level
    # list instead of an object) would raise AttributeError on .get(). Treat
    # any non-dict as empty so build_manifest stays a graceful degradation
    # path rather than a CI-breaking crash. CodeRabbit #368.
    if isinstance(prev_manifest, dict):
        prev_files = prev_manifest.get("files", {})
        prev_history = prev_manifest.get("previous_hashes", {})
        if not isinstance(prev_files, dict):
            prev_files = {}
        if not isinstance(prev_history, dict):
            prev_history = {}
        for rel, prev_current_hash in prev_files.items():
            if not isinstance(prev_current_hash, str):
                continue
            current_hash = files.get(rel)
            # If the file is unchanged this generation, there's no need to
            # accumulate history — the current hash already covers it.
            if current_hash == prev_current_hash:
                continue
            history: list[str] = [prev_current_hash]
            # Validate prev_history[rel] is a list before iterating — a stray
            # string would otherwise expand to its characters via `for ... in`.
            # Symmetric with the prev_manifest/prev_files/prev_history shape
            # checks above. CodeRabbit #368 round 2.
            older_list = prev_history.get(rel, [])
            if not isinstance(older_list, list):
                older_list = []
            for older in older_list:
                if not isinstance(older, str):
                    continue
                if older != current_hash and older not in history:
                    history.append(older)
            previous_hashes[rel] = history[:PREV_HISTORY_CAP]

    return {
        "version": 2,
        "commit": commit,
        "generated_at": generated_at,
        "files": files,
        "previous_hashes": previous_hashes,
    }


def _load_prev_manifest(path: Path) -> dict | None:
    if not path.is_file():
        print(
            f"warning: --prev-manifest not found ({path}); proceeding with empty history",
            file=sys.stderr,
        )
        return None
    try:
        return json.loads(path.read_text())
    except json.JSONDecodeError as exc:
        print(
            f"warning: failed to parse --prev-manifest ({exc}); proceeding with empty history",
            file=sys.stderr,
        )
        return None


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(
        description="Emit a deterministic per-file sha256 manifest (schema v2).",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument("features_dir", type=Path)
    parser.add_argument("commit")
    parser.add_argument("generated_at")
    parser.add_argument(
        "--prev-manifest",
        type=Path,
        default=None,
        help="Optional previous manifest (v1 or v2). Drives previous_hashes.",
    )
    args = parser.parse_args(argv[1:])

    if not args.features_dir.is_dir():
        print(f"error: features dir not found: {args.features_dir}", file=sys.stderr)
        return 1

    prev_manifest = _load_prev_manifest(args.prev_manifest) if args.prev_manifest else None
    manifest = build_manifest(args.features_dir, args.commit, args.generated_at, prev_manifest)
    json.dump(manifest, sys.stdout, indent=2, sort_keys=True)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
