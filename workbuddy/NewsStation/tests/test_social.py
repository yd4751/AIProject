"""M6 社媒热榜验收：解析器、稳定 URL、快照与升降、注入开关、页面与接口。

全部离线：解析器用真实响应结构的内联样本，编排用 httpx.MockTransport，
不访问网络、不跑 lifespan、不碰生产库。
"""

from __future__ import annotations

import asyncio
import json
import time

import httpx
import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from app import config, db
from app.ingest import fetch, registry, social
from app.ingest.registry import Article
from app.ingest.social import parsers
from app.ingest.social.store import save_social_trends
from app.web import social_queries as sq
from app.web.routes import router

# 精简但保留真实字段名的响应样本
SAMPLES = {
    "weibo": json.dumps(
        {"data": {"realtime": [
            {"word": "山姆羽绒服断货", "num": 1121658, "word_scheme": "#山姆羽绒服断货#", "note": "山姆羽绒服断货", "label_name": "沸"},
            {"word": "奔驰CEO谈中国市场", "num": 798863, "word_scheme": "#奔驰CEO谈中国市场#", "note": "奔驰CEO谈中国市场", "label_name": ""},
            {"word": "山姆羽绒服断货", "num": 1, "word_scheme": "#山姆羽绒服断货#"},  # 重复，应被去重
        ]}},
        ensure_ascii=False,
    ),
    "baidu": '<!--s-data:' + json.dumps(
        {"data": {"cards": [{"component": "hotList", "content": [
            {"word": "亚太潮涌 同舟扬帆", "hotScore": "7903922", "desc": "前三季度进出口增长", "hotChange": "same", "hotTag": "0"},
            {"word": "小学生只上半天学", "hotScore": "7807938", "desc": "家长圈热议", "hotChange": "up", "hotTag": "1"},
        ]}]}},
        ensure_ascii=False,
    ) + '-->',
    "toutiao": json.dumps(
        {"data": [
            {"Title": "77万亿存款到期会流向哪里", "HotValue": "9773673", "ClusterId": 770601, "Label": "interpretation",
             "Url": "https://www.toutiao.com/trending/770601/?log_pb=RANDOM_EVERY_ROUND&rank=&style_id=40132"},
            {"Title": "俄美乌三方会谈", "HotValue": "8843585", "ClusterId": 770694, "Label": "",
             "Url": "https://www.toutiao.com/trending/770694/?log_pb=OTHER"},
        ]},
        ensure_ascii=False,
    ),
    "douyin": json.dumps(
        {"word_list": [
            {"word": "俄方公布俄美乌三方会谈成果", "hot_value": 11478526, "label": 1},
            {"word": "纪念孙中山先生诞辰160周年", "hot_value": 11435964, "label": 0},
        ]},
        ensure_ascii=False,
    ),
    "bilibili": json.dumps(
        {"code": 0, "data": {"list": [
            {"title": "【带着AI穿越2018】蓝色大肥鱼", "bvid": "BV1GJWy6rEQz", "desc": "简介内容",
             "stat": {"view": 423644}, "tname": "动漫杂谈", "owner": {"name": "某UP主"}},
        ]}},
        ensure_ascii=False,
    ),
    "hackernews": json.dumps(
        {"hits": [
            {"objectID": "40123456", "title": "Show HN: A new terminal", "url": "https://example.com/term",
             "points": 320, "num_comments": 88},
            {"objectID": "40123457", "title": "Ask HN: How do you learn Rust?", "url": None,
             "points": 210, "num_comments": 140},
        ]},
        ensure_ascii=False,
    ),
    "lobsters": json.dumps(
        [
            {"short_id": "abc123", "title": "A neat systems post", "url": "https://blog.example.com/x",
             "score": 42, "comment_count": 9, "tags": ["programming"]},
            {"short_id": "def456", "title": "Ask: best static site generator", "url": None,
             "score": 17, "comment_count": 3, "tags": ["ask"]},
        ],
        ensure_ascii=False,
    ),
    "github": (
        '<article class="Box-row">'
        '  <h2 class="h3 lh-condensed"><a data-x href="/owner1/repo1">owner1 / repo1</a></h2>'
        '  <p class="col-9 color-fg-muted my-1 pr-4">A test repo description</p>'
        '  <span itemprop="programmingLanguage">Python</span>'
        '  <a href="/owner1/repo1/stargazers"><svg></svg>1,234</a>'
        '  <span class="d-inline-block float-sm-right">56 stars today</span>'
        '</article>'
        '<article class="Box-row">'
        '  <h2 class="h3 lh-condensed"><a href="/owner2/repo2">owner2 / repo2</a></h2>'
        '  <p class="col-9 color-fg-muted my-1 pr-4">Another desc</p>'
        '  <span itemprop="programmingLanguage">Rust</span>'
        '  <a href="/owner2/repo2/stargazers"><svg></svg>789</a>'
        '  <span class="d-inline-block float-sm-right">12 stars today</span>'
        '</article>'
    ),
}


