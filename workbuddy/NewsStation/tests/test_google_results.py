"""谷歌引擎结果可用性回归（M11）。

背景：Google 网页搜索强制 JS，服务端代取只能拿到空壳；「几乎没有结果」的真正
成因是（1）死源一直挂在配置里，（2）失败产生的空结果被缓存 30 分钟，
（3）「仅站内」把谷歌引擎也掐掉。这里逐条锁死修复行为。
"""

from __future__ import annotations

import sqlite3

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from app import config, db
from app.reader import store
from app.search import providers, service
from app.web.routes import router

LOCAL_RSS = (
    '<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel>'
    "<item><title>本地新闻标题 - 路透社</title>"
    "<link>https://news.google.com/rss/articles/LOCAL1</link>"
    '<source url="https://www.reuters.com">路透社</source></item>'
    "<item><title>本地新闻第二条 - 路透社</title>"
    "<link>https://news.google.com/rss/articles/LOCAL2</link>"
    '<source url="https://www.reuters.com">路透社</source></item>'
    "</channel></rss>"
)
GLOBAL_RSS = (
    '<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel>'
    "<item><title>Global AI headline - The Verge</title>"
    "<link>https://news.google.com/rss/articles/GLOBAL1</link>"
    '<source url="https://www.theverge.com">The Verge</source></item>'
    "</channel></rss>"
)
EMPTY_RSS = '<?xml version="1.0"?><rss version="2.0"><channel></channel></rss>'

# DuckDuckGo HTML 版结果片段（真实结构：result__a 链接包 uddg，紧跟 result__snippet）
DDG_HTML = """<html><body>
<div class="result results_links web-result">
  <h2 class="result__title">
    <a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fwww.ibm.com%2Fcn-zh%2Fthink%2Fai-chip&amp;rut=abc">什么是 AI 芯片？ - IBM</a>
  </h2>
  <a class="result__snippet" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fwww.ibm.com">AI 芯片是专为机器学习负载设计的处理器。</a>
</div>
<div class="result results_links web-result">
  <h2 class="result__title">
    <a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fbaike.baidu.com%2Fitem%2FAI&amp;rut=def">AI 芯片_百度百科</a>
  </h2>
  <a class="result__snippet" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fbaike.baidu.com">AI 芯片（AI Chip）是人工智能加速硬件。</a>
</div>
</body></html>"""


@pytest.fixture
def env(tmp_path, monkeypatch):
    db_path = tmp_path / "g.db"
    db.init_db(db_path)
    settings = config.Settings(db_path=str(db_path))
    settings.search.google.prefer_proxy = False   # 跳过真实代理端口探活
    settings.search.providers = ["bing_rss"]
    calls = []

    def fake_fetch(url, settings, **kwargs):
        calls.append(url)
        if "html.duckduckgo.com" in url:
            body = DDG_HTML
        elif "hl=en-US" in url:
            body = GLOBAL_RSS
        else:
            body = LOCAL_RSS
        return {"ok": True, "content": body.encode("utf-8"), "transport": "mock"}

    monkeypatch.setattr(service.reader, "fetch_raw", fake_fetch)
    return db_path, settings, calls


def _app(settings):
    app = FastAPI()
    app.state.settings = settings
    app.include_router(router)
    return app


# ---------------------------------------------------------------- 数据源与 URL

def test_two_google_providers_use_different_regions():
    opts = config.GoogleSearchConfig().url_opts
    local = providers.search_url("google_news", "AI", opts)
    glob = providers.search_url("google_global", "AI", opts)
    assert "hl=zh-CN" in local and "ceid=CN:zh-Hans" in local
    assert "hl=en-US" in glob and "ceid=US:en" in glob
    assert local != glob
    assert "news.google.com/rss/search" in glob


def test_default_google_providers_no_longer_include_dead_web_source():
    s = config.Settings()
    assert s.search.google.providers == ["google_news", "google_global"]
    assert "google_web" not in s.search.google.providers
    assert s.search.google.results_per_provider == 20


def test_direct_google_urls():
    urls = providers.direct_google_urls("AI 芯片", config.GoogleSearchConfig().url_opts)
    assert len(urls) == 2
    assert "www.google.com/search?q=AI%20%E8%8A%AF%E7%89%87" in urls[0]["url"]
    assert "news.google.com/search" in urls[1]["url"]
    assert all(u["label"] and u["hint"] for u in urls)


# ---------------------------------------------------------------- 结果条数与直达

def test_google_engine_returns_both_regions_and_direct_links(env):
    db_path, settings, calls = env
    data = service.search(db_path, settings, "AI", scope="all",
                          engines="google", client=None, resolve=False)
    g = data["engines"][0]
    assert g["count"] == 5                       # 资讯 本地 2 + 全球 1，网页 2
    assert {s["name"] for s in g["sources"]} == {
        "google_news", "google_global", "ddg_web"}
    assert all(s["count"] > 0 for s in g["sources"])
    assert len(g["direct_urls"]) == 2
    assert any("Global AI headline" in it["title"] for it in g["items"])
    news = [it for it in g["items"] if it["provider"] != "ddg_web"]
    assert all(it["title"].startswith("本地") or it["title"].startswith("Global")
               for it in news)                   # 标题尾部「 - 来源」已清理
    assert len(calls) == 3                       # 两路资讯 + 一路网页都真的取了


