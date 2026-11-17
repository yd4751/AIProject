"""页面路由：M3 四视图 + 共用筛选条 + 行业页 + 事件详情（标签可人工锁定/屏蔽）。

所有视图共享 queries.build_where 的筛选语义，/api/events 也复用它，
保证「页面看到的」和「接口/SQL 直查」是同一批事件。
"""

from __future__ import annotations

import asyncio
import html as html_lib
import json
import re
import time
from datetime import datetime, timezone
from urllib.parse import parse_qs, quote, urlencode

from fastapi import APIRouter, BackgroundTasks, Request
from fastapi.responses import RedirectResponse, Response
from fastapi.templating import Jinja2Templates
from jinja2 import pass_context
from markupsafe import Markup

from app import config, db, notify, reader, runtime, search, translate
from app.ingest import registry, social
from app.ingest.fetch import run_once
from app.web import queries as q
from app.web import social_queries as sq

templates_dir = runtime.templates_dir()
templates = Jinja2Templates(directory=str(templates_dir))
router = APIRouter()

VIEWS = ("list", "quad", "dot", "matrix")
VIEW_LABELS = {
    "list": "榜单 · 事件卡", "quad": "热度 × 增速", "dot": "点阵", "matrix": "行业热力",
}


def local_time(ts: int | None) -> str:
    if not ts:
        return "—"
    dt = datetime.fromtimestamp(ts, tz=timezone.utc).astimezone()
    now = time.time()
    if now - ts < 3600:
        return f"{int((now - ts) / 60)} 分钟前"
    if now - ts < 86400:
        return f"{int((now - ts) / 3600)} 小时前"
    return dt.strftime("%m-%d %H:%M")


def filter_url(request: Request, **changes) -> str:
    """保留当前筛选条件，只改动指定参数，生成可分享的 URL。"""
    base = changes.pop("base", "/")
    params = {k: v for k, v in request.query_params.items() if v}
    for k, v in changes.items():
        if v in (None, ""):
            params.pop(k, None)
        else:
            params[k] = str(v)
    return base + ("?" + urlencode(params) if params else "")


def _tr_cache(request: Request, target: str) -> dict[str, str]:
    """每个请求只查一次译文缓存，模板里所有 tr_attr 共用，避免逐条查库（N+1）。"""
    state = request.state
    cached = getattr(state, "_tr_cache", None)
    if cached is None or cached[0] != target:
        conn = _conn(request)
        try:
            data = translate.load_cache(conn, target)
        finally:
            conn.close()
        state._tr_cache = (target, data)
    return state._tr_cache[1]


@pass_context
def tr_attr(ctx, text, scope: str = "title") -> Markup:
    """模板用：仅给外文文本打 data-tr，并带上原文与已缓存译文。

    scope 标记粒度：title（标题）/ para（段落、摘要、正文段）/ all（两者皆翻）。
    输出 Markup（动态内容已转义），模板里直接 {{ tr_attr(x) }}，无需 |safe。
    中文、纯数字等「已是目标语言」的文本返回空，保持原文且不进翻译范围。
    """
    request = ctx.get("request")
    if request is None or not text:
        return Markup("")
    s = str(text)
    target = request.app.state.settings.translate.target
    if not translate.needs_translation(s, target):
        return Markup("")
    zh = _tr_cache(request, target).get(translate.text_hash(s, target), "")
    return Markup(
        'data-tr data-text="%s" data-zh="%s" data-tr-scope="%s"'
        % (html_lib.escape(s, quote=True), html_lib.escape(zh, quote=True),
           html_lib.escape(scope if scope in ("title", "para") else "title", quote=True))
    )


def read_url(url: str) -> str:
    """模板用：把外站原文链接改写成站内阅读页地址（不产生任何外站直链）。"""
    if not url:
        return "/read"
    return "/read?url=" + quote(str(url), safe="")