@pytest.fixture(autouse=True)
def clean_registry():
    """社媒注册表是进程级全局，测试前后都清空，避免污染其它测试文件。"""
    social.clear()
    yield
    social.clear()


def trend(title: str, platform: str, rank: int, heat: float = 100.0) -> Article:
    return Article(
        url=f"https://e.com/{platform}/{title}", title=title, summary="",
        published_at=None, lang="zh", source_id=f"social:{platform}",
        rank=rank, heat_value=heat, platform=platform,
    )


def make_app(db_path, **overrides) -> TestClient:
    db.init_db(db_path)
    settings = config.Settings(db_path=str(db_path), **overrides)
    app = FastAPI()
    app.state.settings = settings
    app.include_router(router)
    return TestClient(app), settings


# ---------------------------------------------------------------- 解析器

def test_all_parsers_return_rows():
    assert set(parsers.PARSERS) == {
        "weibo", "baidu", "toutiao", "douyin", "bilibili",
        "hackernews", "lobsters", "github",
    }
    for name, fn in parsers.PARSERS.items():
        rows = fn(SAMPLES[name], 30)
        assert rows, f"{name} 没有解析出条目"
        for r in rows:
            assert r["title"] and r["url"].startswith("https://")
            assert r["rank"] >= 1 and isinstance(r["heat_value"], float)
        print(name, len(rows), rows[0]["title"])


def test_parsers_keep_real_values():
    assert parsers.weibo(SAMPLES["weibo"], 30)[0]["heat_value"] == 1121658.0
    assert parsers.weibo(SAMPLES["weibo"], 30)[0]["extra"]["label"] == "沸"
    assert parsers.baidu(SAMPLES["baidu"], 30)[0]["heat_value"] == 7903922.0
    assert "前三季度" in parsers.baidu(SAMPLES["baidu"], 30)[0]["summary"]
    assert parsers.toutiao(SAMPLES["toutiao"], 30)[0]["heat_value"] == 9773673.0
    assert parsers.douyin(SAMPLES["douyin"], 30)[0]["heat_value"] == 11478526.0
    b = parsers.bilibili(SAMPLES["bilibili"], 30)[0]
    assert b["heat_value"] == 423644.0 and b["extra"]["category"] == "动漫杂谈"


def test_urls_are_stable_across_rounds():
    """榜单每轮都返回，URL 必须稳定，否则同一话题会反复入库 / 反复推送订阅。"""
    for name in SAMPLES:
        a = parsers.PARSERS[name](SAMPLES[name], 30)
        b = parsers.PARSERS[name](SAMPLES[name], 30)
        assert [x["url"] for x in a] == [x["url"] for x in b], name
    tt = parsers.toutiao(SAMPLES["toutiao"], 30)[0]["url"]
    assert tt == "https://www.toutiao.com/trending/770601/" and "log_pb" not in tt
    assert parsers.weibo(SAMPLES["weibo"], 30)[0]["url"].startswith("https://s.weibo.com/weibo?q=")
    assert parsers.douyin(SAMPLES["douyin"], 30)[0]["url"].startswith("https://www.douyin.com/search/")
    assert parsers.bilibili(SAMPLES["bilibili"], 30)[0]["url"].endswith("BV1GJWy6rEQz")


def test_dedup_and_limit():
    assert len(parsers.weibo(SAMPLES["weibo"], 30)) == 2
    assert len(parsers.toutiao(SAMPLES["toutiao"], 1)) == 1


# ---------------------------------------------------------------- 境外解析器

def test_foreign_parsers_extract_and_stable_urls():
    hn = parsers.hackernews(SAMPLES["hackernews"], 30)
    assert hn[0]["title"] == "Show HN: A new terminal"
    assert hn[0]["url"] == "https://example.com/term"
    assert hn[0]["heat_value"] == 320.0
    # Ask HN 无外链 → 回退到讨论页，URL 稳定
    assert hn[1]["url"] == "https://news.ycombinator.com/item?id=40123457"

    lob = parsers.lobsters(SAMPLES["lobsters"], 30)
    assert lob[0]["url"] == "https://blog.example.com/x"
    assert lob[1]["url"] == "https://lobste.rs/s/def456"
    assert lob[0]["extra"]["tags"] == ["programming"]

    gh = parsers.github(SAMPLES["github"], 30)
    assert len(gh) == 2
    assert gh[0]["title"] == "owner1/repo1"
    assert gh[0]["url"] == "https://github.com/owner1/repo1"
    # 热度优先取 stars today；语言与总 star 也落进 extra
    assert gh[0]["heat_value"] == 56.0
    assert gh[0]["extra"]["language"] == "Python"
    assert gh[0]["extra"]["stars_total"] == 1234.0
    assert gh[1]["heat_value"] == 12.0


