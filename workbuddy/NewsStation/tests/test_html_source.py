"""HTML 列表源（无 RSS 的媒体）回归测试：解析、去重、日期、源类型分发。"""

from __future__ import annotations

import asyncio

import httpx
import pytest

from app.ingest import registry
from app.ingest.html_source import HTMLSource, PARSERS, guancha
from app.ingest.rss import RSSSource

SAMPLE = """
<html><body>
<a href="/ZhengZhi/2026_11_12_908059.shtml">纪念孙中山先生诞辰160周年大会在京举行</a>
<a href="/ZhengZhi/2026_11_12_908059.shtml#comment">纪念孙中山先生诞辰160周年大会在京举行</a>
<a href="/GuoJi/2026_11_10_908027.shtml"><span>西班牙正被推入暴风眼</span></a>
<a href="/other/2026_11_12_1.shtml">短</a>
<a href="/index.html">首页导航</a>
</body></html>
"""


def test_guancha_parser_dedup_abs_url_and_date():
    rows = guancha(SAMPLE, "https://www.guancha.cn/", 30)
    assert len(rows) == 2  # 重复锚点去重、"短"标题过滤、非文章链接忽略
    assert rows[0]["title"].startswith("纪念孙中山")
    assert rows[0]["url"] == "https://www.guancha.cn/ZhengZhi/2026_11_12_908059.shtml"
    assert rows[0]["published_at"].date().isoformat() == "2026-11-12"
    assert rows[1]["title"] == "西班牙正被推入暴风眼"  # 内层 span 被剥离
    assert all(r["url"].startswith("https://") for r in rows)


def test_guancha_respects_limit():
    assert len(guancha(SAMPLE, "https://www.guancha.cn/", 1)) == 1


def test_parser_registry():
    assert "guancha" in PARSERS


def test_html_source_fetch():
    src = HTMLSource(
        id="guancha", name="观察者网", homepage="https://www.guancha.cn/",
        lang="zh", url="https://www.guancha.cn/", parser="guancha",
        base_url="https://www.guancha.cn/", limit=30,
    )

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, text=SAMPLE, headers={"content-type": "text/html; charset=utf-8"})

    async def run():
        async with httpx.AsyncClient(transport=httpx.MockTransport(handler)) as client:
            return await src.fetch(client)

    arts = asyncio.run(run())
    assert len(arts) == 2
    assert all(a.source_id == "guancha" and a.lang == "zh" for a in arts)
    assert arts[0].title and arts[0].published_at


def test_html_source_unknown_parser():
    src = HTMLSource(id="x", name="X", homepage="", lang="zh", url="https://e.com/", parser="nope")

    async def run():
        async with httpx.AsyncClient(transport=httpx.MockTransport(lambda r: httpx.Response(200, text=SAMPLE))) as c:
            await src.fetch(c)

    with pytest.raises(RuntimeError, match="未知 HTML 解析器"):
        asyncio.run(run())


def test_html_source_empty_page_raises():
    src = HTMLSource(id="x", name="X", homepage="", lang="zh", url="https://e.com/", parser="guancha")

    async def run():
        async with httpx.AsyncClient(transport=httpx.MockTransport(lambda r: httpx.Response(200, text="<html></html>"))) as c:
            await src.fetch(c)

    with pytest.raises(RuntimeError, match="未解析出条目"):
        asyncio.run(run())


def test_load_sources_yaml_dispatches_html_and_rss(tmp_path, monkeypatch):
    monkeypatch.setattr(registry, "_registry", {})  # 隔离全局注册表，避免污染其它测试
    p = tmp_path / "sources.yaml"
    p.write_text(
        """
- id: rss-one
  name: RSS 源
  lang: zh
  feed: https://e.com/feed.xml
- id: html-one
  name: HTML 源
  lang: zh
  region: cn
  type: html
  parser: guancha
  url: https://e.com/
  base_url: https://e.com/
  limit: 12
""",
        encoding="utf-8",
    )
    assert registry.load_sources_yaml(p) == 2
    r = registry.get("rss-one")
    h = registry.get("html-one")
    assert isinstance(r, RSSSource) and r.feed.endswith("feed.xml")
    assert isinstance(h, HTMLSource) and h.parser == "guancha" and h.limit == 12
    assert h.region == "cn" and h.proxy_required is False
