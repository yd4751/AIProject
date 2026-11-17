"""媒体源契约与注册表。

三类源：
1. 配置型 RSS —— sources.yaml 一条记录，由 RSSSource 统一实现（覆盖绝大多数媒体）
2. 配置型 HTML —— 无 RSS 但有可用列表页，`type: html` + `parser` 交给 HTMLSource
3. 代码型 —— 无 RSS 或要走 API 的，实现 Source 协议后 register()
"""

from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import Protocol

import httpx
import yaml

from app import runtime


@dataclass(slots=True)
class Article:
    url: str
    title: str
    summary: str
    published_at: datetime | None
    lang: str
    source_id: str
    raw_categories: list[str] = field(default_factory=list)
    # M6 社媒榜单专用；普通 RSS 源全用默认值，platform 为空表示不是榜单条目
    rank: int = 0
    heat_value: float = 0.0
    platform: str = ""
    extra: dict = field(default_factory=dict)


class Source(Protocol):
    id: str
    name: str
    homepage: str
    lang: str
    proxy_required: bool
    region: str | None

    async def fetch(self, client: httpx.AsyncClient) -> list[Article]: ...


_registry: dict[str, Source] = {}


def register(source: Source) -> None:
    _registry[source.id] = source


def all_sources() -> list[Source]:
    return sorted(_registry.values(), key=lambda s: s.id)


def get(source_id: str) -> Source | None:
    return _registry.get(source_id)


def load_sources_yaml(path: str | Path | None = None) -> int:
    """读取 sources.yaml 注册源。默认 RSSSource，`type: html` 走 HTMLSource。返回注册数量。"""
    from app.ingest.html_source import HTMLSource
    from app.ingest.rss import RSSSource

    p = Path(path) if path else runtime.sources_path()
    if not p.exists():
        return 0
    rows = yaml.safe_load(p.read_text(encoding="utf-8")) or []
    for row in rows:
        common = dict(
            id=row["id"],
            name=row["name"],
            homepage=row.get("homepage", ""),
            lang=row.get("lang", "en"),
            proxy_required=bool(row.get("proxy_required", False)),
            region=row.get("region") or None,
        )
        if str(row.get("type", "rss")).lower() == "html":
            register(
                HTMLSource(
                    **common,
                    url=row["url"],
                    parser=row["parser"],
                    base_url=row.get("base_url", ""),
                    limit=int(row.get("limit", 30)),
                )
            )
        else:
            register(RSSSource(**common, feed=row["feed"]))
    return len(rows)
