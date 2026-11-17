"""分析管道编排：分词 → 四维标签 → 事件聚类 → 热度。

只处理 analyzed=0 的文章，抓取与分析的耦合点仅这一张表。
"""

from __future__ import annotations

import json
import time
from collections import Counter

from .. import db
from ..ingest import registry
from . import heat
from .cluster import Clusterer, cosine
from .tagger import DIM_CAPS, Tag, get_tagger, sync_tags
from .tokenize import top_keywords, weighted_counter

HOUR = 3600


def _now() -> int:
    return int(time.time())


def process(db_path, settings, now: int | None = None) -> dict:
    now = now or _now()
    conn = db.connect(db_path)
    stats = {"articles": 0, "events_new": 0, "events_updated": 0, "tags": 0,
             "purged": 0, "seconds": 0.0}
    started = time.perf_counter()
    try:
        tagger = get_tagger()
        sync_tags(conn)
        stats["purged"] = _purge(conn, now, settings.analyze.retention_days)

        rows = conn.execute(
            "SELECT id, title, summary, lang, source_id, published_at, fetched_at"
            " FROM articles WHERE analyzed = 0"
            " ORDER BY COALESCE(published_at, fetched_at) ASC LIMIT ?",
            (settings.analyze.batch_size,),
        ).fetchall()
        if not rows:
            return stats

        clusterer = Clusterer(
            conn,
            threshold=settings.analyze.cluster_threshold,
            window_hours=settings.analyze.cluster_window_hours,
            max_members=settings.analyze.cluster_max_members,
        )

        touched: set[int] = set()
        source_region = _source_regions()
        for row in rows:
            ts = row["published_at"] or row["fetched_at"]
            lang = row["lang"] or "zh"
            tags = tagger.tag(row["title"], row["summary"] or "", lang)
            # 源属地兜底：本地媒体的本地稿不会在标题里写"中国"，纯词典召回不够
            fallback = source_region.get(row["source_id"])
            if fallback and not any(t.dim == "region" for t in tags):
                tags.append(
                    Tag("region", fallback,
                        tagger.labels.get(("region", fallback), fallback), 0.55, "source")
                )
            tag_set = {f"{t.dim}:{t.value}" for t in tags}
            counter = weighted_counter(row["title"], row["summary"] or "", lang)

            if tags:
                conn.executemany(
                    "INSERT OR REPLACE INTO article_tags (article_id, dim, value, confidence, origin)"
                    " VALUES (?,?,?,?,?)",
                    [(row["id"], t.dim, t.value, t.confidence, t.origin) for t in tags],
                )
                stats["tags"] += len(tags)

            article = {"title": row["title"], "source_id": row["source_id"]}
            event_id, _sim = clusterer.assign(article, counter, tag_set)
            if event_id is None:
                cur = conn.execute(
                    "INSERT INTO events (title, category, heat, article_count, source_count,"
                    " keywords, first_seen, last_seen) VALUES (?,?,?,?,?,?,?,?)",
                    (_short_title(row["title"]), _category(tags), 0.0, 1, 1,
                     json.dumps(top_keywords(counter), ensure_ascii=False), ts, ts),
                )
                event_id = cur.lastrowid
                clusterer.create(event_id, article, counter, tag_set, ts)
                stats["events_new"] += 1
            else:
                clusterer.attach(event_id, article, counter, tag_set, ts)
                stats["events_updated"] += 1
            touched.add(event_id)

            conn.execute(
                "UPDATE articles SET event_id = ?, category = ?, analyzed = 1 WHERE id = ?",
                (event_id, _category(tags), row["id"]),
            )
            stats["articles"] += 1

        for event_id in touched:
            _refresh_event(conn, event_id, now, settings)
        _decay_stale(conn, now, settings, skip=touched)
        conn.commit()
        stats["seconds"] = round(time.perf_counter() - started, 2)
        return stats
    finally:
        conn.close()


def _source_regions() -> dict[str, str]:
    """source_id -> 源属地。没加载过源清单时先加载一次，脚本/测试环境也能直接用。"""
    if not registry.all_sources():
        registry.load_sources_yaml()
    out = {s.id: s.region for s in registry.all_sources() if getattr(s, "region", None)}
    # 社媒源是独立注册表，只在已显式装载时合并，避免脚本/测试被塞进网络源
    from app.ingest import social

    out.update(
        {s.id: s.region for s in social.all_sources(enabled_only=False) if getattr(s, "region", None)}
    )
    return out


