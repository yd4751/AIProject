"""通用 RSS/Atom 源。sources.yaml 里九成源都由它驱动，新增源不用写代码。"""

from __future__ import annotations

import asyncio
import calendar
import re
from dataclasses import dataclass
from datetime import datetime, timezone
from html import unescape

import feedparser
import httpx

from app.ingest.registry import Article

_TAG_RE = re.compile(r"<[^>]+>")
_WS_RE = re.compile(r"\s+")
# 部分中文源输出未转义的裸 & ，会让 XML 解析直接失败，先修再重试
_BARE_AMP_RE = re.compile(rb"&(?!(?:amp|lt|gt|quot|apos|#\d+|#x[0-9a-fA-F]+);)")


async def _get_with_retry(client: httpx.AsyncClient, url: str, attempts: int = 3) -> httpx.Response:
    """代理出口会出现间歇性连接重置（实测 BBC 首次必失败、重试即通），必须重试。"""
    last: Exception | None = None
    for i in range(attempts):
        try:
            resp = await client.get(url)
            if resp.status_code < 500:
                resp.raise_for_status()
                return resp
            last = httpx.HTTPStatusError(f"HTTP {resp.status_code}", request=resp.request, response=resp)
        except httpx.HTTPStatusError as exc:
            if exc.response.status_code < 500:
                raise
            last = exc
        except httpx.TransportError as exc:
            last = exc
        if i < attempts - 1:
            await asyncio.sleep(0.8 * (2**i))
    assert last is not None
    raise last


def strip_html(text: str) -> str:
    return _WS_RE.sub(" ", unescape(_TAG_RE.sub(" ", text or ""))).strip()


def parse_time(entry) -> datetime | None:
    # feedparser 的 *_parsed 是 UTC 的 struct_time，必须用 timegm，不能用 mktime（那是本地时区）
    for key in ("published_parsed", "updated_parsed", "created_parsed"):
        value = entry.get(key)
        if value:
            try:
                return datetime.fromtimestamp(calendar.timegm(value), tz=timezone.utc)
            except (OverflowError, ValueError, TypeError):
                continue
    return None


@dataclass(slots=True)
class RSSSource:
    id: str
    name: str
    homepage: str
    lang: str
    feed: str
    proxy_required: bool = False
    region: str | None = None   # 源属地兜底：国内媒体的国内稿标题里不会写"中国"

    async def fetch(self, client: httpx.AsyncClient) -> list[Article]:
        resp = await _get_with_retry(client, self.feed)

        head = resp.content[:200].lstrip().lower()
        if head.startswith(b"<!doctype html") or head.startswith(b"<html"):
            raise RuntimeError("返回 HTML 页面而不是 feed（源已停止供应 RSS）")

        parsed = await asyncio.to_thread(feedparser.parse, resp.content)

        if not parsed.entries:
            repaired = _BARE_AMP_RE.sub(b"&amp;", resp.content)
            if repaired != resp.content:
                parsed = await asyncio.to_thread(feedparser.parse, repaired)

        if not parsed.entries and parsed.bozo:
            raise RuntimeError(f"feed 解析失败: {parsed.get('bozo_exception')}")
        if not parsed.entries:
            raise RuntimeError("feed 中没有条目")

        seen: set[str] = set()
        articles: list[Article] = []
        for entry in parsed.entries:
            url = (entry.get("link") or "").strip()
            title = strip_html(entry.get("title", ""))
            if not url or not title or url in seen:
                continue
            seen.add(url)
            articles.append(
                Article(
                    url=url,
                    title=title,
                    summary=strip_html(entry.get("summary", "") or entry.get("description", ""))[:1200],
                    published_at=parse_time(entry),
                    lang=self.lang,
                    source_id=self.id,
                    raw_categories=[t.get("term", "") for t in entry.get("tags", []) if t.get("term")],
                )
            )
        return articles