def highlight(text, query: str) -> Markup:
    """搜索结果关键字高亮：先整体转义，再逐个关键字包 <mark>，不会引入 HTML 注入。"""
    out = html_lib.escape(str(text or ""), quote=False)
    for token in search.local.tokenize(query):
        needle = html_lib.escape(token, quote=False)
        if needle:
            out = re.sub("(" + re.escape(needle) + ")", r"<mark>\1</mark>", out, flags=re.I)
    return Markup(out)


templates.env.filters["localtime"] = local_time
templates.env.filters["highlight"] = highlight
templates.env.globals["filter_url"] = filter_url
templates.env.globals["read_url"] = read_url
templates.env.globals["tr_attr"] = tr_attr
templates.env.globals["DIM_LABELS"] = q.DIM_LABELS
templates.env.globals["INDUSTRY_COLORS"] = q.INDUSTRY_COLORS
templates.env.globals["DOT_DIMS"] = q.DOT_DIMS
templates.env.globals["VIEW_LABELS"] = VIEW_LABELS
templates.env.globals["VIEWS"] = VIEWS


def _conn(request: Request):
    return db.connect(config.abs_db_path(request.app.state.settings))


def _static_stamp() -> int:
    """静态资源最新修改时间：给 CSS/JS 加 ?v= 破缓存，也用于页面构建标记。"""
    stamps = []
    for name in ("app.css", "app.js"):
        try:
            stamps.append(int((runtime.static_dir() / name).stat().st_mtime))
        except OSError:
            continue
    return max(stamps) if stamps else 0


def _build_tag() -> str:
    ts = _static_stamp()
    return datetime.fromtimestamp(ts).strftime("%m-%d %H:%M") if ts else "dev"


def _engine_selection(request: Request) -> str:
    """URL 上的引擎选择，兼容复数 engines 与单数 engine 两种参数。"""
    return request.query_params.get("engines") or request.query_params.get("engine") or ""


def _engine_options(request: Request, settings: config.Settings) -> list[dict]:
    """搜索栏下拉：每个引擎一个单选项（默认引擎 / 谷歌引擎）。

    只有当用户通过 URL 或下方「多引擎」标签选了组合时，才临时补一条组合选项，
    保证下拉始终能正确反映当前 URL 的选中态；平时不带「全部引擎」这类第三项。
    """
    specs = search.engines.available(settings)
    selected = search.engines.resolve(_engine_selection(request), settings)
    opts = [{"value": e["id"], "label": e["label"], "subtitle": e["subtitle"]}
            for e in specs]
    if len(selected) > 1:
        cur = ",".join(selected)
        all_value = ",".join(e["id"] for e in specs)
        labels = " + ".join(next(e["label"] for e in specs if e["id"] == i)
                           for i in selected)
        opts.append({
            "value": cur,
            "label": "全部引擎" if cur == all_value else f"当前：{labels}",
            "subtitle": "按引擎独立展示多个引擎的结果",
        })
    return opts


def _site(request: Request, conn) -> dict:
    total = conn.execute("SELECT COUNT(*) FROM articles").fetchone()[0]
    today = conn.execute(
        "SELECT COUNT(*) FROM articles WHERE fetched_at >= ?", (int(time.time()) - 86400,)
    ).fetchone()[0]
    events = conn.execute("SELECT COUNT(*) FROM events").fetchone()[0]
    settings = request.app.state.settings
    selected = search.engines.resolve(_engine_selection(request), settings)
    return {
        "total": total, "today": today, "total_events": events,
        "unread_total": notify.unread_total(conn),
        "proxy": settings.proxy.url, "interval": settings.fetch.interval_minutes,
        "engine_options": _engine_options(request, settings),
        "engines_param": ",".join(selected),
        "asset_v": str(_static_stamp()),
        "build_tag": _build_tag(),
    }