def test_foreign_parsers_urls_stable_across_rounds():
    for name in ("hackernews", "lobsters", "github"):
        a = parsers.PARSERS[name](SAMPLES[name], 30)
        b = parsers.PARSERS[name](SAMPLES[name], 30)
        assert [x["url"] for x in a] == [x["url"] for x in b], name


def test_yaml_registers_eight_enabled_sources():
    assert social.load_social_yaml() == 8
    sources = social.all_sources()
    assert len(sources) == 8
    assert {s.platform for s in sources} == set(parsers.PARSERS)
    # 国内 5 源直连、region=cn；境外 3 源需代理、region=global、带 inject_top 覆盖
    domestic = [s for s in sources if s.region == "cn"]
    foreign = [s for s in sources if s.region == "global"]
    assert len(domestic) == 5 and len(foreign) == 3
    assert all(not s.proxy_required for s in domestic)
    assert all(s.proxy_required and s.inject_top for s in foreign)
    assert social.get("social:weibo").headers["Referer"].startswith("https://weibo.com")
    assert social.get("social:github").headers["User-Agent"].startswith("Mozilla")


# ---------------------------------------------------------------- 快照落库

def test_save_trends_batch_and_rank_delta(tmp_path):
    path = tmp_path / "s.db"
    db.init_db(path)
    first = [trend("A", "weibo", 1), trend("B", "weibo", 2), trend("C", "weibo", 3)]
    assert save_social_trends(path, first, batch_id=100, retention_days=7) == 3
    second = [trend("B", "weibo", 1), trend("A", "weibo", 2), trend("C", "weibo", 3), trend("D", "weibo", 4)]
    assert save_social_trends(path, second, batch_id=200, retention_days=7) == 4

    conn = db.connect(path)
    try:
        rows = {
            r["title"]: r["rank_delta"]
            for r in conn.execute("SELECT * FROM social_trends WHERE batch_id = 200")
        }
        assert rows == {"B": 1, "A": -1, "C": 0, "D": None}  # 升 / 降 / 持平 / 新进榜
        assert conn.execute("SELECT COUNT(*) FROM social_trends").fetchone()[0] == 7
        assert sq.delta_label(None) == ("新", "new")
        assert sq.delta_label(3) == ("↑3", "up")
    finally:
        conn.close()


def test_save_trends_purges_old_snapshots(tmp_path):
    path = tmp_path / "s.db"
    db.init_db(path)
    conn = db.connect(path)
    old = int(time.time()) - 10 * 86400
    conn.execute(
        "INSERT INTO social_trends(batch_id,platform,source_id,title,url,rank,heat_value,summary,lang,extra,captured_at)"
        " VALUES(1,'weibo','social:weibo','旧话题','https://e.com/old',1,1,'','zh','{}',?)",
        (old,),
    )
    conn.commit()
    conn.close()
    save_social_trends(path, [trend("新话题", "weibo", 1)], batch_id=200)
    conn = db.connect(path)
    try:
        titles = [r["title"] for r in conn.execute("SELECT title FROM social_trends")]
        assert titles == ["新话题"]
    finally:
        conn.close()


# ---------------------------------------------------------------- 编排

def _mock_client(monkeypatch, handler):
    real = httpx.AsyncClient
    def _wrap(**kw):
        # MockTransport 不能和 proxy 同时传，离线测试里代理无意义，直接丢弃
        kw.pop("proxy", None)
        kw.pop("proxies", None)
        return real(transport=httpx.MockTransport(handler), **kw)
    monkeypatch.setattr(fetch.httpx, "AsyncClient", _wrap)


def _register_mock_sources():
    monkeypatch_sources = [
        ("social:weibo", "weibo", "https://e.com/weibo"),
        ("social:douyin", "douyin", "https://e.com/douyin"),
    ]
    for sid, platform, url in monkeypatch_sources:
        social.register(
            social.SocialSource(
                id=sid, name=sid, platform=platform, homepage="", url=url,
                parser=platform, limit=30,
            )
        )


