"""搜索服务：按「引擎」独立检索、独立代取、独立缓存、独立报错。

M7 原有的整套逻辑被归为 `default` 引擎（站内三组 + 三个外部源）；
M10 新增 `google` 引擎（Google 资讯 + Google 网页）。两者结果不混排，
页面上各自成区，任一引擎失败都不影响另一个。

为了兼容既有页面与 /api/search 的调用方，返回值仍保留 groups / external /
sources / errors / total 这些顶层字段（对应默认引擎），另新增 engines 区块列表。
"""

from __future__ import annotations

import socket
import time
from pathlib import Path
from urllib.parse import urlsplit

from app import config, db, reader
from app.reader import store
from app.search import local, providers
from app.search import engines as engine_reg


def _empty_groups() -> dict:
    return {
        "events": [], "articles": [], "trends": [],
        "counts": {"events": 0, "articles": 0, "trends": 0},
    }


def _proxy_alive(url: str, timeout: float = 0.6) -> bool:
    """端口探活：代理软件没开时立刻给出提示，别让用户干等一整轮超时。"""
    try:
        p = urlsplit(url)
        host, port = p.hostname, p.port or (443 if p.scheme == "https" else 80)
        if not host:
            return False
        with socket.create_connection((host, port), timeout=timeout):
            return True
    except OSError:
        return False


def _remote(name: str, query: str, settings: config.Settings,
            client, resolve: bool, opts: dict | None = None,
            prefer_proxy: bool = False, timeout: float | None = None,
            limit: int | None = None) -> list[dict]:
    url = providers.search_url(name, query, opts)
    if not url:
        return []
    # 实测 Bing RSS 约五成概率被连接重置（10054），与请求头无关；重试两次即可。
    # Google 优先走代理、内部已有 direct/proxy 两条通道，外层不再重复重试。
    attempts = 1 if (client is not None or name in providers.GOOGLE_PROVIDERS) else 3
    last = "代取失败"
    for attempt in range(attempts):
        r = reader.fetch_raw(
            url, settings, client=client, resolve=resolve,
            timeout=timeout or settings.search.timeout_seconds,
            extra_headers={"Accept": "application/rss+xml, application/xml, text/html, */*"},
            preferred="proxy" if prefer_proxy else "",
        )
        if r.get("ok"):
            return providers.parse(name, r["content"],
                                   limit or settings.search.max_results)
        last = r.get("error", last)
        if attempt < attempts - 1:
            time.sleep(0.4 * (attempt + 1))
    raise RuntimeError(last)


def _collect(names: list[str], query: str, settings: config.Settings,
             conn, refresh: bool, client, resolve: bool,
             opts: dict | None = None, prefer_proxy: bool = False,
             timeout: float | None = None,
             limit: int | None = None) -> tuple[list, list, list]:
    """一组 provider 的代取结果，逐源缓存、逐源隔离，最后按 URL 去重。

    空结果一律不写缓存：代理没开、Google 验证页等瞬时失败若被缓存 30 分钟，
    用户后来开了代理也只会一直看到空区块（实测踩过这个坑）。
    """
    items: list[dict] = []
    sources: list[dict] = []
    errors: list[str] = []
    seen: set[str] = set()
    ttl = settings.search.cache_minutes * 60

    for name in names:
        url = providers.search_url(name, query, opts)
        if not url:
            continue
        cached = None if refresh else store.search_get(conn, name, query, ttl)
        if not cached:                 # 历史遗留的空缓存也当作未命中，让它重新抓
            cached = None
        err = ""
        if cached is None:
            try:
                got = _remote(name, query, settings, client, resolve,
                              opts, prefer_proxy, timeout, limit)
                if not got and name in providers.GOOGLE_PROVIDERS + providers.WEB_PROVIDERS:
                    raise RuntimeError("未解析出结果（可能需开启代理或触发验证页）")
            except Exception as exc:
                got, err = [], str(exc)[:120]
                errors.append(f"{providers.LABELS.get(name, name)}：{err}")
            if got:
                store.search_put(conn, name, query, got)
        else:
            got = cached
        sources.append({
            "name": name, "label": providers.LABELS.get(name, name),
            "url": url, "count": len(got),
            "cached": cached is not None, "error": err,
        })
        for it in got:
            if it["url"] in seen:
                continue
            seen.add(it["url"])
            items.append(it)
    return items, sources, errors