def overview(request: Request, settings: config.Settings) -> dict:
    """源管理页用的状态汇总（M1 保留）。"""
    conn = db.connect(config.abs_db_path(settings))
    try:
        total = conn.execute("SELECT COUNT(*) FROM articles").fetchone()[0]
        today = conn.execute(
            "SELECT COUNT(*) FROM articles WHERE fetched_at >= ?", (int(time.time()) - 86400,)
        ).fetchone()[0]
        states = {r["source_id"]: dict(r) for r in conn.execute("SELECT * FROM source_state")}
        articles = [
            dict(r)
            for r in conn.execute(
                "SELECT a.title, a.url, a.lang, a.source_id, a.published_at, a.summary"
                " FROM articles a ORDER BY COALESCE(a.published_at, a.fetched_at) DESC LIMIT 30"
            )
        ]
    finally:
        conn.close()

    rss_sources: list[dict] = []
    social_sources: list[dict] = []
    for src in list(registry.all_sources()) + list(social.all_sources(enabled_only=False)):
        st = states.get(src.id, {})
        is_social = bool(getattr(src, "platform", ""))
        entry = {
            "id": src.id, "name": src.name, "lang": src.lang,
            "proxy_required": src.proxy_required,
            "kind": "social" if is_social else "rss",
            "status": st.get("last_status") or "未运行",
            "count": st.get("last_count") or 0,
            "fail_streak": st.get("fail_streak") or 0,
            "last_run": st.get("last_run"),
        }
        (social_sources if is_social else rss_sources).append(entry)
    sources = rss_sources + social_sources
    ok = sum(1 for s in sources if str(s["status"]).startswith("ok"))
    failed = sum(1 for s in sources if str(s["status"]).startswith("error"))
    return {
        "total": total, "today": today, "sources": rss_sources,
        "social_sources": social_sources, "sources_all": sources,
        "ok_count": ok, "fail_count": failed, "articles": articles,
        "proxy": settings.proxy.url, "interval": settings.fetch.interval_minutes,
    }


def _filters_from(region: str, industry: str, type_: str) -> dict:
    return {"region": region, "industry": industry, "type": type_}


# ---------------------------------------------------------------- 首页四视图

@router.get("/")
def index(request: Request, view: str = "list", region: str = "", industry: str = "",
          type: str = "", color: str = "industry"):
    view = view if view in VIEWS else "list"
    color = color if color in q.DOT_DIMS else "industry"
    filters = _filters_from(region, industry, type)
    conn = _conn(request)
    try:
        events = q.load_events(conn, filters)
        visible = q.visible_events(events)
        ctx = _site(request, conn)
        ctx.update(
            {
                "view": view, "filters": filters, "color": color,
                "options": q.tag_options(conn),
                "events": events, "visible": visible,
                "top": visible[:10], "feed": visible[:24],
                "quad": q.quad_data(visible[:40]),
                "quad_small": q.quad_data(visible[:24], small=True),
                "dot": q.dot_data(conn, filters, color, visible) if view == "dot" else None,
                "matrix": q.matrix_data(conn, filters) if view == "matrix" else None,
                "filtered_out": len(events) - len(visible),
            }
        )
    finally:
        conn.close()
    return templates.TemplateResponse(request=request, name="index.html", context=ctx)


# ---------------------------------------------------------------- 行业页

@router.get("/industries")
def industries_page(request: Request):
    conn = _conn(request)
    try:
        data = q.industry_overview(conn)
        ctx = _site(request, conn)
        ctx.update({"overview": data, "matrix": data["matrix"]})
    finally:
        conn.close()
    return templates.TemplateResponse(request=request, name="industries.html", context=ctx)


@router.get("/industry/{value}")
def industry_page(request: Request, value: str, region: str = "", type: str = ""):
    conn = _conn(request)
    try:
        lm = q.label_map(conn)
        if ("industry", value) not in lm:
            return RedirectResponse("/industries", status_code=303)
        filters = _filters_from(region, value, type)
        events = q.load_events(conn, filters)
        visible = q.visible_events(events)
        ctx = _site(request, conn)
        ctx.update(
            {
                "industry": value, "industry_label": lm[("industry", value)],
                "industry_color": q.INDUSTRY_COLORS.get(value, "#d0393b"),
                "filters": filters, "options": q.tag_options(conn),
                "events": events, "visible": visible, "feed": visible[:24],
                "timeline": q.industry_timeline(conn, value),
                "filtered_out": len(events) - len(visible),
            }
        )
    finally:
        conn.close()
    return templates.TemplateResponse(request=request, name="industry.html", context=ctx)


