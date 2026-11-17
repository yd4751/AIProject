"""M7 站内阅读（服务端代取 + 正文清洗 + SSRF 防护）验收测试。

不依赖真实网络：正文代取用 httpx.MockTransport 注入样例响应；
SSRF 校验纯函数直接测；缓存用临时 SQLite。
"""

from __future__ import annotations

import time
from pathlib import Path
from urllib.parse import urlencode

import httpx
import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from app import config, db, reader
from app.reader import extract, safety, store
from app.web.routes import router


@pytest.fixture
def env(tmp_path):
    db_path = tmp_path / "reader.db"
    settings = config.Settings(db_path=str(db_path))
    return db_path, settings


SAMPLE_HTML = """<!doctype html><html><head><title>测试标题 - 某站</title>
<meta property="og:title" content="OG 标题">
<meta name="description" content="这是一段描述">
</head><body>
<nav>顶部噪声导航</nav>
<script>alert(1)</script>
<article>
  <h1>正文标题</h1>
  <p>第一段正文内容足够长以通过阈值，包含一些中文和 English words 用来测试清洗效果。</p>
  <p>第二段正文继续描述事件经过，提到<a href="/rel/link">一个站内相对链接</a>与
     <img src="//cdn.example.com/pic.png" alt="配图">一张图片。</p>
  <a href="javascript:alert(1)">危险链接</a>
</article>
<footer>页脚噪声</footer>
</body></html>"""


# ---------------------------------------------------------------- SSRF 防护

def test_ssrf_blocks_loopback_and_private():
    s = config.Settings()
    for u in [
        "http://127.0.0.1:8000/x",
        "http://localhost/x",
        "http://192.168.1.1/",
        "http://10.0.0.5:8080/a",
        "http://169.254.169.254/latest/meta-data/",
        "http://[::1]/x",
        "https://example.com:22/x",
        "file:///etc/passwd",
        "ftp://example.com/x",
        "http://user:pw@example.com/x",
    ]:
        with pytest.raises(reader.UnsafeURL):
            safety.validate_url(u, s, resolve=False)


def test_ssrf_blocks_even_with_resolve_false_for_ip_literal():
    # 回归：早期 resolve=False 时内网 IP 字面量被放行；现在必须拦截
    s = config.Settings()
    for u in ["http://192.168.0.1/", "http://172.16.5.5/", "http://169.254.169.254/"]:
        with pytest.raises(reader.UnsafeURL):
            safety.validate_url(u, s, resolve=False)


def test_ssrf_allows_public_https():
    s = config.Settings()
    out = safety.validate_url("https://www.bbc.co.uk/news/articles/abc?x=1#frag", s, resolve=False)
    assert out == "https://www.bbc.co.uk/news/articles/abc?x=1"


def test_ssrf_allowed_domains_whitelist():
    s = config.Settings(reader={"allowed_domains": ["example.com"]})
    # 白名单外被拒
    with pytest.raises(reader.UnsafeURL):
        safety.validate_url("https://other.com/x", s, resolve=False)
    # 白名单内放行
    assert safety.validate_url("https://example.com/a/b", s, resolve=False) == "https://example.com/a/b"


# ---------------------------------------------------------------- 正文清洗

def test_extract_strips_scripts_and_keeps_article():
    p = extract.extract_page(SAMPLE_HTML, "https://news.example.com/a/b", config.Settings())
    text = p["content_html"]
    assert "<script" not in text
    assert "alert(1)" not in text
    assert "顶部噪声导航" not in text
    assert "页脚噪声" not in text
    assert "第一段正文内容" in text
    assert "javascript:" not in text
    # 站内相对链接被改写成绝对路径，避免客户端直连外站
    assert "/rel/link" not in text
    assert "news.example.com" in text
    # 图片改写为 /api/read/asset 代理
    assert "/api/read/asset" in text
    assert p["title"] in ("测试标题", "OG 标题")


def test_extract_handles_non_utf8():
    # 把标记放进正文（head 会被丢弃），用 GBK 编码且不含 charset 声明
    raw = SAMPLE_HTML.replace("第一段正文内容", "GBK编码正文段落").encode("gbk")
    p = extract.extract_page(raw, "https://x.com/a", config.Settings())
    assert "GBK编码正文段落" in p["content_html"]


# ---------------------------------------------------------------- 缓存

def test_cache_miss_then_hit(env):
    db_path, settings = env
    db.init_db(db_path)
    page = reader.read_page(db_path, settings, "https://x.com/a", client=_mock_client(200, SAMPLE_HTML))
    assert page["status"] != "error"
    assert store.get(db.connect(db_path), "https://x.com/a", 3600) is not None
    # 二次调用应命中缓存，不再请求网络
    captured = []
    page2 = reader.read_page(db_path, settings, "https://x.com/a", client=_mock_client(500, "", capture=captured))
    assert page2.get("cached") is True
    assert captured == []  # 没有真正发起请求