def _default_block(q: str, settings: config.Settings, conn, scope: str,
                   refresh: bool, client, resolve: bool, groups: dict) -> dict:
    spec = dict(engine_reg.ENGINES["default"])
    external: list[dict] = []
    sources: list[dict] = []
    errors: list[str] = []
    if q and scope in ("all", "external") and settings.search.enabled \
            and settings.search.external_enabled:
        external, sources, errors = _collect(
            engine_reg.providers_of("default", settings), q, settings,
            conn, refresh, client, resolve,
        )
    # 外部结果与站内报道按 URL 去重（站内优先）
    seen = {a["url"] for a in groups["articles"]}
    deduped = []
    for it in external:
        if it["url"] in seen:
            continue
        seen.add(it["url"])
        deduped.append(it)
    external = deduped[: settings.search.max_results * 3]

    sections = [
        {"key": "events", "label": "热点事件", "items": groups["events"], "hint": "按热度排序"},
        {"key": "articles", "label": "站内报道", "items": groups["articles"], "hint": "按发布时间倒序"},
        {"key": "trends", "label": "社媒热榜", "items": groups["trends"], "hint": "最新一批榜单快照"},
        {"key": "external", "label": "全网结果", "items": external,
         "hint": "服务端代取 · 点击在站内阅读全文"},
    ]
    return {
        **spec,
        "groups": groups, "sections": sections, "items": external,
        "sources": sources, "errors": errors, "note": "",
        "count": sum(groups["counts"].values()) + len(external),
    }


def _external_block(engine_id: str, q: str, settings: config.Settings,
                    conn, scope: str, refresh: bool, client, resolve: bool) -> dict:
    """可选引擎（当前为谷歌）：只跑自己的 provider，结果不与默认引擎合并。"""
    spec = dict(engine_reg.ENGINES[engine_id])
    empty = {
        **spec, "groups": _empty_groups(), "sections": [], "items": [],
        "sources": [], "errors": [], "note": "", "count": 0,
        # 就算代理没开、搜索被关闭，也给用户一条直达 Google 的路
        "direct_urls": (providers.direct_google_urls(q, settings.search.google.url_opts)
                        if engine_id == "google" and q else []),
    }
    if not settings.search.enabled or not settings.search.external_enabled:
        empty["note"] = "全网搜索已在 config.yaml 关闭"
        return empty
    if not q:
        empty["note"] = "输入关键字后开始检索"
        return empty
    # 用户明确选了谷歌引擎就是要谷歌的结果：「仅站内」只约束默认引擎，
    # 否则选完谷歌还一片空白，是「几乎没有结果」的另一个成因。
    local_hint = ("当前为「仅站内」模式，该设置只作用于默认引擎；"
                  "已按所选谷歌引擎联网检索。" if scope == "local" else "")

    opts = settings.search.google.url_opts
    names = engine_reg.providers_of(engine_id, settings)
    ttl = settings.search.cache_minutes * 60
    has_cache = (not refresh) and any(
        bool(store.search_get(conn, n, q, ttl)) for n in names
    )
    # 真实运行时先探一下代理端口：代理软件没开就秒回提示，别让整页等满两轮超时。
    # 有缓存时跳过预检，关掉代理后仍能看上次抓到的结果。
    if client is None and not has_cache and settings.search.google.prefer_proxy:
        proxy = (settings.proxy.url or "").strip()
        if not proxy:
            empty["note"] = "谷歌引擎为境外站点：请先在 config.yaml 的 proxy.url 填入本地代理地址（默认引擎不受影响）"
            return empty
        if not _proxy_alive(proxy):
            empty["note"] = (f"谷歌引擎需要本地代理：config.yaml 的 proxy.url（{proxy}）"
                             "当前不可达，请先开启代理软件（默认引擎不受影响）")
            return empty

    news_names = [n for n in names if n not in providers.WEB_PROVIDERS]
    web_names = [n for n in names if n in providers.WEB_PROVIDERS]
    common = dict(conn=conn, refresh=refresh, client=client, resolve=resolve,
                  opts=opts, prefer_proxy=settings.search.google.prefer_proxy,
                  timeout=settings.search.google.timeout_seconds)
    items, sources, errors = _collect(
        news_names, q, settings, limit=settings.search.google.results_per_provider, **common)
    web_items, web_sources, web_errors = ([], [], [])
    if web_names and settings.search.google.web_results > 0:
        web_items, web_sources, web_errors = _collect(
            web_names, q, settings, limit=settings.search.google.web_results, **common)
    sources += web_sources
    errors += web_errors

    sections = []
    if items:
        sections.append({"key": "news", "label": "资讯结果", "items": items,
                         "hint": "Google 资讯 · 本地版 + 全球版", "google": True})
    if web_items:
        sections.append({"key": "web", "label": "网页结果", "items": web_items,
                         "hint": "DuckDuckGo 网页搜索 · 服务端代取 · 点击站内阅读"})

    note = ""
    if not items and not web_items and not errors:
        note = "未解析出结果（可能需开启代理或触发验证页）"
    elif not items and not web_items and errors:
        note = "该引擎本次没有取到结果；不开代理时 Google 通常不可达，详见下方源状态"
    if local_hint:
        note = (local_hint + " " + note).strip()
    return {
        **spec, "groups": _empty_groups(), "sections": sections,
        "items": items + web_items,
        "sources": sources, "errors": errors, "note": note,
        "count": len(items) + len(web_items),
        "direct_urls": providers.direct_google_urls(q, opts),
    }


