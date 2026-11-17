"""M7 站内阅读：服务端代取原网页，清洗后在站内渲染。

对外只暴露 read_page / fetch_raw / fetch_asset / status；
safety、extract、store、fetcher 各自可单独测试。
"""

from __future__ import annotations

from pathlib import Path

from app import config, db
from app.reader import store
from app.reader.extract import decode_html, extract_page
from app.reader.fetcher import fetch_asset, fetch_raw, read_page
from app.reader.safety import UnsafeURL, validate_url

__all__ = [
    "UnsafeURL", "validate_url", "read_page", "fetch_raw", "fetch_asset",
    "decode_html", "extract_page", "store", "status",
]


def status(db_path: str | Path, settings: config.Settings) -> dict:
    conn = db.connect(db_path)
    try:
        out = store.stats(conn)
    finally:
        conn.close()
    out.update({
        "enabled": settings.reader.enabled,
        "mode": "服务端代取 · 站内渲染",
        "proxy": settings.reader.proxy,
        "cache_hours": settings.reader.cache_hours,
        "max_bytes": settings.reader.max_bytes,
        "allowed_domains": settings.reader.allowed_domains,
        "proxy_images": settings.reader.proxy_images,
    })
    return out