def _short_title(title: str, limit: int = 60) -> str:
    title = " ".join((title or "").split())
    return title if len(title) <= limit else title[: limit - 1] + "…"


def _purge(conn, now: int, retention_days: int) -> int:
    """清掉超期文章与空事件。早期抓取攒下的陈年旧闻会污染聚类和热度，必须定期清。"""
    cutoff = now - retention_days * 24 * HOUR
    cur = conn.execute(
        "DELETE FROM articles WHERE COALESCE(published_at, fetched_at) < ?", (cutoff,)
    )
    removed = cur.rowcount or 0
    if removed:
        conn.execute(
            "DELETE FROM article_tags WHERE article_id NOT IN (SELECT id FROM articles)"
        )
        conn.execute("DELETE FROM events WHERE id NOT IN (SELECT DISTINCT event_id"
                     " FROM articles WHERE event_id IS NOT NULL)")
        conn.execute("DELETE FROM event_tags WHERE event_id NOT IN (SELECT id FROM events)")
    return removed


def _category(tags) -> str | None:
    industries = [t for t in tags if t.dim == "industry"]
    if not industries:
        return None
    return max(industries, key=lambda t: t.confidence).value


def _refresh_event(conn, event_id: int, now: int, settings) -> None:
    rows = conn.execute(
        "SELECT id, title, summary, lang, source_id,"
        " COALESCE(published_at, fetched_at) AS ts"
        " FROM articles WHERE event_id = ? ORDER BY ts DESC",
        (event_id,),
    ).fetchall()
    if not rows:
        conn.execute("DELETE FROM events WHERE id = ?", (event_id,))
        return

    times = [r["ts"] for r in rows if r["ts"]]
    sources = {r["source_id"] for r in rows}
    h = heat.compute(times, sources, now, settings.analyze.heat_half_life_hours,
                     settings.analyze.heat_window_hours)

    # 代表标题用「离簇心最近的成员标题」：最早那条常常是快讯，不具代表性
    sample = rows[: settings.analyze.cluster_max_members]
    counters = [
        weighted_counter(r["title"], r["summary"] or "", r["lang"]) for r in sample
    ]
    best_idx, best_score = 0, -1.0
    for i, ci in enumerate(counters):
        score = sum(cosine(ci, cj) for j, cj in enumerate(counters) if i != j)
        if score > best_score:
            best_idx, best_score = i, score
    title = _short_title(sample[best_idx]["title"]) if len(sample) > 1 else _short_title(rows[0]["title"])

    merged: Counter = Counter()
    for c in counters:
        merged.update(c)

    conn.execute(
        "UPDATE events SET title=?, heat=?, momentum=?, article_count=?, source_count=?,"
        " keywords=?, first_seen=?, last_seen=?, category=COALESCE(?, category) WHERE id=?",
        (
            title, h["heat"], h["momentum"], len(rows), len(sources),
            json.dumps(top_keywords(merged), ensure_ascii=False),
            min(times) if times else now, max(times) if times else now,
            _dominant_industry(conn, event_id), event_id,
        ),
    )
    _refresh_event_tags(conn, event_id)


def _dominant_industry(conn, event_id: int) -> str | None:
    row = conn.execute(
        "SELECT value, COUNT(*) n FROM article_tags WHERE dim='industry' AND article_id IN"
        " (SELECT id FROM articles WHERE event_id=?) GROUP BY value ORDER BY n DESC LIMIT 1",
        (event_id,),
    ).fetchone()
    return row["value"] if row else None