# ---------------------------------------------------------------- 社媒热榜（M6）

@router.get("/social")
def social_page(request: Request, platform: str = "", limit: int = 30):
    settings = request.app.state.settings
    limit = max(1, min(limit, 50))
    conn = _conn(request)
    try:
        data = sq.boards(conn, limit)
        status_rows = sq.status(conn)
        ctx = _site(request, conn)
        ctx.update(
            {
                "boards": data,
                "platforms": sq.PLATFORM_META,
                "selected": platform if platform in sq.PLATFORM_META else "",
                "resonance": sq.resonance(data),
                "status": status_rows,
                "ok_platforms": sum(1 for s in status_rows if str(s["status"]).startswith("ok")),
                "total_trends": sum(len(b["items"]) for b in data.values()),
                "cn_count": sum(1 for m in sq.PLATFORM_META.values() if m.get("region") == "cn"),
                "foreign_count": sum(1 for m in sq.PLATFORM_META.values() if m.get("region") == "global"),
                "captured_at": max((b["captured_at"] for b in data.values()), default=0),
                "inject": settings.social.inject_articles,
                "inject_top": settings.social.inject_top,
                "limit": limit,
            }
        )
    finally:
        conn.close()
    return templates.TemplateResponse(request=request, name="social.html", context=ctx)


@router.get("/api/social/trends")
def api_social_trends(request: Request, platform: str = "", limit: int = 30):
    conn = _conn(request)
    try:
        rows = sq.trends(conn, platform, limit)
        data = sq.boards(conn, limit)
        captured = max((b["captured_at"] for b in data.values()), default=0)
    finally:
        conn.close()
    keys = ("platform", "title", "url", "rank", "heat_value", "heat_label",
            "rank_delta", "delta_text", "summary", "captured_at")
    return {
        "count": len(rows), "platform": platform, "captured_at": captured,
        "platforms": sorted(data.keys()),
        "trends": [{k: r[k] for k in keys} for r in rows],
    }


@router.get("/api/social/status")
def api_social_status(request: Request):
    conn = _conn(request)
    try:
        data = sq.boards(conn, 1)
        out = sq.status(conn)
        for row in out:
            board = data.get(row["platform"])
            row["batch_id"] = board["batch_id"] if board else None
            row["captured_at"] = board["captured_at"] if board else None
        resonance = sq.resonance(sq.boards(conn, 30))
    finally:
        conn.close()
    return {"count": len(out), "platforms": out, "resonance": len(resonance)}


# ---------------------------------------------------------------- 事件详情

@router.get("/event/{event_id}")
def event_page(request: Request, event_id: int, saved: str = ""):
    conn = _conn(request)
    try:
        ev = q.event_detail(conn, event_id)
        if not ev:
            return RedirectResponse("/", status_code=303)
        ctx = _site(request, conn)
        ctx.update(
            {
                "ev": ev, "saved": saved,
                "options": q.tag_options(conn),
                "source_names": {
                    **{s.id: s.name for s in registry.all_sources()},
                    **{s.id: s.name for s in social.all_sources(enabled_only=False)},
                },
            }
        )
    finally:
        conn.close()
    return templates.TemplateResponse(request=request, name="event.html", context=ctx)


