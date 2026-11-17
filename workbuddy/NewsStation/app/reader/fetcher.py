"""服务端代取：浏览器只请求本站，由这里去原站取 HTML / 图片 / 搜索结果。

要点：
  - trust_env=False，忽略机器上的系统代理变量，连法完全由 config 决定；
  - 每一跳重定向都重新过 SSRF 校验（防 302 打内网）；
  - 流式读取，超过 reader.max_bytes 立即截断；
  - proxy=auto 时直连优先，失败/被限流再回退到 settings.proxy.url；
  - 按域名记住上次成功的通道（reader_routes），下次同域名直接优先它，
    建连单独限时，避免境外站每次都白等一次直连超时；
  - 任何异常都转成结构化错误页，不让单篇原文打挂整个站点。
"""

from __future__ import annotations

import time
from pathlib import Path
from urllib.parse import urljoin, urlsplit

import httpx

from app import config, db
from app.reader import safety, store
from app.reader.extract import decode_html, extract_page

REDIRECTS = (301, 302, 303, 307, 308)


def _headers(settings: config.Settings, url: str, extra: dict | None = None) -> dict:
    p = urlsplit(url)
    h = {
        "User-Agent": settings.reader.user_agent,
        "Accept": "text/html,application/xhtml+xml,application/xml,text/xml,*/*;q=0.8",
        "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
        "Referer": f"{p.scheme}://{p.netloc}/",
    }
    if extra:
        h.update(extra)
    return h


def _attempt(url: str, settings: config.Settings, proxy_url: str | None,
             client: httpx.Client | None, resolve: bool,
             max_bytes: int | None = None, timeout: float | None = None,
             extra_headers: dict | None = None,
             connect_timeout: float | None = None) -> dict:
    """单次尝试（固定一种连法），手动跟重定向并逐跳校验。"""
    limit = max_bytes or settings.reader.max_bytes
    tmo = timeout or settings.reader.timeout_seconds
    owns = client is None
    # 连接阶段单独设更短的超时：连不上的站快速失败、立即换下一条通道，
    # 不必等满整页读取超时（境外站直连不通时这是等待久的主因）。
    tmo_obj = httpx.Timeout(tmo, connect=connect_timeout or min(tmo, 5.0))
    cl = client or httpx.Client(
        proxy=proxy_url, trust_env=False, follow_redirects=False, timeout=tmo_obj,
    )
    current = url
    try:
        for hop in range(settings.reader.max_redirects + 1):
            current = safety.validate_url(current, settings, resolve=resolve)
            try:
                with cl.stream("GET", current, headers=_headers(settings, current, extra_headers)) as r:
                    if r.status_code in REDIRECTS and r.headers.get("location"):
                        nxt = urljoin(str(r.request.url), r.headers["location"])
                        safety.validate_url(nxt, settings, resolve=resolve)
                        current = nxt
                        continue
                    if r.status_code >= 400:
                        return {"ok": False, "status": r.status_code,
                                "error": f"原站返回 HTTP {r.status_code}", "final_url": current}
                    ctype = r.headers.get("content-type", "")
                    buf = bytearray()
                    truncated = False
                    for chunk in r.iter_bytes():
                        buf.extend(chunk)
                        if len(buf) >= limit:
                            truncated = True
                            break
                    return {
                        "ok": True, "status": r.status_code, "content": bytes(buf[:limit]),
                        "content_type": ctype, "final_url": str(r.request.url),
                        "truncated": truncated,
                    }
            except httpx.HTTPError as exc:
                return {"ok": False, "status": 0, "error": f"{type(exc).__name__}: {exc}"[:220],
                        "final_url": current}
        return {"ok": False, "status": 0, "error": "重定向次数过多", "final_url": current}
    finally:
        if owns:
            cl.close()


def _chain(settings: config.Settings,
           preferred: str = "") -> list[tuple[str, str | None]]:
    """auto 模式下若记得该域名上次成功的通道，就先走它，失败再试另一条。"""
    mode = (settings.reader.proxy or "auto").lower()
    proxy = settings.proxy.url or None
    if mode == "direct" or not proxy:
        return [("direct", None)]
    if mode == "force":
        return [("proxy", proxy)]
    if preferred == "proxy":
        return [("proxy", proxy), ("direct", None)]
    return [("direct", None), ("proxy", proxy)]