def test_social_source_fetch_returns_articles():
    async def run():
        src = social.SocialSource(
            id="social:weibo", name="微博", platform="weibo", homepage="",
            url="https://e.com/weibo", parser="weibo",
        )
        async with httpx.AsyncClient(
            transport=httpx.MockTransport(lambda r: httpx.Response(200, text=SAMPLES["weibo"]))
        ) as client:
            return await src.fetch(client)

    arts = asyncio.run(run())
    assert len(arts) == 2 and all(a.platform == "weibo" for a in arts)
    assert arts[0].rank == 1 and arts[0].heat_value == 1121658.0
    assert arts[0].source_id == "social:weibo"


def test_run_once_writes_snapshot_and_injects(tmp_path, monkeypatch):
    path = tmp_path / "s.db"
    db.init_db(path)
    monkeypatch.setattr(registry, "_registry", {})
    _register_mock_sources()

    def handler(request):
        platform = "weibo" if "weibo" in str(request.url) else "douyin"
        return httpx.Response(200, text=SAMPLES[platform])

    _mock_client(monkeypatch, handler)
    settings = config.Settings(
        db_path=str(path), social={"inject_articles": True, "inject_top": 1},
    )
    results = asyncio.run(fetch.run_once(settings, only=["social:weibo", "social:douyin"]))
    assert all(r["status"] == "ok" and r["kind"] == "social" for r in results)

    conn = db.connect(path)
    try:
        assert conn.execute("SELECT COUNT(*) FROM social_trends").fetchone()[0] == 4
        # inject_top=1 → 每平台只注入榜首一条
        assert conn.execute("SELECT COUNT(*) FROM articles").fetchone()[0] == 2
        assert {r["source_id"] for r in conn.execute("SELECT source_id FROM articles")} == {
            "social:weibo", "social:douyin"
        }
        assert {r["source_id"]: r["last_status"] for r in conn.execute("SELECT * FROM source_state")} == {
            "social:weibo": "ok", "social:douyin": "ok"
        }
    finally:
        conn.close()


def test_run_once_inject_can_be_disabled(tmp_path, monkeypatch):
    path = tmp_path / "s.db"
    db.init_db(path)
    monkeypatch.setattr(registry, "_registry", {})
    _register_mock_sources()
    _mock_client(
        monkeypatch,
        lambda r: httpx.Response(200, text=SAMPLES["weibo" if "weibo" in str(r.url) else "douyin"]),
    )
    settings = config.Settings(db_path=str(path), social={"inject_articles": False})
    asyncio.run(fetch.run_once(settings, only=["social:weibo", "social:douyin"]))
    conn = db.connect(path)
    try:
        assert conn.execute("SELECT COUNT(*) FROM social_trends").fetchone()[0] == 4
        assert conn.execute("SELECT COUNT(*) FROM articles").fetchone()[0] == 0
    finally:
        conn.close()


def test_foreign_source_routed_via_proxy_or_skipped(tmp_path, monkeypatch):
    """proxy_required 源：配置代理时走代理客户端入库；无代理时被安全跳过（不报错）。"""
    monkeypatch.setattr(registry, "_registry", {})
    social.register(
        social.SocialSource(
            id="social:hackernews", name="HN", platform="hackernews", homepage="",
            url="https://hn.algolia.com/x", parser="hackernews",
            proxy_required=True, region="global", limit=30,
        )
    )

    def handler(request):
        return httpx.Response(200, text=SAMPLES["hackernews"])

    _mock_client(monkeypatch, handler)

    settings = config.Settings(
        db_path=str(tmp_path / "p1.db"),
        proxy={"url": "http://127.0.0.1:7890", "for_foreign": True},
        social={"inject_articles": False},
    )
    res = asyncio.run(fetch.run_once(settings, only=["social:hackernews"]))
    assert res[0]["status"] == "ok"
    conn = db.connect(tmp_path / "p1.db")
    try:
        assert conn.execute("SELECT COUNT(*) FROM social_trends").fetchone()[0] == 2
    finally:
        conn.close()

    # 没有代理：proxy_required 源直接 skip，不影响其它源
    monkeypatch.setattr(registry, "_registry", {})
    social.register(
        social.SocialSource(
            id="social:hackernews", name="HN", platform="hackernews", homepage="",
            url="https://hn.algolia.com/x", parser="hackernews",
            proxy_required=True, region="global", limit=30,
        )
    )
    settings2 = config.Settings(
        db_path=str(tmp_path / "p2.db"),
        proxy={"url": "", "for_foreign": True},
        social={"inject_articles": False},
    )
    res2 = asyncio.run(fetch.run_once(settings2, only=["social:hackernews"]))
    assert res2[0]["status"] == "skip" and res2[0]["note"] == "无代理"