@router.post("/event/{event_id}/tags")
def edit_event_tag(request: Request, event_id: int, action: str = "add", v: str = ""):
    """人工编辑事件标签。

    locked=1  人工添加/锁定，刷新后保留；
    locked=-1 人工屏蔽，pipeline 重建标签时会跳过；
    restore   取消屏蔽并立即恢复为自动标签。
    """
    dim, _, value = (v or "").partition(":")
    conn = _conn(request)
    try:
        ev = conn.execute("SELECT id FROM events WHERE id = ?", (event_id,)).fetchone()
        valid = dim in q.DIMS and conn.execute(
            "SELECT 1 FROM tags WHERE dim = ? AND value = ?", (dim, value)
        ).fetchone()
        if ev and valid and action in ("add", "lock", "remove", "restore"):
            if action == "add":
                conn.execute(
                    "INSERT INTO event_tags (event_id, dim, value, hit_count, locked)"
                    " VALUES (?,?,?,999,1) ON CONFLICT(event_id, dim, value)"
                    " DO UPDATE SET locked=1, hit_count=MAX(hit_count, 999)",
                    (event_id, dim, value),
                )
            elif action == "lock":
                conn.execute(
                    "UPDATE event_tags SET locked=1 WHERE event_id=? AND dim=? AND value=?",
                    (event_id, dim, value),
                )
            elif action == "remove":
                conn.execute(
                    "INSERT INTO event_tags (event_id, dim, value, hit_count, locked)"
                    " VALUES (?,?,?,0,-1) ON CONFLICT(event_id, dim, value)"
                    " DO UPDATE SET locked=-1",
                    (event_id, dim, value),
                )
            else:  # restore
                conn.execute(
                    "DELETE FROM event_tags WHERE event_id=? AND dim=? AND value=? AND locked=-1",
                    (event_id, dim, value),
                )
                conn.execute(
                    "INSERT OR IGNORE INTO event_tags (event_id, dim, value, hit_count, locked)"
                    " VALUES (?,?,?,0,0)",
                    (event_id, dim, value),
                )
            conn.commit()
    finally:
        conn.close()
    return RedirectResponse(f"/event/{event_id}?saved=1", status_code=303)


# ---------------------------------------------------------------- 订阅页

@router.get("/subscriptions")
def subscriptions_page(request: Request, added: str = "", error: str = ""):
    conn = _conn(request)
    try:
        ctx = _site(request, conn)
        subs = [
            dict(r)
            for r in conn.execute(
                "SELECT * FROM subscriptions ORDER BY created_at DESC, id DESC"
            )
        ]
        for s in subs:
            s["description"] = notify.describe(s)
            s["filters"] = notify.parse_tag_filter(s["tag_filter"])
        hits = [
            dict(r)
            for r in conn.execute(
                "SELECT h.*, s.keyword, s.tag_filter FROM subscription_hits h"
                " JOIN subscriptions s ON s.id = h.subscription_id"
                " ORDER BY h.matched_at DESC, h.id DESC LIMIT 80"
            )
        ]
        for h in hits:
            h["description"] = notify.describe(h)
        ctx.update(
            {
                "subs": subs, "hits": hits, "options": q.tag_options(conn),
                "smtp_enabled": request.app.state.settings.smtp.enabled,
                "added": added, "error": error,
                "max_per_sub": notify.MAX_PER_SUB,
            }
        )
    finally:
        conn.close()
    return templates.TemplateResponse(request=request, name="subscriptions.html", context=ctx)


@router.post("/subscriptions/add")
async def add_subscription(request: Request):
    """表单是 application/x-www-form-urlencoded，手工解析即可，不引入 python-multipart。"""
    raw = (await request.body()).decode("utf-8", "replace")
    data = {k: v[0] for k, v in parse_qs(raw, keep_blank_values=True).items()}
    keyword = data.get("keyword", "").strip()[:200]
    tag_filter: dict[str, list[str]] = {}
    for dim in ("region", "industry", "type"):
        value = data.get(dim, "").strip()
        if value:
            tag_filter[dim] = [value]
    if not keyword and not tag_filter:
        return RedirectResponse("/subscriptions?error=empty", status_code=303)

    conn = _conn(request)
    try:
        conn.execute(
            "INSERT INTO subscriptions"
            " (keyword, tag_filter, created_at, last_matched_at, unread_count, notify_email)"
            " VALUES (?,?,?,NULL,0,?)",
            (
                keyword or None,
                json.dumps(tag_filter, ensure_ascii=False) if tag_filter else None,
                int(time.time()),
                1 if "notify_email" in data else 0,
            ),
        )
        conn.commit()
    finally:
        conn.close()
    return RedirectResponse("/subscriptions?added=1", status_code=303)