def fetch_raw(url: str, settings: config.Settings, client: httpx.Client | None = None,
              resolve: bool = True, max_bytes: int | None = None,
              timeout: float | None = None, extra_headers: dict | None = None,
              preferred: str = "") -> dict:
    """代取原始内容（HTML / RSS / 图片）。供阅读页与外部搜索共用。"""
    try:
        safe = safety.validate_url(url, settings, resolve=resolve)
    except safety.UnsafeURL as exc:
        return {"ok": False, "status": 0, "error": f"URL 被拒绝：{exc}", "final_url": url,
                "transport": ""}

    if client is not None:                    # 测试注入：不做代理回退
        r = _attempt(safe, settings, None, client, resolve, max_bytes, timeout, extra_headers)
        r["transport"] = "mock"
        return r

    last: dict = {"ok": False, "status": 0, "error": "未发起请求", "final_url": safe}
    for transport, proxy_url in _chain(settings, preferred):
        r = _attempt(safe, settings, proxy_url, None, resolve, max_bytes, timeout,
                     extra_headers, connect_timeout=settings.reader.connect_timeout_seconds)
        r["transport"] = transport
        if r.get("ok") and r.get("status", 0) < 400:
            return r
        last = r
        # 直连能连上但被业务层拒绝（403/429）时，换出口 IP 也可能有用
        if r.get("status", 0) not in (0, 403, 429):
            break
    return last


def _error_page(url: str, note: str) -> dict:
    return {
        "url": url, "final_url": url, "title": "", "site": urlsplit(url).hostname or "",
        "description": "", "content_html": "", "text_len": 0,
        "status": "error", "note": note, "cached": False, "transport": "",
        "fetched_at": int(time.time()),
    }


def read_page(db_path: str | Path, settings: config.Settings, url: str,
              refresh: bool = False, client: httpx.Client | None = None,
              resolve: bool = True) -> dict:
    """代取 + 清洗 + 缓存。返回可直接渲染的页面字典。"""
    try:
        safe = safety.validate_url(url, settings, resolve=resolve)
    except safety.UnsafeURL as exc:
        return _error_page(url, f"URL 被拒绝：{exc}")

    preferred = ""
    conn = db.connect(db_path)
    try:
        ttl = settings.reader.cache_hours * 3600
        if not refresh:
            hit = store.get(conn, safe, ttl)
            if hit:
                hit["cached"] = True
                hit["content_html"] = hit["content_html"] or ""
                return hit
        if settings.reader.remember_route and client is None:
            preferred = store.preferred_transport(conn, safe)
    finally:
        conn.close()

    r = fetch_raw(safe, settings, client=client, resolve=resolve, preferred=preferred)
    if not r.get("ok"):
        page = _error_page(safe, r.get("error", "无法取得原站内容"))
        page["transport"] = r.get("transport", "")
    else:
        ctype = (r.get("content_type") or "").lower()
        if "html" not in ctype and "xml" not in ctype and ctype:
            page = _error_page(safe, f"原站返回的是 {ctype.split(';')[0]}，不是可阅读的网页")
            page["final_url"] = r["final_url"]
        else:
            text = decode_html(r["content"], r.get("content_type", ""))
            page = extract_page(text, r["final_url"], settings)
            page.update({
                "url": safe, "final_url": r["final_url"],
                "cached": False, "transport": r.get("transport", ""),
                "fetched_at": int(time.time()),
            })
            if r.get("truncated"):
                page["note"] = (page["note"] + " 页面过大已截断。").strip()
    conn = db.connect(db_path)
    try:
        store.put(conn, safe, page, page.get("transport", ""))
        # 只有真正代取成功的通道才记，错误页不算
        if (settings.reader.remember_route and client is None
                and r.get("ok") and page.get("status") != "error"):
            store.remember_transport(conn, safe, page.get("transport", ""))
    finally:
        conn.close()
    return page


def fetch_asset(url: str, settings: config.Settings,
                client: httpx.Client | None = None, resolve: bool = True) -> tuple[bytes, str]:
    """代取正文图片；只放行 image/*，上限 3MB。"""
    r = fetch_raw(url, settings, client=client, resolve=resolve,
                  max_bytes=min(3_000_000, settings.reader.max_bytes))
    if not r.get("ok"):
        raise ValueError(r.get("error", "图片代取失败"))
    ctype = (r.get("content_type") or "").split(";")[0].strip().lower()
    if not ctype.startswith("image/"):
        raise ValueError("目标不是图片")
    return r["content"], ctype
