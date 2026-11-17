"""事件相似度：给详情页挑出真正相关的事件，并说明「为什么相关」。

旧实现只按「共享主行业 + 热度」取 5 条，等于把同行业的热门事件随便塞进来，
看起来毫无关联。这里改为加权相似度：

  关键词重叠（TF-IDF 词，Jaccard）      权重最高 —— 话题是否一致
  实体标签共享（如 nvidia / 商务部）     强信号 —— 说的是同一个主体
  标题分词重叠                           捕捉同一事件的不同表述
  行业 / 类型 / 地区标签                  逐级递减的弱信号
  时间接近度                             同期事件优先

只有达到阈值的才返回；宁可少显示，也不凑无关事件。结果带 reason 供页面展示。
"""

from __future__ import annotations

import json
import math
import re
import sqlite3
from collections import Counter

from app.analyze.tokenize import STOPWORDS, tokenize

WINDOW_SECONDS = 10 * 86400     # 候选时间窗：近 10 天 + 共享实体的历史事件
CANDIDATE_LIMIT = 400
MIN_SCORE = 1.5
# TF-IDF 抽词仍会漏进一些无信息量的口语词/数字，只用于相似度比对
EXTRA_STOP = {
    "什么", "为了", "不再", "经历", "准备", "数据", "问题", "情况", "方面",
    "一个", "没有", "可以", "如何", "怎么", "是否", "哪些", "原因", "内容",
    "含量", "有多高", "继续", "靠什么", "靠", "赢", "有多", "有多少",
}
_NUM_RE = re.compile(r"^[\d.,%\s]+$")
LABEL_DIM = {"entity": "实体", "industry": "行业", "type": "类型", "region": "地区"}
# 标签只是佐证，不能单靠「同行业+同地区」凑出相关事件
DIM_WEIGHT = {"entity": 2.2, "industry": 0.35, "type": 0.25, "region": 0.1}


def clean_words(words) -> set[str]:
    """关键词表里混进了「什么」「为了」和带撇号的英文残片，先净化再比。"""
    out: set[str] = set()
    for w in words or []:
        s = str(w).strip().lower().strip("'’\".,;:!?()[]")
        if len(s) < 2 or s in STOPWORDS or s in EXTRA_STOP or _NUM_RE.match(s):
            continue
        out.add(s)
    return out


def _idf_weighted_jaccard(a: set, b: set, df: Counter, n: int) -> tuple[float, set]:
    """共享一个罕见词，比共享「中国」「发展」这种满库都是的词更有说服力。"""
    shared = a & b
    if not shared:
        return 0.0, shared
    w = lambda x: math.log(1 + n / (1 + df[x]))
    denom = math.sqrt(sum(w(x) for x in a) * sum(w(x) for x in b)) or 1
    return sum(w(x) for x in shared) / denom, shared


def similarity(base: dict, other: dict, now: int,
               df: Counter | None = None, n: int = 1) -> tuple[float, list[str], bool]:
    score = 0.0
    reasons: list[tuple[float, str]] = []
    hard = False   # 是否存在「内容层面」的实质重叠

    df = df or Counter()
    kw_sim, shared_kw = _idf_weighted_jaccard(base["keywords"], other["keywords"], df, n)
    if kw_sim:
        v = 3.0 * kw_sim
        score += v
        hard = True
        reasons.append((v, "共同关键词 " + "、".join(sorted(shared_kw)[:3])))

    shared_ent = base["entity"] & other["entity"]
    if shared_ent:
        v = 2.2 + 0.6 * (min(len(shared_ent), 3) - 1)
        score += v
        hard = True
        reasons.append((v, "同一实体 " + "、".join(sorted(shared_ent)[:2])))

    shared_t = base["title_tokens"] & other["title_tokens"]
    title_sim = _jaccard(base["title_tokens"], other["title_tokens"])
    if title_sim:
        v = 1.2 * title_sim
        score += v
        hard = True
        reasons.append((v, "标题相近 " + "、".join(sorted(shared_t)[:3])))

    for dim, w in DIM_WEIGHT.items():
        if dim == "entity":
            continue
        shared = base[dim] & other[dim]
        if shared:
            v = w * min(len(shared), 2)
            score += v
            reasons.append((v, "同" + LABEL_DIM[dim] + " " + "、".join(sorted(shared)[:2])))

    # 时间接近只是加分项，不能单靠「同行业+同地区+同期」把不相干事件凑进来
    if hard:
        gap = abs((base["last_seen"] or now) - (other["last_seen"] or now))
        if gap <= WINDOW_SECONDS:
            score += 0.5 * (1 - gap / WINDOW_SECONDS)

    reasons.sort(key=lambda x: -x[0])
    return score, [r for _, r in reasons[:2]], hard