@router.post("/subscriptions/{sid}/read")
def mark_subscription_read(request: Request, sid: int):
    conn = _conn(request)
    try:
        conn.execute(
            "UPDATE subscription_hits SET unread=0 WHERE subscription_id=? AND unread=1", (sid,)
        )
        conn.execute("UPDATE subscriptions SET unread_count=0 WHERE id=?", (sid,))
        conn.commit()
    finally:
        conn.close()
    return RedirectResponse("/subscriptions", status_code=303)


@router.post("/subscriptions/{sid}/delete")
def delete_subscription(request: Request, sid: int):
    conn = _conn(request)
    try:
        conn.execute("DELETE FROM subscription_hits WHERE subscription_id=?", (sid,))
        conn.execute("DELETE FROM subscriptions WHERE id=?", (sid,))
        conn.commit()
    finally:
        conn.close()
    return RedirectResponse("/subscriptions", status_code=303)


@router.get("/api/subscriptions")
def api_subscriptions(request: Request):
    conn = _conn(request)
    try:
        subs = [
            {"id": r["id"], "keyword": r["keyword"], "tag_filter": r["tag_filter"],
             "unread_count": r["unread_count"], "notify_email": r["notify_email"]}
            for r in conn.execute("SELECT * FROM subscriptions ORDER BY id")
        ]
        unread = notify.unread_total(conn)
    finally:
        conn.close()
    return {"count": len(subs), "unread": unread, "subscriptions": subs}


# ---------------------------------------------------------------- 源管理 / 抓取

@router.get("/sources")
def sources_page(request: Request, started: str = ""):
    settings = request.app.state.settings
    data = overview(request, settings)
    data["started"] = started
    return templates.TemplateResponse(request=request, name="sources.html", context=data)


@router.post("/fetch")
def trigger_fetch(request: Request, background: BackgroundTasks):
    settings = request.app.state.settings
    background.add_task(run_once, settings, None)
    return RedirectResponse("/sources?started=1", status_code=303)


# ---------------------------------------------------------------- JSON 接口

@router.get("/api/stats")
def stats(request: Request):
    settings = request.app.state.settings
    data = overview(request, settings)
    conn = db.connect(config.abs_db_path(settings))
    try:
        events = conn.execute("SELECT COUNT(*) FROM events").fetchone()[0]
        hot = conn.execute("SELECT COUNT(*) FROM events WHERE heat > 0").fetchone()[0]
        unanalyzed = conn.execute("SELECT COUNT(*) FROM articles WHERE analyzed = 0").fetchone()[0]
        unread = notify.unread_total(conn)
        social_trends = conn.execute("SELECT COUNT(*) FROM social_trends").fetchone()[0]
        social_platforms = conn.execute(
            "SELECT COUNT(DISTINCT platform) FROM social_trends"
        ).fetchone()[0]
    finally:
        conn.close()
    snapshot = data["sources_all"]
    return {
        "total": data["total"], "today": data["today"],
        "ok": data["ok_count"], "fail": data["fail_count"],
        "events": events, "hot_events": hot, "unanalyzed": unanalyzed,
        "unread": unread,
        "social_trends": social_trends, "social_platforms": social_platforms,
        "sources": [
            {k: s[k] for k in ("id", "status", "count", "fail_streak")} for s in snapshot
        ],
    }


