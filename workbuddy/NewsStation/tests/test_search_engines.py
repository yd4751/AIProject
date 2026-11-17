"""M10：可选搜索引擎（默认 / 谷歌）与结果独立展示。

外部请求全部用 httpx.MockTransport 注入样例响应，不依赖真实网络——
Google 为境外站点，本机代理未开启时本测试照样能验证解析与引擎隔离。
"""

from __future__ import annotations

import time

import httpx
import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from app import config, db
from app.search import engines, providers, service
from app.web.routes import router

BING_RSS = (
    '<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel>'
    "<item><title>AI 芯片需求强劲</title>"
    "<link>https://www.bingnews.example.com/ai-chip</link>"
    "<description>综合报道 AI 芯片需求。</description></item>"
    "</channel></rss>"
)
SOGOU_HTML = (
    '<h3 class="vr-title"><a href="https://news.sogou.com/link?url=xyz">人工智能 新政策出台</a></h3>'
    '<p class="text">摘要内容关于人工智能政策落地。</p>'
)

GOOGLE_NEWS_RSS = (
    '<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel>'
    "<item><title>AI 芯片需求增长 - 路透社</title>"
    "<link>https://news.google.com/rss/articles/CBMi123</link>"
    "<description>&lt;a href=&quot;x&quot;&gt;AI 芯片需求增长 - 路透社&lt;/a&gt;</description>"
    "<pubDate>Thu, 12 Nov 2026 10:00:00 GMT</pubDate>"
    '<source url="https://www.reuters.com">路透社</source></item>'
    "<item><title>Google AI 新模型发布 - The Verge</title>"
    "<link>https://news.google.com/rss/articles/CBMi456</link>"
    "<description>&lt;a href=&quot;x&quot;&gt;Google AI 新模型发布 - The Verge&lt;/a&gt;</description>"
    "<pubDate>Thu, 12 Nov 2026 09:00:00 GMT</pubDate>"
    '<source url="https://www.theverge.com">The Verge</source></item>'
    "</channel></rss>"
)

GOOGLE_WEB_HTML = (
    "<html><body>"
    '<div class="g"><a href="/url?q=https%3A%2F%2Fwww.reuters.com%2Ftech%2Fai">'
    '<h3 class="LC20lb">Reuters AI 芯片深度报道</h3></a>'
    '<div class="VwiC3b"><span>2026-11-12 — 摘要内容关于 AI 芯片。</span></div></div>'
    '<a href="https://www.google.com/search?q=more"><h3>更多 Google 搜索</h3></a>'
    '<a href="https://openai.com/index/news/"><h3>OpenAI 官方新闻</h3></a>'
    '<div class="VwiC3b">OpenAI 摘要文本。</div>'
    "</body></html>"
)


@pytest.fixture
def env(tmp_path):
    db_path = tmp_path / "engines.db"
    db.init_db(db_path)
    settings = config.Settings(db_path=str(db_path))
    settings.search.providers = ["bing_rss", "sogou_news"]
    return db_path, settings


def _client(with_google: bool = True, google_ok: bool = True):
    def handler(request):
        u = str(request.url)
        if "bing.com" in u and "format=rss" in u:
            return httpx.Response(200, content=BING_RSS.encode("utf-8"),
                                  headers={"content-type": "application/rss+xml"})
        if "sogou" in u:
            return httpx.Response(200, content=SOGOU_HTML.encode("utf-8"),
                                  headers={"content-type": "text/html"})
        if with_google and "news.google.com" in u:
            if not google_ok:
                return httpx.Response(200, content=b"<html>challenge</html>",
                                      headers={"content-type": "text/html"})
            return httpx.Response(200, content=GOOGLE_NEWS_RSS.encode("utf-8"),
                                  headers={"content-type": "application/rss+xml"})
        if with_google and "www.google.com" in u:
            if not google_ok:
                return httpx.Response(200, content=b"<html>challenge</html>",
                                      headers={"content-type": "text/html"})
            return httpx.Response(200, content=GOOGLE_WEB_HTML.encode("utf-8"),
                                  headers={"content-type": "text/html"})
        return httpx.Response(404)

    return httpx.Client(transport=httpx.MockTransport(handler), trust_env=False)


