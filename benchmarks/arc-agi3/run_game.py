"""ARC-AGI-3 single-game driver for Reasonix.

Protocol:
- The game observation is written to <cwd>/state.json (JSON).
- The agent answers by writing <cwd>/action.txt with one action token on the
  first line (RESET / ACTION1..ACTION7), optionally followed by "# reasoning".
- Each step invokes the Reasonix binary once in print mode, so every step is a
  fresh process but the harness/memory state root persists across steps, which
  is exactly the A/B axis this benchmark measures.

Usage:
  .venv/Scripts/python run_game.py --game ls20 --cwd <dir> --state-home <dir> [--reasonix-bin reasonix] [--max-steps 200]
"""
from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import time
from pathlib import Path

ACTION_NAMES = {0: "RESET", 1: "ACTION1", 2: "ACTION2", 3: "ACTION3",
                4: "ACTION4", 5: "ACTION5", 6: "ACTION6", 7: "ACTION7"}


def write_state(path: Path, obs, step: int, history: list[str], last_action: str | None) -> None:
    """Serialize a FrameDataRaw into the agent-visible state.json."""
    frames = getattr(obs, "frame", []) or []
    grid = [f.tolist() for f in frames] if frames else []
    payload = {
        "game_id": getattr(obs, "game_id", ""),
        "step": step,
        "state": str(getattr(obs, "state", "")),
        "levels_completed": int(getattr(obs, "levels_completed", 0)),
        "win_levels": int(getattr(obs, "win_levels", 0)),
        "available_actions": [ACTION_NAMES.get(i, f"ACTION{i}") for i in getattr(obs, "available_actions", [])],
        "last_action": last_action,
        "history": history[-20:],
        "frame": grid,
    }
    path.write_text(json.dumps(payload, indent=1), encoding="utf-8")