def _jaccard(a: set, b: set) -> float:
    if not a or not b:
        return 0.0
    return len(a & b) / math.sqrt(len(a) * len(b))


def _profile(row: sqlite3.Row, tags: dict[int, dict[str, set]]) -> dict:
    eid = row["id"]
    t = tags.get(eid, {})
    return {
        "id": eid,
        "title": row["title"],
        "heat": row["heat"] or 0,
        "source_count": row["source_count"] or 0,
        "last_seen": row["last_seen"] or 0,
        "keywords": clean_words(json.loads(row["keywords"] or "[]")),
        "title_tokens": set(tokenize(row["title"] or "")),
        "entity": t.get("entity", set()),
        "industry": t.get("industry", set()),
        "type": t.get("type", set()),
        "region": t.get("region", set()),
    }


def related_events(conn: sqlite3.Connection, event_id: int,
                   limit: int = 5, now: int | None = None) -> list[dict]:
    """返回按相似度降序的相关事件；低于 MIN_SCORE 的不返回（不凑数）。"""
    import time

    now = now or int(time.time())
    row = conn.execute(
        "SELECT id, title, keywords, last_seen, heat, source_count FROM events WHERE id = ?",
        (event_id,),
    ).fetchone()
    if not row:
        return []

    base_tags: dict[str, set] = {d: set() for d in DIM_WEIGHT}
    for t in conn.execute(
        "SELECT dim, value FROM event_tags WHERE event_id = ? AND locked <> -1", (event_id,)
    ):
        base_tags.setdefault(t["dim"], set()).add(t["value"])
    base = _profile(row, {event_id: base_tags})

    # 候选：时间窗内的全部事件，外加历史上共享实体的事件（实体是最强信号，不受时间窗限制）
    ents = list(base["entity"])
    shared_entity_ids: list[int] = []
    if ents:
        ph = ",".join("?" * len(ents))
        shared_entity_ids = [
            r["event_id"] for r in conn.execute(
                f"SELECT DISTINCT event_id FROM event_tags"
                f" WHERE dim='entity' AND locked <> -1 AND value IN ({ph})"
                f" AND event_id <> ? LIMIT 200",
                (*ents, event_id),
            )
        ]
    recent = [
        r["id"] for r in conn.execute(
            "SELECT id FROM events WHERE id <> ? AND last_seen >= ?"
            " ORDER BY last_seen DESC LIMIT ?",
            (event_id, now - WINDOW_SECONDS, CANDIDATE_LIMIT),
        )
    ]
    cand_ids = list(dict.fromkeys(shared_entity_ids + recent))[:CANDIDATE_LIMIT]
    if not cand_ids:
        return []

    ph = ",".join("?" * len(cand_ids))
    rows = conn.execute(
        f"SELECT id, title, keywords, last_seen, heat, source_count FROM events"
        f" WHERE id IN ({ph})", cand_ids,
    ).fetchall()
    tags: dict[int, dict[str, set]] = {}
    for t in conn.execute(
        f"SELECT event_id, dim, value FROM event_tags"
        f" WHERE event_id IN ({ph}) AND locked <> -1", cand_ids,
    ):
        tags.setdefault(t["event_id"], {}).setdefault(t["dim"], set()).add(t["value"])

    # 文档频率：候选集里每个关键词出现在几篇事件里，用于压低「中国」这类泛词
    df: Counter = Counter()
    for r in rows:
        df.update(clean_words(json.loads(r["keywords"] or "[]")))

    scored: list[tuple[float, dict, list[str]]] = []
    for r in rows:
        other = _profile(r, tags)
        s, reasons, hard = similarity(base, other, now, df, len(rows))
        if not hard or s < MIN_SCORE:   # 必须有关键词/实体/标题的实质重叠
            continue
        scored.append((s, other, reasons))

    scored.sort(key=lambda x: (-x[0], -x[1]["heat"]))
    out = []
    for s, other, reasons in scored[:limit]:
        out.append({
            "id": other["id"], "title": other["title"],
            "heat": round(other["heat"], 2), "source_count": other["source_count"],
            "score": round(s, 2),
            "reason": " · ".join(reasons) if reasons else "内容相近",
        })
    return out
