# Probe the arc-agi toolkit surface: version, game list, action enum, and the
# observation/step contract. Run from benchmarks/arc-agi3:
#   .venv/Scripts/python probe.py
import sys


def main() -> None:
    print(f"python={sys.version.split()[0]}")
    try:
        import arc_agi
    except ImportError as e:
        print(f"arc_agi import failed: {e}")
        return 1
    print(f"arc_agi module: {arc_agi.__file__}")
    print(f"arc_agi attrs: {[a for a in dir(arc_agi) if not a.startswith('_')][:20]}")
    try:
        from arcengine import GameAction
        print(f"GameAction members: {[m.name for m in GameAction]}")
    except ImportError as e:
        print(f"arcengine import failed: {e}")
    try:
        from arcengine import GameObservation
        print(f"GameObservation members: {[a for a in dir(GameObservation) if not a.startswith('_')][:30]}")
    except ImportError as e:
        print(f"GameObservation import failed: {e}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