def wait_for_action(path: Path, timeout: float) -> str | None:
    """Poll for action.txt; returns its first line or None on timeout."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        if path.exists():
            try:
                text = path.read_text(encoding="utf-8", errors="replace").strip()
            except OSError:
                text = ""
            for line in text.splitlines():
                line = line.strip()
                if not line or line.startswith("#"):
                    continue
                return line
            return None  # file present but no action line yet
        time.sleep(0.2)
    return None


def run_reasonix(reasonix_bin: str, prompt: str, cwd: Path, state_home: Path,
                 env_extra: dict[str, str] | None = None, timeout: float = 300.0,
                 allowed_tools: str = "read_file,write_file", max_rounds: int = 10) -> str:
    """One print-mode Reasonix call; returns (trimmed stdout, stderr tail)."""
    env = os.environ.copy()
    env["REASONIX_STATE_HOME"] = str(state_home)
    env.setdefault("REASONIX_NONINTERACTIVE", "1")
    if env_extra:
        env.update(env_extra)
    cmd = [reasonix_bin, "run", "--max-steps", str(max_rounds), "-p", prompt]
    if allowed_tools:
        cmd += ["--allowed-tools", allowed_tools]
    try:
        proc = subprocess.run(
            cmd,
            cwd=str(cwd), env=env, capture_output=True, text=True, encoding="utf-8",
            errors="replace", timeout=timeout,
        )
    except subprocess.TimeoutExpired:
        return "<timeout>"
    except FileNotFoundError:
        return f"<reasonix binary not found: {reasonix_bin}>"
    return (proc.stdout or "")[-4000:]


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--game", required=True)
    ap.add_argument("--cwd", required=True, help="workspace dir holding state.json/action.txt")
    ap.add_argument("--state-home", required=True, help="REASONIX_STATE_HOME for this run")
    ap.add_argument("--reasonix-bin", default=os.environ.get("REASONIX_BIN", "reasonix"))
    ap.add_argument("--max-steps", type=int, default=200)
    ap.add_argument("--step-timeout", type=float, default=300.0)
    ap.add_argument("--agent-rounds", type=int, default=10, help="reasonix run --max-steps (agent tool rounds per step)")
    ap.add_argument("--allowed-tools", default="read_file,write_file", help="reasonix --allowed-tools restriction")
    ap.add_argument("--system-prompt", default="", help="extra instruction prefix for the agent")
    args = ap.parse_args()

    cwd = Path(args.cwd)
    cwd.mkdir(parents=True, exist_ok=True)
    state_home = Path(args.state_home)
    state_home.mkdir(parents=True, exist_ok=True)
    state_path = cwd / "state.json"
    action_path = cwd / "action.txt"

    sys.path.insert(0, str(Path(__file__).parent))
    import arc_agi  # local import keeps the driver importable without the venv

    arc = arc_agi.Arcade()
    env = arc.make(args.game)
    obs = env.reset()
    history: list[str] = []
    last_action: str | None = None
    result: dict = {"game": args.game, "steps": 0, "final_state": "", "levels_completed": 0,
                    "win_levels": 0, "score": 0.0, "errors": []}

    for step in range(1, args.max_steps + 1):
        state = str(getattr(obs, "state", ""))
        if state in ("WIN", "GAME_OVER"):
            break
        if action_path.exists():
            action_path.unlink()
        write_state(state_path, obs, step, history, last_action)

        prompt_lines = [
            args.system_prompt,
            f"ARC-AGI-3 game step {step}/{args.max_steps}. Read {state_path.name} with the read_file tool, "
            f"decide ONE action, then write {action_path.name} with the write_file tool (first line: RESET or "
            f"one of the available actions, optional '# reasoning:' comment). Writing action.txt is mandatory "
            f"— not writing it counts as a failed step. Do not plan multiple steps ahead; act now.",
        ]
        prompt = "\n".join(line for line in prompt_lines if line)
        output = run_reasonix(args.reasonix_bin, prompt, cwd, state_home,
                              allowed_tools=args.allowed_tools, max_rounds=args.agent_rounds,
                              timeout=args.step_timeout)
        if output.startswith("<") and output.endswith(">"):
            # The call itself failed (timeout/binary missing): the agent never
            # ran, so action.txt will never appear — do not burn another
            # step_timeout waiting for it.
            result["errors"].append(f"step {step}: {output}")
            history.append("NO_ACTION")
            last_action = "NO_ACTION"
            continue

        action_line = wait_for_action(action_path, timeout=args.step_timeout)
        if action_line is None:
            result["errors"].append(f"step {step}: no action written (agent output: {output[:300]})")
            # A failed step is not a failed game: keep going so a single bad
            # step (round limit, timeout) cannot end the run early.
            history.append("NO_ACTION")
            last_action = "NO_ACTION"
            continue
        token = action_line.split()[0].strip().upper()
        if token not in ACTION_NAMES.values():
            result["errors"].append(f"step {step}: invalid action token {token!r}")
            history.append(f"INVALID:{token}")
            last_action = f"INVALID:{token}"
            continue
        action_id = next(i for i, n in ACTION_NAMES.items() if n == token)
        try:
            from arcengine import GameAction
            obs = env.step(GameAction[token])
        except Exception as e:  # noqa: BLE001
            result["errors"].append(f"step {step}: step({token}) failed: {e}")
            break
        if obs is None:
            result["errors"].append(f"step {step}: env.step returned None")
            break
        history.append(token)
        last_action = token

    final_state = str(getattr(obs, "state", ""))
    result["steps"] = len(history)
    result["final_state"] = final_state
    result["levels_completed"] = int(getattr(obs, "levels_completed", 0))
    result["win_levels"] = int(getattr(obs, "win_levels", 0))
    try:
        card = arc.get_scorecard()
        if hasattr(card, "score"):
            result["score"] = float(card.score)
    except Exception:  # noqa: BLE001
        pass
    out_path = cwd / "result.json"
    out_path.write_text(json.dumps(result, indent=1), encoding="utf-8")
    print(json.dumps(result))
    return 0


if __name__ == "__main__":
    sys.exit(main())
