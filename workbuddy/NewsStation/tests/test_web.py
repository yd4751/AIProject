"""M3 展示层验收：四视图、组合筛选、点阵着色、详情标签锁定。

用临时库 + 独立 FastAPI app（不跑 lifespan，不碰调度与生产库）。
"""

from __future__ import annotations

import re
import time

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from app import config, db
from app.analyze import pipeline
from app.web import queries as q
from app.web.routes import router


@pytest.fixture()
def env(tmp_path):
    db_path = tmp_path / "web.db"
    db.init_db(db_path)
    conn = db.connect(db_path)
    now = int(time.time())

    tags = [
        ("region", "cn", "中国", None), ("region", "hk", "中国香港", "cn"),
        ("region", "mo", "中国澳门", "cn"), ("region", "tw", "中国台湾", "cn"),
        ("region", "us", "美国", None), ("region", "global", "全球", None),
        ("industry", "tech", "科技", None), ("industry", "finance", "财经", None),
        ("industry", "politics", "政治", None),
        ("type", "earnings", "财报业绩", None), ("type", "product", "产品发布", None),
        ("entity", "apple", "苹果", None),
    ]
    conn.executemany(
        "INSERT INTO tags(dim,value,label,aliases,parent) VALUES(?,?,?,'',?)",
        [(d, v, label, parent) for d, v, label, parent in tags],
    )

    events = [
        (1, "英伟达财报超预期，AI 芯片需求强劲", "tech", 5.0, 0.8, 3, 2),
        (2, "港股科技板块走强", "finance", 3.0, -0.3, 2, 1),
        (3, "美国新芯片法案公布", "tech", 2.0, 0.0, 1, 1),
        (4, "科创100ETF博时标的指数涨超2%", "finance", 4.0, 1.0, 4, 1),
        (5, "中国台湾半导体展会开幕", "tech", 1.5, 0.2, 1, 1),
    ]
    conn.executemany(
        "INSERT INTO events(id,title,category,heat,momentum,article_count,source_count,"
        " keywords,first_seen,last_seen) VALUES(?,?,?,?,?,?,?,'[]',?,?)",
        [(i, t, c, h, m, n, s, now - 7200, now - 1800) for i, t, c, h, m, n, s in events],
    )
    event_tags = [
        (1, "region", "cn", 3), (1, "industry", "tech", 3), (1, "type", "earnings", 2),
        (2, "region", "hk", 2), (2, "industry", "finance", 2),
        (3, "region", "us", 1), (3, "industry", "tech", 1), (3, "type", "product", 1),
        (4, "industry", "finance", 4),
        (5, "region", "tw", 1), (5, "industry", "tech", 1),
    ]
    conn.executemany(
        "INSERT INTO event_tags(event_id,dim,value,hit_count,locked) VALUES(?,?,?,?,0)",
        event_tags,
    )

    articles = []
    article_tags = []
    aid = 1
    for eid, n in ((1, 3), (2, 2), (3, 1), (4, 4), (5, 1)):
        for k in range(n):
            articles.append(
                (aid, f"src-{(eid + k) % 3}", f"https://example.com/{aid}",
                 f"报道 {eid}-{k}", "摘要", "zh", now - 3600 - k * 600,
                 now - 3600 - k * 600, eid, 1)
            )
            article_tags.append((aid, "industry", "tech" if eid in (1, 3, 5) else "finance", 0.9, "dict"))
            if eid == 1:
                article_tags.append((aid, "region", "cn", 0.9, "dict"))
                article_tags.append((aid, "type", "earnings", 0.9, "dict"))
            aid += 1
    conn.executemany(
        "INSERT INTO articles(id,source_id,url,title,summary,lang,published_at,fetched_at,"
        " event_id,analyzed) VALUES(?,?,?,?,?,?,?,?,?,?)",
        articles,
    )
    conn.executemany(
        "INSERT INTO article_tags(article_id,dim,value,confidence,origin) VALUES(?,?,?,?,?)",
        article_tags,
    )
    conn.commit()

    settings = config.Settings(db_path=str(db_path))
    app = FastAPI()
    app.state.settings = settings
    app.include_router(router)
    client = TestClient(app)
    yield client, conn, now
    conn.close()


def test_four_views_open(env):
    client, _, _ = env
    markers = {
        "list": "热度榜",
        "quad": "四象限 Top 3",
        "dot": "报道点阵",
        "matrix": "行业热力矩阵",
    }
    for view, marker in markers.items():
        r = client.get(f"/?view={view}")
        assert r.status_code == 200, view
        assert marker in r.text, view
    # 非法 view 回退默认，且筛选参数保留在分享链接里
    r = client.get("/?view=nope&region=cn")
    assert r.status_code == 200
    assert "热度榜" in r.text and "region=cn" in r.text


def test_other_pages_open(env):
    client, _, _ = env
    assert client.get("/industries").status_code == 200
    assert "全行业热力矩阵" in client.get("/industries").text
    assert client.get("/industry/tech").status_code == 200
    assert client.get("/industry/unknown", follow_redirects=False).status_code == 303
    assert client.get("/event/1").status_code == 200
    assert "四维标签" in client.get("/event/1").text
    assert client.get("/sources").status_code == 200
    assert client.get("/api/stats").status_code == 200