def test_run_one_source_failure_is_isolated(tmp_path, monkeypatch):
    path = tmp_path / "s.db"
    db.init_db(path)
    monkeypatch.setattr(registry, "_registry", {})
    _register_mock_sources()
    _mock_client(
        monkeypatch,
        lambda r: httpx.Response(500) if "weibo" in str(r.url)
        else httpx.Response(200, text=SAMPLES["douyin"]),
    )
    settings = config.Settings(db_path=str(path), social={"inject_articles": False})
    results = asyncio.run(fetch.run_once(settings, only=["social:weibo", "social:douyin"]))
    by_id = {r["id"]: r["status"] for r in results}
    assert by_id == {"social:weibo": "fail", "social:douyin": "ok"}
    conn = db.connect(path)
    try:
        assert conn.execute("SELECT COUNT(*) FROM social_trends").fetchone()[0] == 2
        st = {r["source_id"]: r["fail_streak"] for r in conn.execute("SELECT * FROM source_state")}
        assert st["social:weibo"] == 1 and st["social:douyin"] == 0
    finally:
        conn.close()


# ---------------------------------------------------------------- 展示层

def _seed_trends(conn):
    now = int(time.time())
    rows = [
        (100, "weibo", "social:weibo", "俄美乌三方会谈成果", "https://e.com/w1", 1, 1000000, None, "", "zh", "{}", now),
        (100, "douyin", "social:douyin", "俄美乌三方会谈成果", "https://e.com/d1", 1, 900000, 2, "", "zh", "{}", now),
        (100, "baidu", "social:baidu", "亚太潮涌 同舟扬帆", "https://e.com/b1", 1, 800000, None, "", "zh", "{}", now),
        (100, "bilibili", "social:bilibili", "AI 穿越 2018", "https://e.com/v1", 1, 400000, None, "", "zh", "{}", now),
        (100, "toutiao", "social:toutiao", "存款到期流向哪里", "https://e.com/t1", 1, 700000, None, "", "zh", "{}", now),
    ]
    conn.executemany(
        "INSERT INTO social_trends(batch_id,platform,source_id,title,url,rank,heat_value,"
        " rank_delta,summary,lang,extra,captured_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", rows,
    )
    conn.commit()


def test_boards_resonance_and_status(tmp_path):
    path = tmp_path / "w.db"
    db.init_db(path)
    conn = db.connect(path)
    try:
        _seed_trends(conn)
        data = sq.boards(conn)
        # 只播种了国内 5 平台，boards 仅含有快照的平台；顺序按 PLATFORM_META
        assert set(data) <= set(sq.PLATFORM_META)
        assert set(data) == {"weibo", "baidu", "toutiao", "douyin", "bilibili"}
        assert data["douyin"]["items"][0]["delta_text"] == "↑2"
        assert data["weibo"]["items"][0]["delta_class"] == "new"
        hits = sq.resonance(data)
        assert len(hits) == 1 and hits[0]["platforms"] == ["weibo", "douyin"]
        assert hits[0]["title"] == "俄美乌三方会谈成果"
        # B 站是视频榜，不参与共振，也不因长标题包含热搜词误合并
        assert all("bilibili" not in h["platforms"] for h in hits)
        # status 覆盖全部 PLATFORM_META（含境外 3 个，未抓取显示「未运行」）
        assert len(sq.status(conn)) == 8
        assert len(sq.trends(conn, "douyin")) == 1
        assert sq.heat_label(11478526) == "1147.9万"
    finally:
        conn.close()


def test_social_pages_and_api(tmp_path):
    path = tmp_path / "w.db"
    client, _ = make_app(path)
    conn = db.connect(path)
    try:
        _seed_trends(conn)
    finally:
        conn.close()

    page = client.get("/social")
    assert page.status_code == 200
    assert "board-grid" in page.text and "跨平台共振" in page.text
    assert "俄美乌三方会谈成果" in page.text and "亚太潮涌" in page.text
    single = client.get("/social?platform=weibo")
    assert single.status_code == 200
    assert "亚太潮涌" not in single.text  # 单平台筛选生效

    trend_api = client.get("/api/social/trends").json()
    assert trend_api["count"] == 5 and len(trend_api["platforms"]) == 5
    assert client.get("/api/social/trends?platform=douyin").json()["count"] == 1
    status_api = client.get("/api/social/status").json()
    assert status_api["count"] == 8 and status_api["resonance"] == 1
    stats = client.get("/api/stats").json()
    assert stats["social_trends"] == 5 and stats["social_platforms"] == 5
    assert client.get("/sources").status_code == 200