def _app(settings):
    app = FastAPI()
    app.state.settings = settings
    app.include_router(router)
    return app


# ---------------------------------------------------------------- Google 解析

def test_google_search_url_options():
    u = providers.search_url("google_news", "AI 芯片",
                             {"hl": "en-US", "gl": "US", "ceid": "US:en", "num": 5})
    assert "hl=en-US" in u and "gl=US" in u and "ceid=US:en" in u
    assert "q=AI%20%E8%8A%AF%E7%89%87" in u
    assert "num=5" in providers.search_url("google_web", "AI", {"num": 5})
    assert providers.search_url("bing_rss", "AI") == "https://www.bing.com/search?q=AI&format=rss"
    assert providers.search_url("unknown", "AI") == ""


def test_parse_google_news_rss():
    items = providers.parse("google_news", GOOGLE_NEWS_RSS.encode("utf-8"), 10)
    assert len(items) == 2
    assert items[0]["provider_label"] == "Google 资讯（本地）"
    assert items[0]["title"] == "AI 芯片需求增长"   # 标题尾部的「 - 来源」已去掉
    assert items[0]["source"] in ("路透社", "Google 资讯")
    assert items[0]["summary"] == ""          # description 是标题重复，应清掉
    assert items[0]["url"].startswith("https://news.google.com/")
    assert items[0]["date"].startswith("Thu")


def test_parse_google_web_html():
    items = providers.parse("google_web", GOOGLE_WEB_HTML.encode("utf-8"), 10)
    assert len(items) == 2                    # google.com 自身链接被过滤
    urls = {it["url"] for it in items}
    assert "https://www.reuters.com/tech/ai" in urls     # /url?q= 已解包
    assert "https://openai.com/index/news/" in urls
    assert all("google.com" not in u for u in urls)
    assert any("AI 芯片" in it["summary"] for it in items)
    assert any(it["date"] == "2026-11-12" for it in items)


def test_google_web_consent_page_yields_nothing():
    assert providers.parse("google_web", b"<html>Enable JavaScript</html>", 10) == []


# ---------------------------------------------------------------- 引擎选择

def test_resolve_engines(env):
    _, settings = env
    assert engines.resolve(None, settings) == ["default"]      # 默认只跑原整套
    assert engines.resolve("", settings) == ["default"]
    assert engines.resolve("all", settings) == ["default", "google"]
    assert engines.resolve("google,default", settings) == ["default", "google"]
    assert engines.resolve("google", settings) == ["google"]
    assert engines.resolve("nope", settings) == ["default"]   # 未知项回退
    assert engines.resolve(["google", "google"], settings) == ["google"]


def test_disabled_google_is_not_selectable(env):
    _, settings = env
    settings.search.google.enabled = False
    assert [e["id"] for e in engines.available(settings)] == ["default"]
    assert engines.resolve("all", settings) == ["default"]
    assert len(engines.choices(["default"], "AI", "all", settings)) == 1


def test_engine_choices_toggle_and_keep_at_least_one(env):
    _, settings = env
    one = engines.choices(["default"], "AI", "all", settings)
    assert [c["selected"] for c in one] == [True, False]
    assert "engines=default%2Cgoogle" in one[1]["href"] or "engines=default,google" in one[1]["href"]
    last = engines.choices(["google"], "AI", "all", settings)
    # 已是最后一个引擎时不允许取消到 0 个
    assert "engines=google" in last[1]["href"]


# ---------------------------------------------------------------- service 独立展示

def test_two_engines_return_independent_blocks(env):
    db_path, settings = env
    data = service.search(db_path, settings, "人工智能", scope="external",
                          engines="default,google", client=_client(), resolve=False)
    assert data["selected"] == ["default", "google"]
    assert [b["id"] for b in data["engines"]] == ["default", "google"]

    default, google = data["engines"]
    assert default["count"] >= 2 and google["count"] >= 2
    assert {s["name"] for s in default["sources"]} == {"bing_rss", "sogou_news"}
    assert {s["name"] for s in google["sources"]} == {
        "google_news", "google_global", "ddg_web"}
    # 独立：谷歌结果不混进默认引擎，旧字段仍指向默认引擎
    d_urls = {it["url"] for it in default["items"]}
    g_urls = {it["url"] for it in google["items"]}
    assert d_urls.isdisjoint(g_urls)
    assert data["external"] == default["items"]
    assert data["sources"] == default["sources"]
    assert data["total"] == default["count"] + google["count"]
    assert data["engines_param"] == "default,google"


