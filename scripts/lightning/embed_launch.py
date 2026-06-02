#!/usr/bin/env python3
"""Drive a 3GPP-MCP embed run on a Lightning AI Studio GPU.

This is the Lightning counterpart of the Kaggle campaign driver: it provisions a
Studio, (later) bootstraps the repo + model + DB slice via scripts/embed-run.sh,
switches the Studio to the requested GPU only for the embed, streams the log, and
switches back to CPU to stop the GPU billing. The embed engine itself is the
platform-agnostic Go binary (cmd/embed), so the Studio is just a launcher.

Auth comes from LIGHTNING_USER_ID + LIGHTNING_API_KEY (source the gitignored
.env first). Discovered account config — username 'kodflow', teamspace
'default-teamspace' — is resolved at runtime, not hardcoded credentials.

Usage:
    set -a; . ./.env; set +a
    python scripts/lightning/embed_launch.py --probe                # T4 readiness check
    python scripts/lightning/embed_launch.py --probe --machine CPU  # cheap probe
    python scripts/lightning/embed_launch.py --embed --series 21    # (after embed-run.sh lands)

NOTE: launching compute requires a VERIFIED Lightning account. A brand-new
account returns HTTP 403 on Studio creation until onboarding/verification is
completed in the browser (lightning.ai). This script detects that and prints the
exact unblock step instead of a raw stack trace.
"""

from __future__ import annotations

import argparse
import sys

TEAMSPACE = "default-teamspace"


def _fail_if_unverified(exc: Exception) -> None:
    msg = str(exc)
    if "403" in msg or "unauthorized" in msg.lower():
        sys.stderr.write(
            "\n[BLOCKED] Lightning refused to create compute (HTTP 403).\n"
            "Your account is not verified yet (completed_signup/verified = False).\n"
            "Fix: open https://lightning.ai, sign in (GitHub), finish onboarding,\n"
            "then re-run this script. Nothing in the code needs to change.\n"
        )
        sys.exit(2)


def _resolve(studio_name: str):
    """Authenticate and return a Studio handle (created if absent)."""
    from lightning_sdk import Studio, Teamspace
    from lightning_sdk.lightning_cloud.login import Auth
    from lightning_sdk.lightning_cloud.rest_client import LightningClient

    Auth().authenticate()
    username = LightningClient().auth_service_get_user().username
    ts = Teamspace(name=TEAMSPACE, user=username)
    try:
        return Studio(name=studio_name, teamspace=ts, create_ok=True)
    except Exception as exc:  # noqa: BLE001 — turn the 403 into an actionable message
        _fail_if_unverified(exc)
        raise


def cmd_probe(args: argparse.Namespace) -> int:
    from lightning_sdk import Machine

    machine = getattr(Machine, args.machine)
    studio = _resolve(args.studio)
    print(f"studio={studio.name} status={studio.status} -> starting {args.machine}", flush=True)
    studio.start(machine)
    try:
        out = studio.run(
            "echo '--- gpu ---'; nvidia-smi -L 2>/dev/null || echo 'no GPU'; "
            "echo '--- cuda ---'; nvcc --version 2>/dev/null | tail -1 || echo 'no nvcc'; "
            "ls /usr/local/cuda*/lib64/libcudart.so* 2>/dev/null | head -1 || true; "
            "echo '--- host ---'; nproc; python3 --version; (go version 2>/dev/null || echo 'no go'); "
            "echo '--- disk ---'; df -h / | tail -1"
        )
        print(out)
    finally:
        # Drop back to CPU so a GPU probe does not keep billing.
        studio.start(Machine.CPU)
        print(f"switched back to CPU (status={studio.status})", flush=True)
    return 0


def cmd_embed(_args: argparse.Namespace) -> int:
    sys.stderr.write(
        "embed orchestration is finalized against a live (verified) Studio so the "
        "GPU bootstrap (ORT-CUDA + model + DB slice + build) is validated, not shipped "
        "blind. Verify the account, run --probe to confirm the T4, then this lands.\n"
    )
    return 3


def main(argv: list[str]) -> int:
    p = argparse.ArgumentParser(description="Lightning AI embed launcher for 3GPP-MCP")
    p.add_argument("--studio", default="embed-t4", help="Studio name (created if absent)")
    p.add_argument("--machine", default="T4", help="Machine type (T4, L4, A100, H200, CPU, ...)")
    g = p.add_mutually_exclusive_group(required=True)
    g.add_argument("--probe", action="store_true", help="start the machine, report GPU/host/disk, stop")
    g.add_argument("--embed", action="store_true", help="run the embed campaign (after embed-run.sh lands)")
    p.add_argument("--series", default="21", help="3GPP series to embed (smallest useful = 21)")
    args = p.parse_args(argv)
    return cmd_probe(args) if args.probe else cmd_embed(args)


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
