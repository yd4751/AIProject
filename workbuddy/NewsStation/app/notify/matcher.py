"""订阅匹配：一轮抓取 + 分析结束后，只对新入库文章跑订阅规则。

两类订阅（可叠加，叠加时取 AND）：
- 关键字：title / summary 命中任一关键字（英文大小写不敏感，支持逗号或竖线分隔）
- 标签：文章所属事件的 event_tags 必须满足 tag_filter 的每个维度（维度内 OR，维度间 AND）

去重靠 UNIQUE(subscription_id, article_id)，重复执行不会重复计未读。
"""

from __future__ import annotations

import json
import re
import time

from app import db

DIM_LABELS = {"region": "地区", "industry": "行业", "type": "类型", "entity": "实体"}


def split_keywords(raw: str | None) -> list[str]:
    return [w.strip().lower() for w in re.split(r"[,|，]", raw or "") if w.strip()]


def parse_tag_filter(raw) -> dict[str, list[str]]:
    if not raw:
        return {}
    try:
        data = json.loads(raw) if isinstance(raw, str) else dict(raw)
    except (ValueError, TypeError):
        return {}
    out: dict[str, list[str]] = {}
    for dim, values in data.items():
        vals = values if isinstance(values, list) else [values]
        cleaned = [str(v) for v in vals if str(v).strip()]
        if cleaned:
            out[str(dim)] = cleaned
    return out


def describe(sub) -> str:
    """订阅的人类可读描述，页面与邮件共用。"""
    parts = []
    keyword = (sub.get("keyword") or "").strip() if isinstance(sub, dict) else (sub["keyword"] or "").strip()
    raw_tf = sub.get("tag_filter") if isinstance(sub, dict) else sub["tag_filter"]
    if keyword:
        parts.append("关键字「" + keyword + "」")
    tf = parse_tag_filter(raw_tf)
    if tf:
        bits = [f"{DIM_LABELS.get(d, d)}={'/'.join(v)}" for d, v in tf.items()]
        parts.append("标签 " + "; ".join(bits))
    return " + ".join(parts) or "（空订阅）"


def _keyword_ok(sub: dict, article: dict) -> bool:
    kws = split_keywords(sub.get("keyword"))
    if not kws:
        return True  # 这一半不设限
    text = f"{article['title']} {article.get('summary') or ''}".lower()
    return any(k in text for k in kws)


def _tags_ok(conn, sub: dict, article: dict) -> bool:
    tf = parse_tag_filter(sub.get("tag_filter"))
    if not tf:
        return True
    event_id = article.get("event_id")
    if not event_id:
        return False
    for dim, values in tf.items():
        ph = ",".join("?" * len(values))
        hit = conn.execute(
            f"SELECT 1 FROM event_tags WHERE event_id=? AND dim=? AND value IN ({ph})"
            " AND locked <> -1 LIMIT 1",
            (event_id, dim, *values),
        ).fetchone()
        if not hit:
            return False
    return True


def match_new(db_path, min_article_id: int, now: int | None = None) -> dict:
    """匹配 id > min_article_id 的新文章，返回本轮新命中的 hit id 列表（供邮件摘要用）。"""
    now = now or int(time.time())
    stats = {
        "new_articles": 0, "subscriptions": 0, "new_hits": 0,
        "hit_ids": [], "subscriptions_hit": 0,
    }
    conn = db.connect(db_path)
    try:
        subs = [
            dict(r)
            for r in conn.execute("SELECT * FROM subscriptions ORDER BY id")
        ]
        subs = [
            s for s in subs
            if split_keywords(s.get("keyword")) or parse_tag_filter(s.get("tag_filter"))
        ]
        stats["subscriptions"] = len(subs)
        if not subs:
            return stats

        articles = [
            dict(r)
            for r in conn.execute(
                "SELECT a.id, a.title, a.url, a.summary, a.event_id,"
                " COALESCE(e.heat, 0) AS heat"
                " FROM articles a LEFT JOIN events e ON e.id = a.event_id"
                " WHERE a.id > ? ORDER BY a.id",
                (min_article_id,),
            )
        ]
        stats["new_articles"] = len(articles)
        touched: set[int] = set()
        for a in articles:
            for s in subs:
                if not _keyword_ok(s, a) or not _tags_ok(conn, s, a):
                    continue
                cur = conn.execute(
                    "INSERT OR IGNORE INTO subscription_hits"
                    " (subscription_id, article_id, event_id, title, url, heat, matched_at, unread)"
                    " VALUES (?,?,?,?,?,?,?,1)",
                    (s["id"], a["id"], a["event_id"], a["title"], a["url"], a["heat"], now),
                )
                if not cur.rowcount:
                    continue
                stats["new_hits"] += 1
                stats["hit_ids"].append(cur.lastrowid)
                touched.add(s["id"])
                conn.execute(
                    "UPDATE subscriptions SET unread_count = unread_count + 1,"
                    " last_matched_at = ? WHERE id = ?",
                    (now, s["id"]),
                )
        stats["subscriptions_hit"] = len(touched)
        conn.commit()
        return stats
    finally:
        conn.close()


def unread_total(conn) -> int:
    return conn.execute(
        "SELECT COALESCE(SUM(unread_count), 0) FROM subscriptions"
    ).fetchone()[0]
