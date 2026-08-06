"""ARC-AGI-3 A/B evaluation: plain static harness vs Prime-style Continual
Harness, same model, same games, same steps budget.

Prepares, for each mode, an isolated REASONIX_STATE_HOME (copies the user's
real config.toml + .env so the provider works) and a per-game workspace that
carries the arc-agi3 skill + REASONIX.md. The only difference between modes is
the [harness] switch in the copied reasonix.toml.

Usage:
  .venv/Scripts/python run_eval.py --games ls20,ft09 --max-steps 200 [--verbose]
"""
from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
REASONIX_MD_SRC = HERE / "agent" / "REASONIX.md"

# Runs live OUTSIDE the repo so the agent can never read the game source or the
# eval harness (environment_files/ holds the downloadable game logic).
DEFAULT_RUNS_DIR = Path(tempfile.gettempdir()) / "arc-agi3-eval"

PLAIN_TOML = """config_version = 5
default_model = "opencode-go/deepseek-v4-flash"

[harness]
enabled = false
auto_refine = false
"""


def user_state_home() -> Path:
    """Locate the real Reasonix state root to copy provider config from."""
    for cand in (os.environ.get("REASONIX_STATE_HOME"), os.environ.get("REASONIX_HOME")):
        if cand and Path(cand).exists():
            return Path(cand)
    if os.name == "nt":
        cand = Path(os.environ.get("APPDATA", "")) / "reasonix"
    else:
        cand = Path.home() / ".reasonix"
    return cand if cand.exists() else Path()


def prepare_mode(base: Path, mode: str, user_home: Path) -> Path:
    """Create the isolated state home for one mode; returns its path."""
    state_home = base / f"state-{mode}"
    state_home.mkdir(parents=True, exist_ok=True)
    for name in ("config.toml", ".env"):
        src = user_home / name
        dst = state_home / name
        if src.exists() and not dst.exists():
            shutil.copy2(src, dst)
    if mode == "plain":
        toml = state_home / "reasonix.toml"
        if not toml.exists():
            toml.write_text(PLAIN_TOML, encoding="utf-8")
    return state_home


def prepare_workspace(base: Path, mode: str, game: str, state_home: Path) -> Path:
    """Per-game workspace with the project instruction (protocol + harness usage)."""
    ws = base / mode / game
    ws.mkdir(parents=True, exist_ok=True)
    md = ws / "REASONIX.md"
    if not md.exists():
        shutil.copy2(REASONIX_MD_SRC, md)
    return ws


def run_game(bin_path: str, game: str, ws: Path, state_home: Path,
             max_steps: int, verbose: bool) -> dict:
    cmd = [
        sys.executable, str(HERE / "run_game.py"),
        "--game", game,
        "--cwd", str(ws),
        "--state-home", str(state_home),
        "--reasonix-bin", bin_path,
        "--max-steps", str(max_steps),
        "--agent-rounds", "15",
        "--allowed-tools", "read_file,write_file,bash",
    ]
    proc = subprocess.run(cmd, capture_output=True, text=True, timeout=7200)
    if verbose:
        print(proc.stdout[-2000:], file=sys.stderr)
        if proc.stderr:
            print(proc.stderr[-2000:], file=sys.stderr)
    result_path = ws / "result.json"
    if result_path.exists():
        return json.loads(result_path.read_text(encoding="utf-8"))
    return {"game": game, "error": proc.stdout[-500:] or proc.stderr[-500:]}


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--games", required=True, help="comma-separated game ids")
    ap.add_argument("--max-steps", type=int, default=200)
    ap.add_argument("--verbose", action="store_true")
    ap.add_argument("--reasonix-bin", default=os.environ.get("REASONIX_BIN", "reasonix"))
    ap.add_argument("--runs-dir", default=str(DEFAULT_RUNS_DIR))
    ap.add_argument("--modes", default="plain,prime",
                    help="comma-separated modes to run (plain, prime)")
    ap.add_argument("--user-state-home", default=str(user_state_home()))
    args = ap.parse_args()

    games = [g.strip() for g in args.games.split(",") if g.strip()]
    modes = [m.strip() for m in args.modes.split(",") if m.strip()]
    runs = Path(args.runs_dir)
    runs.mkdir(parents=True, exist_ok=True)
    user_home = Path(args.user_state_home)

    summary: dict[str, dict[str, dict]] = {}
    for mode in modes:
        state_home = prepare_mode(runs, mode, user_home)
        summary[mode] = {}
        for game in games:
            ws = prepare_workspace(runs, mode, game, state_home)
            print(f"[{mode}] {game} ...", flush=True)
            summary[mode][game] = run_game(
                args.reasonix_bin, game, ws, state_home, args.max_steps, args.verbose)

    print("\n=== summary ===")
    header = f"{'game':<22}"
    for m in modes:
        header += f" {m+' lvls':>14}"
    if len(modes) == 2:
        header += f" {'delta':>7}"
    print(header)
    totals = {m: 0 for m in modes}
    for game in games:
        row = f"{game:<22}"
        for m in modes:
            lv = summary[m].get(game, {}).get("levels_completed", 0)
            totals[m] += lv
            row += f" {lv:>14}"
        if len(modes) == 2:
            row += f" {totals[modes[1]] - totals[modes[0]]:>7}"
        print(row)
    row = f"{'TOTAL':<22}"
    for m in modes:
        row += f" {totals[m]:>14}"
    print(row)
    (runs / "summary.json").write_text(json.dumps(summary, indent=1), encoding="utf-8")
    return 0


if __name__ == "__main__":
    sys.exit(main())