def test_cache_expires_after_ttl(env):
    db_path, settings = env
    db.init_db(db_path)
    reader.read_page(db_path, settings, "https://x.com/a", client=_mock_client(200, SAMPLE_HTML))
    assert store.get(db.connect(db_path), "https://x.com/a", -1) is None  # 已过期


# ---------------------------------------------------------------- 阅读接口

def test_api_read_unsafe_url(env):
    db_path, settings = env
    db.init_db(db_path)
    app = _app(settings)
    cl = TestClient(app)
    # 内网地址在应用层也应被拒绝
    r = cl.get("/api/read?url=" + "http://192.168.1.1/")
    assert r.status_code == 200
    assert r.json()["status"] == "error"
    assert "拒绝" in r.json()["note"]


def test_read_page_renders_with_embed(env):
    db_path, settings = env
    db.init_db(db_path)
    app = _app(settings)
    cl = TestClient(app)
    # 注入一篇入库报道，验证降级内容（无网络也能展示本站原有摘要）
    _seed_article(db_path)
    r = cl.get("/read?url=" + "https://news.example.com/a" + "&embed=1")
    assert r.status_code == 200
    assert "重新获取" in r.text


def test_read_asset_falls_back_to_gif(env):
    db_path, settings = env
    db.init_db(db_path)
    app = _app(settings)
    cl = TestClient(app)
    r = cl.get("/api/read/asset?url=" + "http://127.0.0.1/x")  # 被拒
    assert r.status_code == 200
    assert r.headers["content-type"] == "image/gif"


# ---------------------------------------------------------------- helpers

def _mock_client(status, body, capture=None):
    def handler(request):
        if capture is not None:
            capture.append(request.url)
        return httpx.Response(
            status,
            content=body.encode("utf-8") if isinstance(body, str) else body,
            headers={"content-type": "text/html; charset=utf-8"},
        )
    return httpx.Client(transport=httpx.MockTransport(handler), trust_env=False)


def _app(settings):
    app = FastAPI()
    app.state.settings = settings
    app.include_router(router)
    return app


def _seed_article(db_path):
    c = db.connect(db_path)
    now = int(time.time())
    c.execute(
        "INSERT INTO articles(url,title,summary,source_id,published_at,fetched_at,lang,event_id,analyzed)"
        " VALUES(?,?,?,?,?,?,?,?,?)",
        ("https://news.example.com/a", "示例报道标题", "本站已有摘要", "bbc", now, now, "zh", None, 1),
    )
    c.commit()
    c.close()


# ---------------------------------------------------------------- M8 通道记忆

def test_route_memory_roundtrip(tmp_path):
    """记住的通道按域名复用；mock/空值不写；不同域名互不干扰。"""
    db_path = tmp_path / "route.db"
    db.init_db(db_path)
    conn = db.connect(db_path)
    try:
        assert store.preferred_transport(conn, "https://www.bbc.com/news/a") == ""
        store.remember_transport(conn, "https://www.bbc.com/news/a", "proxy")
        assert store.preferred_transport(conn, "https://www.bbc.com/news/b") == "proxy"
        assert store.preferred_transport(conn, "https://example.com/x") == ""
        store.remember_transport(conn, "https://www.bbc.com/news/a", "mock")
        assert store.preferred_transport(conn, "https://www.bbc.com/news/a") == "proxy"
        store.remember_transport(conn, "https://www.bbc.com/news/a", "direct")
        assert store.preferred_transport(conn, "https://www.bbc.com/news/a") == "direct"
    finally:
        conn.close()


def test_chain_prefers_remembered_proxy():
    settings = config.Settings()
    settings.proxy.url = "http://127.0.0.1:7890"
    settings.reader.proxy = "auto"
    from app.reader.fetcher import _chain

    assert [t for t, _ in _chain(settings)] == ["direct", "proxy"]
    assert [t for t, _ in _chain(settings, "proxy")] == ["proxy", "direct"]
    assert [t for t, _ in _chain(settings, "direct")] == ["direct", "proxy"]
    settings.reader.proxy = "force"
    assert [t for t, _ in _chain(settings, "proxy")] == ["proxy"]
    settings.reader.proxy = "direct"
    assert [t for t, _ in _chain(settings, "proxy")] == ["direct"]


def test_read_page_does_not_pollute_route_with_mock_client(env):
    """注入 mock client 属于测试路径，不应污染真实通道记忆。"""
    db_path, settings = env
    db.init_db(db_path)
    page = reader.read_page(db_path, settings, "https://x.com/a",
                            client=_mock_client(200, SAMPLE_HTML))
    assert page["status"] != "error"
    conn = db.connect(db_path)
    try:
        assert store.preferred_transport(conn, "https://x.com/a") == ""
    finally:
        conn.close()
