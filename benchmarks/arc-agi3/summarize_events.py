"""Summarize reasonix --events-jsonl output: print kind + brief text per event.

Usage: reasonix run --events-jsonl ... 2>/dev/null | .venv/Scripts/python summarize_events.py
"""
import json
import sys


def main() -> int:
    for line in sys.stdin:
        try:
            e = json.loads(line)
        except ValueError:
            continue
        kind = e.get("kind", "")
        text = str(e.get("type", e.get("text", e.get("name", ""))))[:140]
        print(f"{kind:<14} {text}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
