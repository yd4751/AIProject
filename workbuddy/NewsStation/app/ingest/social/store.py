"""社媒榜单快照落库：批次、排名升降、过期清理。

与 articles 的「同一 URL 只存一次」不同，榜单每次抓取都要留一份快照，
才能看出话题的排名变化；同时按平台比较上一批，算出 rank_delta。
"""

from __future__ import annotations

import json
import time

from app import db
from app.ingest.registry import Article


def save_social_trends(
    db_path,
    items: list[Article],
    batch_id: int | None = None,
    retention_days: int = 7,
) -> int:
    items = [a for a in items if a.platform]
    if not items:
        return 0
    now = int(time.time())
    batch = batch_id or now
    conn = db.connect(db_path)
    try:
        platforms = {a.platform for a in items}
        previous: dict[str, dict[str, int]] = {}
        for platform in platforms:
            last = conn.execute(
                "SELECT MAX(batch_id) FROM social_trends WHERE platform = ?", (platform,)
            ).fetchone()[0]
            if last is None or last == batch:
                continue
            previous[platform] = {
                r["title"]: r["rank"]
                for r in conn.execute(
                    "SELECT title, rank FROM social_trends WHERE platform = ? AND batch_id = ?",
                    (platform, last),
                )
            }

        rows = []
        for a in items:
            prior_rank = previous.get(a.platform, {}).get(a.title)
            # NULL = 新进榜；正数 = 名次上升；负数 = 下滑
            delta = None if prior_rank is None else prior_rank - a.rank
            rows.append(
                (
                    batch, a.platform, a.source_id, a.title, a.url, a.rank,
                    a.heat_value, delta, a.summary, a.lang,
                    json.dumps(a.extra or {}, ensure_ascii=False), now,
                )
            )
        conn.executemany(
            "INSERT INTO social_trends"
            " (batch_id, platform, source_id, title, url, rank, heat_value,"
            "  rank_delta, summary, lang, extra, captured_at)"
            " VALUES (?,?,?,?,?,?,?,?,?,?,?,?)",
            rows,
        )
        conn.execute(
            "DELETE FROM social_trends WHERE captured_at < ?",
            (now - max(1, retention_days) * 86400,),
        )
        conn.commit()
        return len(rows)
    finally:
        conn.close()
