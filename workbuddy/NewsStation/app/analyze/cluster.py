"""事件聚类。

相似度 = 0.7 × 标题余弦 + 0.3 × 标签 Jaccard，行业无交集直接跳过。
不引 numpy：token 计数就是 dict，余弦手写更快也更可控。
"""

from __future__ import annotations

import math
import time
from collections import Counter
from dataclasses import dataclass, field

from .tokenize import weighted_counter

TAG_WEIGHT_DIMS = ("region", "industry", "type", "entity")


def cosine(a: Counter, b: Counter) -> float:
    if not a or not b:
        return 0.0
    common = set(a) & set(b)
    dot = sum(a[t] * b[t] for t in common)
    if dot == 0:
        return 0.0
    na = math.sqrt(sum(v * v for v in a.values()))
    nb = math.sqrt(sum(v * v for v in b.values()))
    return dot / (na * nb) if na and nb else 0.0


def jaccard(a: set[str], b: set[str]) -> float:
    if not a or not b:
        return 0.0
    inter = len(a & b)
    return inter / (len(a) + len(b) - inter)


@dataclass
class EventProfile:
    id: int
    title: str
    first_seen: int
    last_seen: int
    tags: set[str] = field(default_factory=set)
    counters: list[Counter] = field(default_factory=list)
    sources: set[str] = field(default_factory=set)
    article_count: int = 0


class Clusterer:
    """在内存里维护近 window 小时的事件画像，逐篇归簇后统一落库。"""

    def __init__(self, conn, threshold: float = 0.45, window_hours: int = 48,
                 max_members: int = 12):
        self.conn = conn
        self.threshold = threshold
        self.window = window_hours * 3600
        self.max_members = max_members
        self.events: dict[int, EventProfile] = {}
        self._load_recent()

    def _load_recent(self) -> None:
        since = int(time.time()) - self.window
        rows = self.conn.execute(
            "SELECT id, title, first_seen, last_seen, article_count FROM events"
            " WHERE last_seen >= ?",
            (since,),
        ).fetchall()
        for r in rows:
            self.events[r["id"]] = EventProfile(
                id=r["id"], title=r["title"], first_seen=r["first_seen"],
                last_seen=r["last_seen"], article_count=r["article_count"] or 0,
            )
        if not self.events:
            return
        # 近 N 篇文章的 token 与标签，构成事件画像
        ids = tuple(self.events)
        placeholders = ",".join("?" * len(ids))
        rows = self.conn.execute(
            f"SELECT event_id, id, title, summary, lang, source_id FROM articles"
            f" WHERE event_id IN ({placeholders}) ORDER BY published_at DESC",
            ids,
        ).fetchall()
        seen_per_event: Counter = Counter()
        for r in rows:
            ev = self.events[r["event_id"]]
            if seen_per_event[r["event_id"]] >= self.max_members:
                continue
            seen_per_event[r["event_id"]] += 1
            ev.counters.append(weighted_counter(r["title"], r["summary"] or "", r["lang"]))
            ev.sources.add(r["source_id"])
        tag_rows = self.conn.execute(
            f"SELECT event_id, dim, value FROM event_tags"
            f" WHERE event_id IN ({placeholders})",
            ids,
        ).fetchall()
        for r in tag_rows:
            self.events[r["event_id"]].tags.add(f"{r['dim']}:{r['value']}")

    def _candidates(self, tags: set[str]) -> list[EventProfile]:
        industries = {t for t in tags if t.startswith("industry:")}
        out = []
        for ev in self.events.values():
            ev_industries = {t for t in ev.tags if t.startswith("industry:")}
            # 行业门禁只在两边都识别出行业时生效：单边没标签就放行，
            # 否则 36% 没行业标签的文章会永远孤立。
            if industries and ev_industries and not (industries & ev_industries):
                continue
            out.append(ev)
        return out

    def assign(self, article: dict, counter: Counter, tags: set[str]) -> tuple[int | None, float]:
        """返回 (event_id, sim)。event_id 为 None 时表示应新建事件。"""
        best, best_sim = None, 0.0
        for ev in self._candidates(tags):
            title_sim = max((cosine(counter, c) for c in ev.counters), default=0.0)
            tag_sim = jaccard(tags, ev.tags)
            sim = 0.7 * title_sim + 0.3 * tag_sim
            if sim > best_sim:
                best, best_sim = ev, sim
        if best and best_sim >= self.threshold:
            return best.id, best_sim
        return None, best_sim

    def attach(self, event_id: int, article: dict, counter: Counter, tags: set[str],
               ts: int) -> None:
        ev = self.events[event_id]
        ev.counters.append(counter)
        if len(ev.counters) > self.max_members:
            ev.counters.pop(0)
        ev.tags |= tags
        ev.sources.add(article["source_id"])
        ev.article_count += 1
        ev.last_seen = max(ev.last_seen, ts)

    def create(self, event_id: int, article: dict, counter: Counter, tags: set[str],
               ts: int) -> None:
        self.events[event_id] = EventProfile(
            id=event_id, title=article["title"], first_seen=ts, last_seen=ts,
            tags=set(tags), counters=[counter], sources={article["source_id"]},
            article_count=1,
        )
