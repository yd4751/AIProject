"""社交媒体热榜源：契约、注册表、YAML 装载。

与 RSS 源分开注册，但实现同一个 `Source` 协议（id/name/lang/proxy_required/region/fetch），
所以抓取编排能用完全相同的并发与状态记录逻辑，只是返回带 `platform` 的 Article。
分开的好处：现有 registry 的用户（/sources、pipeline 地区兜底、既有测试）不会被自动
塞进 5 个网络源，需要时显式取用。
"""

from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path
from typing import Protocol

import httpx
import yaml

from app import runtime
from app.ingest.registry import Article


class ParserFn(Protocol):
    def __call__(self, text: str, limit: int) -> list[dict]: ...


@dataclass(slots=True)
class SocialSource:
    id: str
    name: str
    platform: str
    homepage: str
    url: str
    parser: str
    lang: str = "zh"
    region: str | None = "cn"
    proxy_required: bool = False
    enabled: bool = True
    limit: int = 30
    inject_top: int | None = None   # 覆盖全局 social.inject_top；None 时用全局值
    headers: dict[str, str] = field(default_factory=dict)

    async def fetch(self, client: httpx.AsyncClient) -> list[Article]:
        from app.ingest.social.parsers import PARSERS

        fn: ParserFn | None = PARSERS.get(self.parser)
        if fn is None:
            raise RuntimeError(f"未知解析器 {self.parser}")
        resp = await client.get(self.url, headers=self.headers or {})
        resp.raise_for_status()
        rows = fn(resp.text, self.limit)
        if not rows:
            raise RuntimeError("榜单为空或字段已变更")
        now = datetime.now(timezone.utc)
        return [
            Article(
                url=r["url"],
                title=r["title"],
                summary=r.get("summary", ""),
                published_at=now,
                lang=self.lang,
                source_id=self.id,
                raw_categories=[self.platform, "社媒热点"],
                rank=r.get("rank", i + 1),
                heat_value=r.get("heat_value", 0.0),
                platform=self.platform,
                extra=r.get("extra", {}),
            )
            for i, r in enumerate(rows)
        ]


_registry: dict[str, SocialSource] = {}


def register(source: SocialSource) -> None:
    _registry[source.id] = source


def all_sources(enabled_only: bool = True) -> list[SocialSource]:
    rows = sorted(_registry.values(), key=lambda s: s.id)
    return [s for s in rows if s.enabled] if enabled_only else rows


def get(source_id: str) -> SocialSource | None:
    return _registry.get(source_id)


def clear() -> None:
    """测试用：清空注册表。"""
    _registry.clear()


def load_social_yaml(path: str | Path | None = None) -> int:
    """读取项目根的 social_sources.yaml。返回注册数量（含 disabled）。"""
    p = Path(path) if path else runtime.social_sources_path()
    if not p.exists():
        return 0
    rows = yaml.safe_load(p.read_text(encoding="utf-8")) or []
    for row in rows:
        register(
            SocialSource(
                id=row["id"],
                name=row["name"],
                platform=row["platform"],
                homepage=row.get("homepage", ""),
                url=row["url"],
                parser=row["parser"],
                lang=row.get("lang", "zh"),
                region=row.get("region") or None,
                proxy_required=bool(row.get("proxy_required", False)),
                enabled=bool(row.get("enabled", True)),
                limit=int(row.get("limit", 30)),
                inject_top=int(row["inject_top"]) if row.get("inject_top") is not None else None,
                headers=dict(row.get("headers") or {}),
            )
        )
    return len(rows)
