"""采集层回归测试：URL 归一化、幂等入库、时间解析、HTML 误判。"""

from __future__ import annotations

import asyncio
import calendar
import time
from datetime import datetime, timezone

import httpx
import pytest

from app import db
from app.ingest.fetch import normalize_url, save_articles
from app.ingest.registry import Article
from app.ingest.rss import RSSSource, parse_time


def make(url: str, title: str = "标题", source: str = "t") -> Article:
    return Article(
        url=url, title=title, summary="", published_at=datetime.now(timezone.utc), lang="zh", source_id=source
    )


def test_normalize_url_strips_tracking():
    a = normalize_url("https://WWW.BBC.co.uk/news/x?at_campaign=rss&at_medium=custom7&id=3#top")
    b = normalize_url("https://www.bbc.co.uk/news/x?id=3")
    assert a == b == "https://www.bbc.co.uk/news/x?id=3"


def test_normalize_url_sorts_query():
    assert normalize_url("https://e.com/p?b=2&a=1") == normalize_url("https://e.com/p?a=1&b=2")


def test_save_articles_is_idempotent(tmp_path):
    path = tmp_path / "t.db"
    db.init_db(path)
    arts = [make(f"https://e.com/{i}") for i in range(3)]
    assert save_articles(path, arts) == 3
    assert save_articles(path, arts) == 0

    conn = db.connect(path)
    try:
        assert conn.execute("SELECT COUNT(*) FROM articles").fetchone()[0] == 3
    finally:
        conn.close()


def test_save_articles_drops_stale(tmp_path):
    path = tmp_path / "t.db"
    db.init_db(path)
    old = Article(
        url="https://e.com/old",
        title="旧闻",
        summary="",
        published_at=datetime(2018, 9, 23, tzinfo=timezone.utc),
        lang="zh",
        source_id="t",
    )
    assert save_articles(path, [old], max_age_days=7) == 0


def test_parse_time_is_utc():
    st = time.gmtime(calendar.timegm((2026, 11, 4, 2, 0, 0, 0, 0, 0)))
    dt = parse_time({"published_parsed": st})
    assert dt == datetime(2026, 11, 4, 2, 0, tzinfo=timezone.utc)


def test_rss_source_rejects_html():
    src = RSSSource(id="x", name="X", homepage="", lang="zh", feed="https://e.com/feed")

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, content=b"<!DOCTYPE html><html><body>not a feed</body></html>")

    async def run():
        async with httpx.AsyncClient(transport=httpx.MockTransport(handler)) as client:
            await src.fetch(client)

    with pytest.raises(RuntimeError, match="HTML"):
        asyncio.run(run())