def _refresh_event_tags(conn, event_id: int) -> None:
    """簇内标签按命中数聚合；locked=1 的人工标签原样保留，不被覆盖。"""
    rows = conn.execute(
        "SELECT dim, value, COUNT(*) n FROM article_tags"
        " WHERE article_id IN (SELECT id FROM articles WHERE event_id=?)"
        " GROUP BY dim, value",
        (event_id,),
    ).fetchall()
    # locked 三态：1=人工锁定（保留）、-1=人工屏蔽（不重建也不展示）、0=自动
    overrides = {
        (r["dim"], r["value"]): r["locked"]
        for r in conn.execute(
            "SELECT dim, value, locked FROM event_tags WHERE event_id=? AND locked<>0",
            (event_id,),
        )
    }
    locked = {k for k, v in overrides.items() if v == 1}
    blocked = {k for k, v in overrides.items() if v == -1}
    by_dim: dict[str, list[tuple[str, int]]] = {}
    for r in rows:
        key = (r["dim"], r["value"])
        if key in locked or key in blocked:
            continue
        by_dim.setdefault(r["dim"], []).append((r["value"], r["n"]))

    # 实体门槛：一篇报道顺带提到一次的公司名，不该代表整个事件。
    # 大簇要求至少 2 篇提及，小簇允许 1 篇。
    article_total = conn.execute(
        "SELECT COUNT(*) FROM articles WHERE event_id = ?", (event_id,)
    ).fetchone()[0]
    entity_floor = 2 if article_total >= 3 else 1
    if "entity" in by_dim:
        by_dim["entity"] = [(v, n) for v, n in by_dim["entity"] if n >= entity_floor]

    keep = {d: sorted(v, key=lambda x: -x[1])[: DIM_CAPS.get(d, 2)] for d, v in by_dim.items()}
    conn.execute("DELETE FROM event_tags WHERE event_id=? AND locked=0", (event_id,))
    conn.executemany(
        "INSERT OR REPLACE INTO event_tags (event_id, dim, value, hit_count, locked)"
        " VALUES (?,?,?,?,0)",
        [(event_id, dim, value, n) for dim, items in keep.items() for value, n in items],
    )


def _decay_stale(conn, now: int, settings, skip: set[int]) -> None:
    """没有新文章的事件热度也要随时间衰减，否则旧热点永远挂在榜上。"""
    rows = conn.execute(
        "SELECT id FROM events WHERE last_seen >= ?", (now - 72 * HOUR,)
    ).fetchall()
    for r in rows:
        if r["id"] in skip:
            continue
        times = [
            x["ts"]
            for x in conn.execute(
                "SELECT COALESCE(published_at, fetched_at) ts FROM articles WHERE event_id=?",
                (r["id"],),
            )
        ]
        sources = {
            x["source_id"]
            for x in conn.execute(
                "SELECT source_id FROM articles WHERE event_id=?", (r["id"],)
            )
        }
        h = heat.compute(times, sources, now, settings.analyze.heat_half_life_hours,
                         settings.analyze.heat_window_hours)
        conn.execute(
            "UPDATE events SET heat=?, momentum=?, article_count=?, source_count=? WHERE id=?",
            (h["heat"], h["momentum"], len(times), len(sources), r["id"]),
        )


def rebuild(db_path, settings) -> dict:
    """清空分析结果重跑。改了词典或调了公式之后用，避免新旧规则混在一张表里。"""
    conn = db.connect(db_path)
    try:
        conn.execute("UPDATE articles SET analyzed = 0, event_id = NULL, category = NULL")
        conn.execute("DELETE FROM event_tags")
        conn.execute("DELETE FROM article_tags")
        conn.execute("DELETE FROM events")
        conn.commit()
    finally:
        conn.close()
    return process(db_path, settings)


def main() -> None:
    import argparse

    from .. import config, runtime

    parser = argparse.ArgumentParser(description="NewsStation 分析管道")
    parser.add_argument("--db", default=None, help="数据库路径，默认取 config.yaml（相对运行目录）")
    parser.add_argument("--rebuild", action="store_true", help="清空分析结果后重跑")
    args = parser.parse_args()

    settings = config.load()
    db_path = str(runtime.resolve(args.db)) if args.db else str(config.abs_db_path(settings))
    db.init_db(db_path)
    stats = rebuild(db_path, settings) if args.rebuild else process(db_path, settings)
    print(
        f"文章 {stats['articles']} 篇 → 新事件 {stats['events_new']}、"
        f"并入 {stats['events_updated']}，标签 {stats['tags']} 条，"
        f"清理过期 {stats['purged']} 篇，耗时 {stats['seconds']}s"
    )


if __name__ == "__main__":
    main()