def test_combined_filter_matches_sql(env):
    """页面/API 的筛选结果必须与同一份 SQL 直查一致。"""
    client, conn, _ = env
    filters = {"region": "cn", "industry": "tech", "type": "earnings"}
    where, params = q.build_where(conn, filters)
    direct = [
        r[0]
        for r in conn.execute(
            "SELECT e.id FROM events e WHERE " + " AND ".join(where)
            + " ORDER BY e.heat DESC, e.last_seen DESC",
            params,
        )
    ]
    payload = client.get("/api/events?region=cn&industry=tech&type=earnings&limit=50").json()
    assert payload["count"] == len(direct)
    assert [e["id"] for e in payload["events"]] == direct

    # 首页榜单同样用 build_where，命中的事件必须出现在卡流里
    r = client.get("/?view=list&region=cn&industry=tech&type=earnings")
    assert r.status_code == 200
    assert "英伟达财报超预期" in r.text
    assert "港股科技板块" not in r.text

    # 选中国自动含港澳台：region=cn 应命中 hk / tw 的事件
    _, p = q.build_where(conn, {"region": "cn", "industry": "", "type": ""})
    ids = {
        r[0]
        for r in conn.execute(
            "SELECT DISTINCT event_id FROM event_tags WHERE dim='region' AND value IN (" +
            ",".join("?" * (len(p) - 1)) + ")", p[1:]
        )
    }
    assert {2, 5} <= ids


def test_boilerplate_excluded_from_ranking(env):
    client, conn, _ = env
    events = q.load_events(conn, {})
    assert any(e["boilerplate"] for e in events)
    visible = q.visible_events(events)
    assert all(not e["boilerplate"] for e in visible)
    r = client.get("/")
    assert "已剔除 1 个单源行情模板事件" in r.text
    assert "科创100ETF" not in r.text  # 不进榜也不进卡流
    assert client.get("/event/4").status_code == 200  # 但详情页仍可访问


def test_dot_color_mapping(env):
    """切换着色维度后，每个点的颜色都等于该维度取值的调色板颜色。"""
    client, conn, _ = env
    events = q.visible_events(q.load_events(conn, {}))
    for dim in ("industry", "region", "type"):
        dot = q.dot_data(conn, {}, dim, events)
        palette = {l["value"]: l["color"] for l in dot["legend"]}
        assert palette, dim
        assert len(set(palette.values())) == len(palette), f"{dim} 图例颜色不唯一"
        for p in dot["points"]:
            if p["value"]:
                assert p["color"] == palette[p["value"]], (dim, p["value"])
            else:
                assert p["color"] == dot["none_color"]
        r = client.get(f"/?view=dot&color={dim}")
        assert r.status_code == 200
        assert "报道点阵" in r.text
        assert f"color={dim}" in r.text  # 着色 chip 链接保留当前维度
        assert re.search(r'data-value="[^"]+"', r.text), dim

    # 行业维度用固定 12 色调色板
    dot = q.dot_data(conn, {}, "industry", events)
    for l in dot["legend"]:
        if l["value"] in q.INDUSTRY_COLORS:
            assert l["color"] == q.INDUSTRY_COLORS[l["value"]]


def test_event_tag_lock_survives_refresh(env):
    """人工添加/锁定/屏蔽的标签在 pipeline 刷新后依然生效。"""
    client, conn, _ = env

    # 添加（locked=1）
    r = client.post("/event/1/tags?action=add&v=entity:apple", follow_redirects=False)
    assert r.status_code == 303
    assert conn.execute(
        "SELECT locked FROM event_tags WHERE event_id=1 AND dim='entity' AND value='apple'"
    ).fetchone()[0] == 1

    # 屏蔽自动标签 region:cn（locked=-1）
    r = client.post("/event/1/tags?action=remove&v=region:cn", follow_redirects=False)
    assert r.status_code == 303

    # 模拟分析管道刷新
    pipeline._refresh_event_tags(conn, 1)
    conn.commit()
    detail = q.event_detail(conn, 1)
    assert any(t["value"] == "apple" for t in detail["tags"]["entity"]), "人工添加标签被覆盖"
    assert not any(t["value"] == "cn" for t in detail["tags"]["region"]), "屏蔽标签被重建"
    assert any(h["value"] == "cn" for h in detail["hidden"]), "屏蔽标签应出现在已屏蔽区"
    assert "四维标签" in client.get("/event/1").text

    # 恢复屏蔽
    client.post("/event/1/tags?action=restore&v=region:cn", follow_redirects=False)
    pipeline._refresh_event_tags(conn, 1)
    conn.commit()
    assert any(t["value"] == "cn" for t in q.event_detail(conn, 1)["tags"]["region"])

    # 锁定自动标签后，即使文章标签变化也保留
    client.post("/event/1/tags?action=lock&v=type:earnings", follow_redirects=False)
    pipeline._refresh_event_tags(conn, 1)
    conn.commit()
    row = conn.execute(
        "SELECT locked FROM event_tags WHERE event_id=1 AND dim='type' AND value='earnings'"
    ).fetchone()
    assert row and row[0] == 1

    # 非法标签不入库
    client.post("/event/1/tags?action=add&v=evil:nope", follow_redirects=False)
    assert conn.execute(
        "SELECT COUNT(*) FROM event_tags WHERE event_id=1 AND dim='evil'"
    ).fetchone()[0] == 0
