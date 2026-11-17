"""站内阅读缓存：服务端代取过的页面按 URL hash 存正文，重复点击不再打原站。"""

from __future__ import annotations

import hashlib
import json
import sqlite3
import time
from pathlib import Path
from urllib.parse import urlsplit

from app import config


def url_hash(url: str) -> str:
    return hashlib.sha256(url.encode("utf-8", "replace")).hexdigest()


def get(conn: sqlite3.Connection, url: str, ttl_seconds: float) -> dict | None:
    row = conn.execute(
        "SELECT * FROM reader_cache WHERE url_hash = ?", (url_hash(url),)
    ).fetchone()
    if not row:
        return None
    age = int(time.time()) - (row["fetched_at"] or 0)
    # 失败结果只缓存 10 分钟，避免网络恢复后一直显示错误
    limit = 600 if row["status"] == "error" else ttl_seconds
    if age > limit:
        return None
    return dict(row)


def put(conn: sqlite3.Connection, url: str, page: dict, transport: str) -> None:
    conn.execute(
        "INSERT INTO reader_cache"
        " (url_hash, url, final_url, title, site, description, content_html,"
        "  text_len, status, note, transport, fetched_at)"
        " VALUES (?,?,?,?,?,?,?,?,?,?,?,?)"
        " ON CONFLICT(url_hash) DO UPDATE SET"
        "  final_url=excluded.final_url, title=excluded.title, site=excluded.site,"
        "  description=excluded.description, content_html=excluded.content_html,"
        "  text_len=excluded.text_len, status=excluded.status, note=excluded.note,"
        "  transport=excluded.transport, fetched_at=excluded.fetched_at",
        (
            url_hash(url), url, page.get("final_url") or url, page.get("title", ""),
            page.get("site", ""), page.get("description", ""),
            page.get("content_html", ""), page.get("text_len", 0),
            page.get("status", "ok"), page.get("note", ""), transport, int(time.time()),
        ),
    )
    conn.commit()


def route_host(url: str) -> str:
    return (urlsplit(url).hostname or "").lower()


def preferred_transport(conn: sqlite3.Connection, url: str) -> str:
    """该域名上次走哪条通道成功的；没有记录返回空串。"""
    host = route_host(url)
    if not host:
        return ""
    row = conn.execute(
        "SELECT transport FROM reader_routes WHERE host = ?", (host,)
    ).fetchone()
    return (row["transport"] if row else "") or ""


def remember_transport(conn: sqlite3.Connection, url: str, transport: str) -> None:
    """只记成功通道；direct/proxy 之外的（mock、空）不记。"""
    host = route_host(url)
    if not host or transport not in ("direct", "proxy"):
        return
    conn.execute(
        "INSERT INTO reader_routes (host, transport, updated_at) VALUES (?,?,?)"
        " ON CONFLICT(host) DO UPDATE SET"
        " transport=excluded.transport, updated_at=excluded.updated_at",
        (host, transport, int(time.time())),
    )
    conn.commit()


def purge(conn: sqlite3.Connection, keep_days: int = 14) -> int:
    cur = conn.execute(
        "DELETE FROM reader_cache WHERE fetched_at < ?",
        (int(time.time()) - keep_days * 86400,),
    )
    conn.execute(
        "DELETE FROM search_cache WHERE fetched_at < ?",
        (int(time.time()) - keep_days * 86400,),
    )
    conn.commit()
    return cur.rowcount or 0


def stats(conn: sqlite3.Connection) -> dict:
    rows = conn.execute(
        "SELECT status, COUNT(*) n, MAX(fetched_at) last FROM reader_cache GROUP BY status"
    ).fetchall()
    by_status = {r["status"]: r["n"] for r in rows}
    return {
        "cached": sum(by_status.values()),
        "ok": by_status.get("ok", 0),
        "thin": by_status.get("thin", 0),
        "error": by_status.get("error", 0),
        "last_fetched": max((r["last"] for r in rows), default=0),
        "search_cached": conn.execute("SELECT COUNT(*) FROM search_cache").fetchone()[0],
    }


# ---------------------------------------------------------------- 外部搜索结果缓存

def search_key(provider: str, query: str) -> str:
    return hashlib.sha256(f"{provider}\n{query}".encode("utf-8")).hexdigest()


def search_get(conn: sqlite3.Connection, provider: str, query: str,
               ttl_seconds: float) -> list[dict] | None:
    row = conn.execute(
        "SELECT payload, fetched_at FROM search_cache WHERE key = ?",
        (search_key(provider, query),),
    ).fetchone()
    if not row or int(time.time()) - row["fetched_at"] > ttl_seconds:
        return None
    try:
        data = json.loads(row["payload"])
        return data if isinstance(data, list) else None
    except Exception:
        return None


def search_put(conn: sqlite3.Connection, provider: str, query: str,
               items: list[dict]) -> None:
    conn.execute(
        "INSERT INTO search_cache (key, provider, query, payload, fetched_at)"
        " VALUES (?,?,?,?,?) ON CONFLICT(key) DO UPDATE SET"
        " payload=excluded.payload, fetched_at=excluded.fetched_at",
        (search_key(provider, query), provider, query,
         json.dumps(items, ensure_ascii=False), int(time.time())),
    )
    conn.commit()
