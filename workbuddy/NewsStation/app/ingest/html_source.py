"""无 RSS 的媒体源：从列表页 HTML 提取「标题 + 链接」，构造标准 Article。

与 social/parsers.py 同构：解析器注册表 + YAML 配置化。新增这类站点只需
写一个解析函数、在 PARSERS 注册、在 sources.yaml 里加一条 `type: html`。
产出的是普通 Article，因此自动获得四维标签、事件聚类、订阅命中与一键翻译。

适用场景：官网已停止供应 RSS、且未被 WAF/SPA 挡住的列表页。若页面需要
JS 渲染或 Cookie（如虎嗅的阿里云 WAF 挑战页），此通道无能为力，应改走
官方/第三方 RSS 镜像（见 sources.yaml 里 anyfeeder 那几条）。
"""

from __future__ import annotations

import asyncio
import re
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Callable
from urllib.parse import urljoin

import httpx

from app.ingest.registry import Article
from app.ingest.rss import strip_html

_WS_RE = re.compile(r"\s+")


def _clean(text: str) -> str:
    return _WS_RE.sub(" ", strip_html(text)).strip()


def guancha(html: str, base: str, limit: int) -> list[dict]:
    """观察者网首页：/栏目/2026_11_12_908059.shtml，标题在 <a> 文本里。

    首页同一篇文章常出现多次（含 `#comment` 锚点），按绝对 URL 去重；
    日期从 URL 反解，让 max_age_days 过期过滤对本源同样生效。
    """
    pairs = re.findall(
        r'<a[^>]+href=["\']([^"\']+/20\d{2}_\d{2}_\d{2}_\d+\.shtml[^"\']*)["\'][^>]*>(.*?)</a>',
        html,
        re.S,
    )
    seen: set[str] = set()
    out: list[dict] = []
    for href, inner in pairs:
        title = _clean(inner)
        if len(title) < 8:
            continue
        url = urljoin(base, href.split("#")[0])
        if url in seen:
            continue
        seen.add(url)

        published = None
        m = re.search(r"/(20\d{2})_(\d{2})_(\d{2})_\d+\.shtml", href)
        if m:
            try:
                published = datetime(int(m[1]), int(m[2]), int(m[3]), tzinfo=timezone.utc)
            except ValueError:
                pass

        out.append({"title": title, "url": url, "summary": "", "published_at": published})
        if len(out) >= limit:
            break
    return out


PARSERS: dict[str, Callable[[str, str, int], list[dict]]] = {
    "guancha": guancha,
}


async def _get(client: httpx.AsyncClient, url: str, attempts: int = 2) -> httpx.Response:
    """国内直连站偶发连接重置，轻量重试一次即可。"""
    last: Exception | None = None
    for i in range(attempts):
        try:
            resp = await client.get(url)
            resp.raise_for_status()
            return resp
        except (httpx.HTTPStatusError, httpx.TransportError) as exc:
            last = exc
            if i < attempts - 1:
                await asyncio.sleep(0.6)
    assert last is not None
    raise last


@dataclass(slots=True)
class HTMLSource:
    id: str
    name: str
    homepage: str
    lang: str
    url: str
    parser: str
    base_url: str = ""
    proxy_required: bool = False
    region: str | None = None
    limit: int = 30

    async def fetch(self, client: httpx.AsyncClient) -> list[Article]:
        fn = PARSERS.get(self.parser)
        if fn is None:
            raise RuntimeError(f"未知 HTML 解析器: {self.parser}")
        resp = await _get(client, self.url)
        rows = fn(resp.text, self.base_url or self.homepage or self.url, self.limit)
        if not rows:
            raise RuntimeError("列表页未解析出条目（页面结构可能已改版）")
        return [
            Article(
                url=r["url"],
                title=r["title"],
                summary=r.get("summary", ""),
                published_at=r.get("published_at"),
                lang=self.lang,
                source_id=self.id,
            )
            for r in rows
        ]
