"""M7 关键字搜索验收：站内全库检索 + 外部结果服务端代取。

外部搜索用 httpx.MockTransport 注入样例响应，不依赖真实网络；
SSRF 已由 test_reader 覆盖，这里只验证检索与代取链路。
"""

from __future__ import annotations

import time
from pathlib import Path

import httpx
import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from app import config, db
from app.search import local, providers, service
from app.web.routes import router


BING_RSS = (
    '<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel>'
    "<item><title>AI 芯片需求强劲</title>"
    "<link>https://www.reuters.com/tech/ai-chip-demand</link>"
    "<description>路透社报道 AI 芯片需求持续旺盛。</description>"
    "<pubDate>Mon, 12 Nov 2026 10:00:00 GMT</pubDate></item>"
    "<item><title>人工智能大会召开</title>"
    "<link>https://www.theguardian.com/world/ai-summit</link>"
    "<description>全球人工智能安全峰会开幕。</description>"
    "<pubDate>Tue, 11 Nov 2026 09:00:00 GMT</pubDate></item>"
    "</channel></rss>"
)

SOGOU_HTML = (
    '<html><body>'
    '<h3 class="vr-title"><a href="https://news.sogou.com/link?url=xyz" >人工智能 新政策出台</a></h3>'
    '<p class="text">摘要内容关于人工智能政策落地。</p>'
    '<p class="news-from"><span>新华网</span><span>2026-11-12</span></p>'
    "</body></html>"
)


@pytest.fixture
def env(tmp_path):
    db_path = tmp_path / "search.db"
    settings = config.Settings(db_path=str(db_path))
    # 测试只 mock 了 bing_rss + sogou_news，收窄 providers 让外部代取可离线验证
    settings.search.providers = ["bing_rss", "sogou_news"]
    return db_path, settings


def _seed(db_path):
    db.init_db(db_path)
    c = db.connect(db_path)
    now = int(time.time())
    c.executemany(
        "INSERT INTO tags(dim,value,label,aliases,parent) VALUES(?,?,?,'',NULL)",
        [("region", "cn", "中国"), ("industry", "tech", "科技")],
    )
    c.execute(
        "INSERT INTO events(id,title,category,heat,momentum,article_count,source_count,keywords,first_seen,last_seen)"
        " VALUES(1,'人工智能监管新动向','tech',8,0.3,3,2,'[]',?,?)", (now - 3600, now),
    )
    c.executemany(
        "INSERT INTO articles(id,source_id,url,title,summary,lang,published_at,fetched_at,event_id,analyzed)"
        " VALUES(?,?,?,?,?,?,?,?,?,?)",
        [
            (1, "bbc", "https://www.reuters.com/tech/ai-chip-demand", "人工智能 芯片需求强劲", "英文摘要", "en", now - 3600, now, 1, 1),
            (2, "chinanews", "https://www.chinanews.com.cn/gj/x.shtml", "人工智能大会召开", "中文摘要", "zh", now - 3600, now, 1, 1),
        ],
    )
    c.execute(
        "INSERT INTO social_trends(batch_id,platform,source_id,title,url,rank,heat_value,rank_delta,summary,lang,extra,captured_at)"
        " VALUES(?,?,?,?,?,?,?,?,?,?,?,?)",
        (now, "weibo", "social:weibo", "人工智能 热搜话题", "https://s.weibo.com/top/summary?q=%23AI%23", 1, 1000000, None, "微博摘要", "zh", "{}", now),
    )
    c.commit()
    c.close()


# ---------------------------------------------------------------- 本地检索

def test_tokenize_splits_and_caps():
    assert local.tokenize("人工智能 芯片") == ["人工智能", "芯片"]
    assert local.tokenize("AI, 芯片") == ["AI", "芯片"]
    assert local.tokenize("a" * 50) == ["a" * 40]  # 长度截断
    assert local.tokenize("") == []


def test_local_search_and_matching(env):
    db_path, _ = env
    _seed(db_path)
    c = db.connect(db_path)
    # “人工智能” → 事件 + 2 篇报道 + 1 热榜
    g = local.search_all(c, ["人工智能"], 50)
    assert g["counts"]["events"] >= 1
    assert g["counts"]["articles"] == 2
    assert g["counts"]["trends"] == 1
    # 多词 AND：只有同时含「人工智能」和「大会」的报道命中
    g2 = local.search_all(c, ["人工智能", "大会"], 50)
    assert g2["counts"]["articles"] == 1
    c.close()