@router.get("/api/events")
def api_events(request: Request, limit: int = 20, region: str = "", industry: str = "",
               type: str = "", min_sources: int = 1, exclude_boilerplate: int = 0):
    """与首页共用 build_where；M3 验收拿它和 SQL 直查对拍。"""
    filters = _filters_from(region, industry, type)
    conn = _conn(request)
    try:
        where, params = q.build_where(conn, filters)
        if min_sources > 1:
            where.append("e.source_count >= ?")
            params.append(min_sources)
        sql = "SELECT e.* FROM events e" + (" WHERE " + " AND ".join(where) if where else "")
        sql += " ORDER BY e.heat DESC, e.last_seen DESC LIMIT ?"
        rows = conn.execute(sql, (*params, max(1, min(limit, 800)))).fetchall()
        events = []
        for r in rows:
            tags: dict[str, list[str]] = {}
            for t in conn.execute(
                "SELECT dim, value FROM event_tags WHERE event_id = ? AND locked <> -1"
                " ORDER BY dim, hit_count DESC",
                (r["id"],),
            ):
                tags.setdefault(t["dim"], []).append(t["value"])
            ev = {
                "id": r["id"], "title": r["title"], "category": r["category"],
                "heat": round(r["heat"] or 0, 3), "momentum": r["momentum"] or 0,
                "articles": r["article_count"], "sources": r["source_count"],
                "last_seen": r["last_seen"], "tags": tags,
            }
            ev["boilerplate"] = q._is_boilerplate(
                {"source_count": ev["sources"], "article_count": ev["articles"],
                 "title": ev["title"]}
            )
            if exclude_boilerplate and ev["boilerplate"]:
                continue
            events.append(ev)
    finally:
        conn.close()
    return {"count": len(events), "events": events}


# ---------------------------------------------------------------- 一键翻译（M5）

@router.post("/api/translate")
async def api_translate(request: Request):
    """批量翻译：命中缓存的直接返回，未命中的交给后端；结果写回 translations。"""
    settings = request.app.state.settings
    try:
        payload = await request.json()
    except Exception:
        payload = {}
    if not isinstance(payload, dict):
        payload = {}
    texts = payload.get("texts") or []
    if not isinstance(texts, list):
        texts = []
    target = str(payload.get("target") or settings.translate.target)
    return await asyncio.to_thread(
        translate.translate_texts,
        config.abs_db_path(settings), settings, [str(t) for t in texts], target,
    )


@router.get("/api/translate/status")
def api_translate_status(request: Request):
    settings = request.app.state.settings
    return translate.status(config.abs_db_path(settings), settings)


# ---------------------------------------------------------------- 站内阅读（M7）
# 浏览器只与本站通信：/read 由服务端代取原网页、清洗正文后渲染；
# 正文里的链接与图片也全部改写成站内地址，不产生外站直连。

TRANSPARENT_GIF = (
    b"GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!"
    b"\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00"
    b"\x00\x02\x02D\x01\x00;"
)
_READ_FIELDS = ("url", "final_url", "title", "site", "description", "content_html",
                "text_len", "status", "note", "cached", "transport", "fetched_at")


def _empty_read(url: str, note: str) -> dict:
    host = ""
    if "://" in url:
        host = url.split("://", 1)[1].split("/", 1)[0]
    return {
        "url": url, "final_url": url, "title": "", "site": host, "description": "",
        "content_html": "", "text_len": 0, "status": "error", "note": note,
        "cached": False, "transport": "", "fetched_at": 0,
    }


def _known_article(conn, url: str) -> dict | None:
    """原站抓不动时的降级内容：本站入库时已经存下来的标题与摘要。"""
    if not url:
        return None
    row = conn.execute(
        "SELECT title, summary, source_id, published_at, lang, event_id"
        " FROM articles WHERE url = ?", (url,)
    ).fetchone()
    if row:
        d = dict(row)
        d["kind"] = "article"
        return d
    row = conn.execute(
        "SELECT title, summary, platform, captured_at FROM social_trends"
        " WHERE url = ? ORDER BY captured_at DESC LIMIT 1", (url,)
    ).fetchone()
    if row:
        d = dict(row)
        d["kind"] = "trend"
        return d
    return None