def test_google_failure_does_not_break_default_engine(env):
    db_path, settings = env
    data = service.search(db_path, settings, "人工智能", scope="external",
                          engines="all", client=_client(google_ok=False), resolve=False)
    default, google = data["engines"]
    assert default["count"] >= 2 and default["errors"] == []
    assert google["count"] == 0
    assert len(google["errors"]) == 3          # 三个 provider 各自报错
    assert google["note"]
    assert len(data["errors"]) == 3           # 顶层汇总，但默认引擎不受影响


def test_only_google_selected_keeps_legacy_shape(env):
    db_path, settings = env
    data = service.search(db_path, settings, "人工智能", scope="external",
                          engines="google", client=_client(), resolve=False)
    assert data["selected"] == ["google"]
    assert len(data["engines"]) == 1
    assert data["groups"]["counts"]["articles"] == 0
    assert data["external"] == []
    assert data["total"] == data["engines"][0]["count"]


def test_scope_local_still_returns_google_results(env):
    """选了谷歌引擎就是要谷歌结果：「仅站内」只约束默认引擎。"""
    db_path, settings = env
    data = service.search(db_path, settings, "人工智能", scope="local",
                          engines="all", client=_client(), resolve=False)
    default, google = data["engines"]
    assert default["sources"] == []            # 默认引擎仍不联网
    assert google["sources"] and google["count"] >= 2
    assert "仅站内" in google["note"]


def test_google_engine_fails_fast_when_proxy_is_down(env):
    db_path, settings = env
    settings.proxy.url = "http://127.0.0.1:1"     # 必定不可达
    started = time.time()
    data = service.search(db_path, settings, "人工智能", scope="external",
                          engines="google", client=None, resolve=False)
    assert time.time() - started < 3
    google = data["engines"][0]
    assert google["count"] == 0 and google["sources"] == []
    assert "不可达" in google["note"]
    assert "默认引擎不受影响" in google["note"]


def test_google_engine_asks_for_proxy_when_unset(env):
    db_path, settings = env
    settings.proxy.url = ""
    data = service.search(db_path, settings, "人工智能", scope="external",
                          engines="google", client=None, resolve=False)
    assert "填入本地代理地址" in data["engines"][0]["note"]


def test_status_lists_engines(env):
    db_path, settings = env
    d = service.status(db_path, settings)
    assert d["default_engine"] == "default"
    assert [e["id"] for e in d["engines"]] == ["default", "google"]
    assert d["engines"][1]["providers"] == ["google_news", "google_global", "ddg_web"]


# ---------------------------------------------------------------- 页面与接口

def test_search_page_shows_engine_selector_and_independent_blocks(env):
    _, settings = env
    settings.search.external_enabled = False      # 页面测试不发真实外网请求
    cl = TestClient(_app(settings))
    r = cl.get("/search?q=AI&scope=local&engines=default,google")
    assert r.status_code == 200
    assert "同时搜多个引擎" in r.text
    assert "谷歌引擎" in r.text and "默认引擎" in r.text
    assert 'id="engine-default"' in r.text and 'id="engine-google"' in r.text
    assert "各自独立成区" in r.text
    assert "sch-engine-badge" in r.text


def test_api_search_accepts_engines_param(env):
    _, settings = env
    settings.search.external_enabled = False      # 只验参数路由，不联网
    cl = TestClient(_app(settings))
    d = cl.get("/api/search?q=AI&scope=external&engines=google").json()
    assert d["selected"] == ["google"]
    assert len(d["engines"]) == 1
    assert d["engines"][0]["id"] == "google"
    assert "engine_choices" in d