def test_results_per_provider_is_honoured(env):
    db_path, settings, _ = env
    settings.search.google.results_per_provider = 1
    settings.search.google.web_results = 0       # 关掉网页结果，只看资讯条数
    g = service.search(db_path, settings, "AI", scope="all", engines="google",
                       client=None, resolve=False)["engines"][0]
    assert g["count"] == 2                       # 每路只取 1 条
    assert all(s["count"] == 1 for s in g["sources"])


# ---------------------------------------------------------------- 网页结果（DuckDuckGo）

def test_ddg_url_and_parser():
    opts = config.GoogleSearchConfig().url_opts
    url = providers.search_url("ddg_web", "AI 芯片", opts)
    assert url.startswith("https://html.duckduckgo.com/html/?q=")
    assert "kl=cn-zh" in url
    items = providers.parse("ddg_web", DDG_HTML.encode("utf-8"), 10)
    assert [it["url"] for it in items] == [
        "https://www.ibm.com/cn-zh/think/ai-chip",
        "https://baike.baidu.com/item/AI",
    ]                                           # uddg 包装已解成真实 URL
    assert items[0]["source"] == "ibm.com"
    assert "机器学习" in items[0]["summary"]
    assert items[0]["provider_label"] == "DuckDuckGo 网页"


def test_google_block_has_news_and_web_sections(env):
    db_path, settings, _ = env
    g = service.search(db_path, settings, "AI", scope="all", engines="google",
                       client=None, resolve=False)["engines"][0]
    keys = [s["key"] for s in g["sections"]]
    assert keys == ["news", "web"]              # 资讯在上、网页在下，各自成区
    web = next(s for s in g["sections"] if s["key"] == "web")
    assert len(web["items"]) == 2
    assert all(it["provider"] == "ddg_web" for it in web["items"])
    # 网页结果是真实 URL，可以站内阅读；资讯结果才是 Google 跳转链
    assert web["items"][0]["url"].startswith("https://www.ibm.com")


def test_web_results_can_be_disabled(env):
    db_path, settings, calls = env
    settings.search.google.web_providers = []
    g = service.search(db_path, settings, "AI", scope="all", engines="google",
                       client=None, resolve=False)["engines"][0]
    assert g["count"] == 3
    assert [s["key"] for s in g["sections"]] == ["news"]
    assert not any("duckduckgo" in u for u in calls)


# ---------------------------------------------------------------- 空缓存修复

def test_empty_results_are_not_cached(env, monkeypatch):
    db_path, settings, calls = env
    monkeypatch.setattr(service.reader, "fetch_raw",
                        lambda url, s, **kw: {"ok": True,
                                              "content": EMPTY_RSS.encode("utf-8")})
    g = service.search(db_path, settings, "空结果", scope="all", engines="google",
                       client=None, resolve=False)["engines"][0]
    assert g["count"] == 0
    conn = sqlite3.connect(db_path)
    try:
        rows = conn.execute(
            "SELECT payload FROM search_cache WHERE provider LIKE 'google%'").fetchall()
    finally:
        conn.close()
    assert rows == [], "失败/空结果绝不能进缓存，否则代理恢复后仍是一片空白"


def test_legacy_empty_cache_is_treated_as_miss(env):
    """历史遗留的 [] 缓存也要能自愈，不需要用户手动清库或强制刷新。"""
    db_path, settings, _ = env
    conn = db.connect(db_path)
    try:
        store.search_put(conn, "google_news", "AI", [])
        store.search_put(conn, "google_global", "AI", [])
    finally:
        conn.close()
    g = service.search(db_path, settings, "AI", scope="all", engines="google",
                       client=None, resolve=False)["engines"][0]
    assert g["count"] == 5                       # 3 条资讯 + 2 条网页结果


def test_scope_local_no_longer_suppresses_google(env):
    db_path, settings, _ = env
    data = service.search(db_path, settings, "AI", scope="local",
                          engines="default,google", client=None, resolve=False)
    default, google = data["engines"]
    assert default["sources"] == []              # 默认引擎仍遵守「仅站内」
    assert google["count"] == 5                  # 选了谷歌就必须有谷歌结果
    assert "仅站内" in google["note"]


# ---------------------------------------------------------------- 页面渲染

def test_search_page_renders_direct_bar_and_clickable_google_titles(env):
    _, settings, _ = env
    cl = TestClient(_app(settings))
    r = cl.get("/search?q=AI&scope=all&engines=google")
    assert r.status_code == 200
    assert "直接在 Google 中查看" in r.text
    assert "www.google.com/search?q=AI" in r.text
    assert 'target="_blank"' in r.text
    assert "Google 资讯（本地）" in r.text and "Google 资讯（全球）" in r.text
    assert "DuckDuckGo" in r.text
    assert "资讯结果" in r.text and "网页结果" in r.text
    assert "站内阅读" in r.text


def test_css_has_direct_bar_styles():
    from app import runtime
    css = (runtime.static_dir() / "app.css").read_text(encoding="utf-8")
    assert ".sch-direct {" in css and ".sch-ext {" in css