@router.get("/read")
def read_page(request: Request, url: str = "", embed: int = 0, refresh: int = 0):
    """站内阅读页。embed=1 时由列表页的抽屉以 iframe 载入，隐藏全站导航。"""
    settings = request.app.state.settings
    conn = _conn(request)
    try:
        ctx = _site(request, conn)
        known = _known_article(conn, url)
    finally:
        conn.close()

    if not settings.reader.enabled:
        page = _empty_read(url, "站内阅读已在 config.yaml 的 reader.enabled 关闭")
    elif not url:
        page = _empty_read("", "缺少 URL 参数")
    else:
        page = reader.read_page(
            config.abs_db_path(settings), settings, url, refresh=bool(refresh)
        )
    ctx.update({
        "page": page, "known": known, "embed": bool(embed),
        "refresh": bool(refresh),
        "allow_external": settings.reader.allow_external,
        "active": "",
    })
    return templates.TemplateResponse(request=request, name="read.html", context=ctx)


@router.get("/api/read")
def api_read(request: Request, url: str = "", refresh: int = 0):
    settings = request.app.state.settings
    if not url or not settings.reader.enabled:
        return {"status": "error", "note": "缺少 URL 或站内阅读已关闭"}
    page = reader.read_page(config.abs_db_path(settings), settings, url, refresh=bool(refresh))
    conn = _conn(request)
    try:
        known = _known_article(conn, url)
    finally:
        conn.close()
    out = {k: page.get(k, "") for k in _READ_FIELDS}
    out["known"] = known
    return out


@router.get("/api/read/asset")
def api_read_asset(request: Request, url: str = ""):
    """正文图片也由服务端代取；失败时回一张透明图，避免破图与外站直连。"""
    settings = request.app.state.settings
    if not url or not settings.reader.enabled:
        return Response(TRANSPARENT_GIF, media_type="image/gif")
    try:
        data, ctype = reader.fetch_asset(url, settings)
    except Exception:
        return Response(TRANSPARENT_GIF, media_type="image/gif",
                        headers={"Cache-Control": "no-store"})
    return Response(data, media_type=ctype,
                    headers={"Cache-Control": "public, max-age=3600"})


@router.get("/api/read/status")
def api_read_status(request: Request):
    settings = request.app.state.settings
    out = reader.status(config.abs_db_path(settings), settings)
    out["search"] = search.status(config.abs_db_path(settings), settings)
    return out


# ---------------------------------------------------------------- 关键字搜索（M7）

@router.get("/search")
def search_page(request: Request, q: str = "", scope: str = "all", refresh: int = 0,
                engines: str = "", engine: str = ""):
    settings = request.app.state.settings
    scope = scope if scope in ("all", "local", "external") else "all"
    data = search.search(config.abs_db_path(settings), settings, q,
                         scope=scope, refresh=bool(refresh),
                         engines=engines or engine)
    conn = _conn(request)
    try:
        ctx = _site(request, conn)
    finally:
        conn.close()
    ctx.update({
        "q": data["query"], "data": data, "scope": scope,
        "active": "search", "refresh": bool(refresh),
        "external_enabled": settings.search.enabled and settings.search.external_enabled,
    })
    return templates.TemplateResponse(request=request, name="search.html", context=ctx)


@router.get("/api/search")
def api_search(request: Request, q: str = "", scope: str = "all", refresh: int = 0,
               engines: str = "", engine: str = ""):
    settings = request.app.state.settings
    scope = scope if scope in ("all", "local", "external") else "all"
    return search.search(config.abs_db_path(settings), settings, q,
                         scope=scope, refresh=bool(refresh),
                         engines=engines or engine)


@router.get("/api/search/status")
def api_search_status(request: Request):
    settings = request.app.state.settings
    return search.status(config.abs_db_path(settings), settings)