def test_local_search_no_match_returns_empty(env):
    db_path, _ = env
    _seed(db_path)
    c = db.connect(db_path)
    g = local.search_all(c, ["量子计算zzz"], 50)
    assert sum(g["counts"].values()) == 0
    c.close()


# ---------------------------------------------------------------- 外部代取（Mock）

def _ext_client():
    def handler(request):
        url = str(request.url)
        if "bing.com" in url and "format=rss" in url:
            return httpx.Response(200, content=BING_RSS.encode("utf-8"), headers={"content-type": "application/rss+xml"})
        if "sogou" in url:
            return httpx.Response(200, content=SOGOU_HTML.encode("utf-8"), headers={"content-type": "text/html"})
        return httpx.Response(404)
    return httpx.Client(transport=httpx.MockTransport(handler), trust_env=False)


def test_providers_parse_bing_rss():
    items = providers.parse("bing_rss", BING_RSS.encode("utf-8"), 10)
    assert len(items) == 2
    assert items[0]["provider"] == "bing_rss"
    assert items[0]["url"].startswith("https://")


def test_providers_parse_sogou():
    items = providers.parse("sogou_news", SOGOU_HTML.encode("utf-8"), 10)
    assert len(items) == 1
    assert "人工智能" in items[0]["title"]
    # 来源与日期从 meta 区块解析
    assert items[0]["source"] in ("新华网", "news.sogou.com")


def test_search_external_with_mock(env):
    db_path, settings = env
    _seed(db_path)
    # 注入 mock client，resolve=False 避免真实 DNS
    r = service.search(db_path, settings, "人工智能", scope="external",
                       client=_ext_client(), resolve=False)
    assert r["total"] >= 2  # bing 2 + sogou 1（去重后）
    assert len(r["external"]) >= 2
    assert r["errors"] == []  # 两个源都应成功
    assert {s["name"] for s in r["sources"]} == {"bing_rss", "sogou_news"}


def test_search_dedup_external_vs_local(env):
    db_path, settings = env
    _seed(db_path)
    # 本地已有一篇 reuters.com/tech/ai-chip-demand，外部 bing 也返回同 URL → 不应重复
    r = service.search(db_path, settings, "人工智能", scope="all",
                       client=_ext_client(), resolve=False)
    ext_urls = {it["url"] for it in r["external"]}
    assert "https://www.reuters.com/tech/ai-chip-demand" not in ext_urls
    assert r["groups"]["articles"][0]["url"] == "https://www.reuters.com/tech/ai-chip-demand"


def test_search_caches_external(env):
    db_path, settings = env
    _seed(db_path)
    s1 = service.search(db_path, settings, "人工智能", scope="external", client=_ext_client(), resolve=False)
    s2 = service.search(db_path, settings, "人工智能", scope="external", client=_ext_client(), resolve=False)
    # 第二次应全部命中缓存，不再请求（mock 仍可用但 store 直接返回）
    assert s2["sources"][0]["cached"] is True
    assert s1["total"] == s2["total"]


# ---------------------------------------------------------------- 页面与接口

def _app(settings):
    app = FastAPI()
    app.state.settings = settings
    app.include_router(router)
    return app


def test_search_page_renders_local(env):
    db_path, settings = env
    _seed(db_path)
    cl = TestClient(_app(settings))
    r = cl.get("/search?q=" + "人工智能" + "&scope=local")
    assert r.status_code == 200
    assert "sch-item" in r.text  # 结果条目渲染
    assert "/read?url=" in r.text  # 原文走站内阅读
    assert "共" in r.text and "条结果" in r.text


def test_search_api_local(env):
    db_path, settings = env
    _seed(db_path)
    cl = TestClient(_app(settings))
    r = cl.get("/api/search?q=人工智能&scope=local")
    assert r.status_code == 200
    d = r.json()
    assert d["total"] >= 3
    assert d["groups"]["counts"]["articles"] == 2


def test_search_status(env):
    db_path, settings = env
    _seed(db_path)
    cl = TestClient(_app(settings))
    d = cl.get("/api/search/status").json()
    assert d["enabled"] is True
    assert "bing_rss" in d["providers"]
