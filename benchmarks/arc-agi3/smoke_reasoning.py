# Smoke-check the reasoning-carrying eval protocol helpers. Run from
# benchmarks/arc-agi3:  .venv/Scripts/python smoke_reasoning.py
import sys


def main() -> int:
    import run_game

    tok, reason = run_game.parse_action_line("ACTION3 # 推断:右移一格")
    assert tok == "ACTION3" and reason == "推断:右移一格", (tok, reason)
    tok, reason = run_game.parse_action_line("RESET")
    assert tok == "RESET" and reason == "", (tok, reason)
    tok, reason = run_game.parse_action_line("action2  #lowercase # no reasoning")
    assert tok == "ACTION2" and reason == "lowercase # no reasoning", (tok, reason)
    print("reasoning protocol OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
