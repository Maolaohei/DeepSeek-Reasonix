# Inspect the arc-agi environment contract: what env.make returns, what
# observation looks like, and the step() signature. Run from benchmarks/arc-agi3:
#   .venv/Scripts/python probe_env.py [game_id]
import json
import sys


def main() -> None:
    game = sys.argv[1] if len(sys.argv) > 1 else "ls20"
    import arc_agi

    arc = arc_agi.Arcade()
    try:
        env = arc.make(game)
    except Exception as e:  # noqa: BLE001 - probe prints any failure
        print(f"arc.make({game!r}) failed: {type(e).__name__}: {e}")
        return 1
    print(f"env type: {type(env)}")
    print(f"env attrs: {[a for a in dir(env) if not a.startswith('_')][:40]}")
    try:
        obs = env.reset()
        print(f"reset() type: {type(obs)}")
        print(f"reset() attrs: {[a for a in dir(obs) if not a.startswith('_')][:40]}")
        for a in dir(obs):
            if not a.startswith("_"):
                try:
                    v = getattr(obs, a)
                    if not callable(v):
                        s = str(v)
                        print(f"  obs.{a} = {s[:200]}")
                except Exception as e:  # noqa: BLE001
                    print(f"  obs.{a} -> error {e}")
    except Exception as e:  # noqa: BLE001
        print(f"env.reset() failed: {type(e).__name__}: {e}")
    try:
        from arcengine import GameAction
        obs2, reward, done, info = env.step(GameAction.ACTION1)
        print(f"step() -> obs={type(obs2).__name__} reward={reward} done={done} info={info}")
    except Exception as e:  # noqa: BLE001
        print(f"env.step() failed: {type(e).__name__}: {e}")
    try:
        scorecard = arc.get_scorecard()
        print(f"scorecard: {scorecard}")
    except Exception as e:  # noqa: BLE001
        print(f"get_scorecard failed: {type(e).__name__}: {e}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
