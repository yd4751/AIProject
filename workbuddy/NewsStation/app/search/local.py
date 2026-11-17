"""站内全库检索：事件 / 文章 / 社媒热榜快照一次搜完。

中文不做分词（SQLite 没有中文分词器），用 instr(lower(...)) 子串匹配；
多个关键字取 AND，避免「人工智能 芯片」把不相关内容捞进来。
"""

from __future__ import annotations

import re
import sqlite3


def tokenize(query: str) -> list[str]:
    parts = [p.strip() for p in re.split(r"[\s,，|、;；]+", query or "") if p.strip()]
    return [p[:40] for p in parts[:6]]


def _clause(tokens: list[str], columns: list[str]) -> tuple[str, list[str]]:
    text = " || ' ' || ".join(f"coalesce({c}, '')" for c in columns)
    return " AND ".join(["instr(lower(" + text + "), lower(?)) > 0"] * len(tokens)), list(tokens)


def search_events(conn: sqlite3.Connection, tokens: list[str], limit: int) -> list[dict]:
    if not tokens:
        return []
    where, params = _clause(tokens, ["e.title", "e.keywords"])
    rows = conn.execute(
        "SELECT e.id, e.title, e.heat, e.momentum, e.article_count, e.source_count,"
        " e.last_seen, e.category FROM events e WHERE " + where
        + " ORDER BY e.heat DESC, e.last_seen DESC LIMIT ?",
        (*params, limit),
    ).fetchall()
    return [
        {
            "kind": "event", "id": r["id"], "title": r["title"],
            "url": f"/event/{r['id']}", "summary": r["category"] or "",
            "heat": round(r["heat"] or 0, 2), "ts": r["last_seen"],
            "source": f"{r['source_count']} 家媒体 · {r['article_count']} 篇",
        }
        for r in rows
    ]


def search_articles(conn: sqlite3.Connection, tokens: list[str], limit: int) -> list[dict]:
    if not tokens:
        return []
    where, params = _clause(tokens, ["a.title", "a.summary"])
    rows = conn.execute(
        "SELECT a.url, a.title, a.summary, a.source_id, a.published_at,"
        " a.fetched_at, a.lang, a.event_id FROM articles a WHERE " + where
        + " ORDER BY coalesce(a.published_at, a.fetched_at) DESC LIMIT ?",
        (*params, limit),
    ).fetchall()
    return [
        {
            "kind": "article", "title": r["title"], "url": r["url"],
            "summary": (r["summary"] or "")[:200], "source": r["source_id"],
            "lang": r["lang"], "event_id": r["event_id"],
            "ts": r["published_at"] or r["fetched_at"], "heat": 0,
        }
        for r in rows
    ]


def search_trends(conn: sqlite3.Connection, tokens: list[str], limit: int) -> list[dict]:
    if not tokens:
        return []
    row = conn.execute("SELECT MAX(batch_id) FROM social_trends").fetchone()
    batch = row[0] if row else None
    if batch is None:
        return []
    where, params = _clause(tokens, ["s.title", "s.summary"])
    rows = conn.execute(
        "SELECT s.platform, s.title, s.url, s.summary, s.rank, s.heat_value,"
        " s.captured_at FROM social_trends s WHERE s.batch_id = ? AND " + where
        + " ORDER BY s.heat_value DESC LIMIT ?",
        (batch, *params, limit),
    ).fetchall()
    return [
        {
            "kind": "trend", "title": r["title"], "url": r["url"],
            "summary": (r["summary"] or "")[:200], "source": r["platform"],
            "rank": r["rank"], "heat": r["heat_value"] or 0,
            "ts": r["captured_at"], "batch_id": batch,
        }
        for r in rows
    ]


def hot_terms(conn: sqlite3.Connection, limit: int = 12) -> list[str]:
    """空查询时的热词建议：优先最新一批社媒热榜，其次高热度事件。"""
    row = conn.execute("SELECT MAX(batch_id) FROM social_trends").fetchone()
    out: list[str] = []
    if row and row[0] is not None:
        for r in conn.execute(
            "SELECT title FROM social_trends WHERE batch_id = ?"
            " ORDER BY heat_value DESC LIMIT ?", (row[0], limit)
        ):
            t = (r["title"] or "").strip()
            if t and t not in out:
                out.append(t)
    if len(out) < limit:
        for r in conn.execute(
            "SELECT title FROM events ORDER BY heat DESC, last_seen DESC LIMIT ?", (limit,)
        ):
            t = (r["title"] or "").strip()
            if t and t not in out:
                out.append(t)
            if len(out) >= limit:
                break
    return out[:limit]


def search_all(conn: sqlite3.Connection, tokens: list[str], limit: int) -> dict:
    events = search_events(conn, tokens, limit)
    articles = search_articles(conn, tokens, limit)
    trends = search_trends(conn, tokens, limit)
    # 同一 URL 既被注入成文章、又在热榜快照里时只保留文章那条
    seen = {a["url"] for a in articles}
    trends = [t for t in trends if t["url"] not in seen]
    return {
        "events": events, "articles": articles, "trends": trends,
        "counts": {"events": len(events), "articles": len(articles), "trends": len(trends)},
    }