def search(db_path: str | Path, settings: config.Settings, query: str,
           scope: str = "all", refresh: bool = False, client=None,
           resolve: bool = True, engines=None) -> dict:
    """scope: all / local / external；engines: 逗号分隔或列表，空值取 default_engine。"""
    q = (query or "").strip()[:120]
    tokens = local.tokenize(q)
    selected = engine_reg.resolve(engines, settings)

    conn = db.connect(db_path)
    try:
        # 站内检索只在选中默认引擎时做；无论 scope 都返回（沿用 M7 的页面语义）
        groups = local.search_all(conn, tokens, settings.search.local_limit) \
            if (q and "default" in selected) else _empty_groups()
        hot = local.hot_terms(conn, 12) if not q else []

        blocks: list[dict] = []
        for eid in selected:
            if eid == "default":
                blocks.append(_default_block(
                    q, settings, conn, scope, refresh, client, resolve, groups))
            else:
                blocks.append(_external_block(
                    eid, q, settings, conn, scope, refresh, client, resolve))
    finally:
        conn.close()

    # 顶层旧字段：仍指向默认引擎，保证既有调用方与测试不受影响
    default = next((b for b in blocks if b["id"] == "default"), None)
    legacy_groups = default["groups"] if default else _empty_groups()
    legacy_external = default["items"] if default else []
    legacy_sources = default["sources"] if default else []
    all_errors = [e for b in blocks for e in b["errors"]]

    return {
        "query": q, "tokens": tokens, "scope": scope,
        "selected": selected,
        "engines_param": ",".join(selected),
        "engine_choices": engine_reg.choices(selected, q, scope, settings),
        "engines": blocks,
        "groups": legacy_groups, "external": legacy_external,
        "sources": legacy_sources, "errors": all_errors, "hot": hot,
        "total": sum(b["count"] for b in blocks),
        "reader_url": "/read?url=",
    }


def status(db_path: str | Path, settings: config.Settings) -> dict:
    conn = db.connect(db_path)
    try:
        cached = conn.execute("SELECT COUNT(*) FROM search_cache").fetchone()[0]
    finally:
        conn.close()
    return {
        "enabled": settings.search.enabled,
        "external_enabled": settings.search.external_enabled,
        "providers": settings.search.providers,
        "cache_minutes": settings.search.cache_minutes,
        "cached_queries": cached,
        "default_engine": settings.search.default_engine,
        "engines": [
            {
                "id": e["id"], "label": e["label"],
                "providers": e["providers"],
                "enabled": (e["id"] != "google" or settings.search.google.enabled),
            }
            for e in engine_reg.available(settings)
        ],
    }
