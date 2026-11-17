"""标注基准评测：把 pipeline 产出的事件标签与人工标注比对。

用法：python -m tests.eval_tags [db_path]
口径：expected 为空表示「不该有该维标签」；expected 中任一项被预测到即算正确，
多标不罚（一篇文章天然跨多行业），漏标与错标算错。
"""

from __future__ import annotations

import sys
from pathlib import Path

import yaml

from app import db

FIXTURE = Path(__file__).parent / "fixtures" / "tag_labels.yaml"
DIMS = ("region", "industry", "type")


def evaluate(db_path: str) -> dict:
    conn = db.connect(db_path)
    labels = yaml.safe_load(FIXTURE.read_text(encoding="utf-8"))
    events = conn.execute("SELECT id, title FROM events").fetchall()
    predicted: dict[int, dict[str, set[str]]] = {}
    for ev in events:
        dims: dict[str, set[str]] = {}
        for row in conn.execute(
            "SELECT dim, value FROM event_tags WHERE event_id = ?", (ev["id"],)
        ):
            dims.setdefault(row["dim"], set()).add(row["value"])
        predicted[ev["id"]] = dims

    stats = {d: {"ok": 0, "total": 0, "missed": []} for d in DIMS}
    unmatched = []
    for item in labels:
        needle = item["title"]
        hits = [e for e in events if needle in e["title"]]
        if not hits:
            unmatched.append(needle)
            continue
        dims = predicted[hits[0]["id"]]
        for dim in DIMS:
            expected = set(item.get(dim) or [])
            got = dims.get(dim, set())
            stats[dim]["total"] += 1
            if (not expected and not got) or (expected & got):
                stats[dim]["ok"] += 1
            else:
                stats[dim]["missed"].append((needle, sorted(expected), sorted(got)))

    out = {
        "matched": len(labels) - len(unmatched),
        "unmatched_titles": unmatched,
        "dims": {d: round(s["ok"] / s["total"], 3) if s["total"] else 0.0
                 for d, s in stats.items()},
        "misses": {d: s["missed"] for d, s in stats.items()},
    }
    conn.close()
    return out


if __name__ == "__main__":
    db_path = sys.argv[1] if len(sys.argv) > 1 else "data/_eval.db"
    report = evaluate(db_path)
    print(f"匹配到 {report['matched']} 条标注（未匹配 {len(report['unmatched_titles'])}）")
    for dim, acc in report["dims"].items():
        print(f"  {dim:<9}{acc:.1%}")
    for dim, misses in report["misses"].items():
        for needle, expected, got in misses:
            print(f"  ✗ {dim:<9}{needle[:26]:<28} 期望{expected} 实际{got}")
