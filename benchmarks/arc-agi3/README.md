# ARC-AGI-3 Evaluation

Benchmark harness that runs Reasonix as an ARC-AGI-3 agent and compares two
configurations:

- **plain** — static harness: Continual Harness disabled (`[harness]
  enabled = false`). Equivalent to a fixed system prompt / fixed memory.
- **prime** — Prime-Agent-style harness: `refine` tool + auto-refine gate on,
  the durable, model-editable supplemental state layer.

The two runs differ **only** in the harness switch: same model, same skill,
same per-step protocol, same workspace layout.

## How it works

```
step loop (max 200 steps per game):
  1. driver writes state.json  (frame grid, available actions, progress, history)
  2. `reasonix -p <step prompt>` runs once in the game workspace
  3. agent reads state.json, analyzes, writes action.txt
  4. driver reads action.txt, env.step(action), updates state.json
  5. until WIN / GAME_OVER
```

Each step is a fresh process (`reasonix run --max-steps 10 -p <step prompt>`
with `--allowed-tools read_file,write_file,bash`), but `REASONIX_STATE_HOME`
persists across steps and games, so harness prompt notes / memories learned on
earlier steps load into the system prefix of later steps automatically.

The game workspace (outside the repo, under the temp dir by default) carries a
`REASONIX.md` project instruction — the full protocol, ARC puzzle knowledge,
and when to call `refine` — plus `state.json` / `action.txt`. The agent is
sandboxed to that workspace and a minimal tool set, so it can never read the
game source or the eval harness.

## Prerequisites

- Python 3.12+ and the venv: `python -m venv .venv && .venv/Scripts/python -m pip install arc-agi`
  (the venv is already set up in this directory)
- A Reasonix binary (default `reasonix` on PATH; override with `--reasonix-bin`),
  with a working provider config. The user's real config + `.env` are copied
  into each run's isolated `REASONIX_STATE_HOME`, so the run never touches your
  real harness/memory state.
- No ARC API key needed: the toolkit registers an anonymous key automatically.
  Register at https://three.arcprize.org for public game access at release.

## Run

```bash
# all games, both modes, with per-step agent output logged
.venv/Scripts/python run_eval.py --games ls20,ft09 --max-steps 200 --verbose

# a quick smoke test (1 game, both modes, 30 steps)
.venv/Scripts/python run_eval.py --games ls20 --max-steps 30
```

Results land in `runs/<mode>/<game>/result.json` and the summary table prints
at the end. Use the same `--games` list and `--max-steps` for both modes so the
comparison is fair.

## Reading results

`result.json`:

```json
{
  "game": "ls20-9607627b",
  "steps": 41,
  "final_state": "WIN",
  "levels_completed": 7,
  "win_levels": 7,
  "score": 1.0,
  "errors": []
}
```

Compare `levels_completed` / `score` per game and the aggregate across the
game list. `errors` shows timeouts / invalid actions (steps consumed without
progress), which is itself a signal of agent quality.

## Notes

- Reasonix's `-p` print mode is one-shot per step; the session does not carry
  conversation memory across steps by design — the harness state is the
  durable memory, which is exactly what the benchmark measures.
- The eval workspaces are disposable: delete `runs/` to re-run cleanly.
- `probe.py` / `probe_env.py` introspect the arc-agi toolkit surface.
