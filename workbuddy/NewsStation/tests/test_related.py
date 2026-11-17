"""事件相似度：相关事件必须真的相关，不能靠「同行业热门」凑数。"""

from __future__ import annotations

import json
import time

from app import db
from app.web.related import clean_words, related_events, similarity


def seed(conn):
    now = int(time.time())
    events = [
        # 1：目标事件
        (1, "Nvidia board meets as Jensen Huang faces ouster", json.dumps(
            ["nvidia", "board", "jensen", "huang", "ceo"]), now),
        # 2：同实体 + 多关键词重叠 → 应排第一
        (2, "Nvidia opens investigation after board report", json.dumps(
            ["nvidia", "board", "jensen", "huang", "investigation"]), now - 3600),
        # 3：只共享一个泛词，且实体不同 → 弱
        (3, "Apple quarterly results beat expectations", json.dumps(
            ["apple", "results", "china", "market"]), now - 7200),
        # 4：完全无关，只是同行业同地区同时间 → 必须被排除
        (4, "Local supermarket opens new branch", json.dumps(
            ["supermarket", "branch", "local"]), now - 60),
        # 5：中文同系列
        (5, "商务部：中美就降税保持密切沟通", json.dumps(
            ["商务部", "中美", "降税", "保持", "沟通"]), now),
        # 6：中文同系列
        (6, "外交部：中美双方保持着密切沟通", json.dumps(
            ["外交部", "中美", "保持", "密切", "沟通"]), now - 1800),
    ]
    conn.executemany(
        "INSERT INTO events (id, title, keywords, heat, source_count, last_seen)"
        " VALUES (?,?,?,?,?,?)",
        [(i, t, k, 10 - i, 3, ts) for i, t, k, ts in events],
    )
    tags = [
        (1, "entity", "nvidia"), (1, "industry", "tech"), (1, "region", "global"),
        (2, "entity", "nvidia"), (2, "industry", "tech"), (2, "region", "global"),
        (3, "industry", "tech"), (3, "region", "global"),
        (4, "industry", "tech"), (4, "region", "global"), (4, "type", "brief"),
        (5, "industry", "finance"), (5, "region", "cn"),
        (6, "industry", "finance"), (6, "region", "cn"),
    ]
    conn.executemany(
        "INSERT INTO event_tags (event_id, dim, value, locked) VALUES (?,?,?,0)", tags
    )
    conn.commit()
    return now


def test_clean_words_drops_noise():
    assert clean_words(["什么", "为了", "nvidia'", "中国", "9.9", "board"]) == {
        "nvidia", "中国", "board"
    }


def test_similarity_needs_content_overlap():
    """纯标签重合不能算相关。"""
    base = {
        "keywords": {"china", "market"}, "entity": set(), "title_tokens": {"china", "market"},
        "industry": {"tech"}, "type": set(), "region": {"global"}, "last_seen": 0,
    }
    other = dict(base, keywords={"local", "shop"}, entity=set(),
                 title_tokens={"local", "shop"}, last_seen=0)
    s, reasons, hard = similarity(base, other, 0)
    assert hard is False
    assert s < 1.5
    assert not reasons or all("同行业" in r or "同地区" in r for r in reasons)


def test_related_events_ranks_and_excludes(tmp_path):
    path = tmp_path / "t.db"
    db.init_db(path)
    conn = db.connect(path)
    try:
        now = seed(conn)
        rel = related_events(conn, 1, limit=5, now=now)
        ids = [r["id"] for r in rel]
        assert ids[0] == 2, "同实体 + 关键词重叠的应排第一"
        assert 4 not in ids, "只共享行业/地区/时间的无关事件不得出现"
        assert 3 not in ids, "仅一个泛词重合的不得出现"
        assert rel[0]["reason"], "必须给出关联理由"
        assert "nvidia" in rel[0]["reason"].lower() or "共同关键词" in rel[0]["reason"]
        assert all(r["score"] >= 1.5 for r in rel)

        zh = related_events(conn, 5, limit=5, now=now)
        assert [r["id"] for r in zh] == [6], "中文同系列事件应互相关联"
    finally:
        conn.close()


def test_related_events_empty_for_isolated_and_missing(tmp_path):
    path = tmp_path / "t.db"
    db.init_db(path)
    conn = db.connect(path)
    try:
        now = seed(conn)
        assert related_events(conn, 4, limit=5, now=now) == []
        assert related_events(conn, 99999, limit=5, now=now) == []
    finally:
        conn.close()
