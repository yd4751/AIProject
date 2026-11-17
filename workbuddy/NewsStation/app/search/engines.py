"""搜索引擎注册表：默认引擎 + 可选引擎，结果互不混排。

- `default`：M7 原有的整套逻辑（站内事件/报道/热榜 + 搜狗资讯 / Bing RSS / 百度资讯）。
- `google`：M10 新增，Google 资讯 RSS（本地 + 全球）+ DuckDuckGo 网页结果。

引擎只负责「选择」与「展示元数据」；真正的代取、缓存、错误隔离在 service.py。
新增引擎：在 ENGINES / ENGINE_ORDER 加一项，给一组 provider，再在
config.yaml 的 `search.engines` 里挂上即可。
"""

from __future__ import annotations

from urllib.parse import quote

from app import config

ENGINE_ORDER = ("default", "google")

ENGINES = {
    "default": {
        "id": "default",
        "label": "默认引擎",
        "subtitle": "站内事件 / 报道 / 社媒热榜 + 搜狗资讯 / Bing / 百度资讯",
        "kind": "default",
    },
    "google": {
        "id": "google",
        "label": "谷歌引擎",
        "subtitle": "Google 资讯（本地 + 全球）+ 网页结果（DuckDuckGo）· 附 Google 直达入口（需代理）",
        "kind": "external",
    },
}


def providers_of(engine_id: str, settings: config.Settings) -> list[str]:
    if engine_id == "default":
        return list(settings.search.providers)
    if engine_id == "google":
        g = settings.search.google
        return [p for p in list(g.providers) + list(g.web_providers) if p]
    return list(getattr(settings.search, engine_id, None) or [])


def available(settings: config.Settings) -> list[dict]:
    """页面上可勾选的引擎（受 config.yaml 的 search.engines 控制）。"""
    out: list[dict] = []
    for eid in ENGINE_ORDER:
        if eid not in settings.search.engines:
            continue
        if eid == "google" and not settings.search.google.enabled:
            continue
        spec = dict(ENGINES[eid])
        spec["providers"] = providers_of(eid, settings)
        out.append(spec)
    if not out:                     # 配错了也不能把搜索页弄成空白
        spec = dict(ENGINES["default"])
        spec["providers"] = providers_of("default", settings)
        out.append(spec)
    return out


def resolve(selection, settings: config.Settings) -> list[str]:
    """把 URL 参数解析成有序、去重、合法的引擎 id 列表。

    `engines=default,google` / `engines=all` / 列表都支持；空值取 default_engine。
    未知或被禁用的 id 直接忽略，永远至少返回一个引擎。
    """
    ids = [e["id"] for e in available(settings)]
    if selection is None or selection == "":
        parts = [settings.search.default_engine]
    elif isinstance(selection, str):
        parts = [s.strip() for s in selection.replace(";", ",").split(",") if s.strip()]
    elif isinstance(selection, (list, tuple, set)):
        parts = [str(s).strip() for s in selection if str(s).strip()]
    else:
        parts = [settings.search.default_engine]
    if "all" in parts:
        parts = list(ids)
    chosen = [i for i in ids if i in parts]
    if not chosen:
        fallback = settings.search.default_engine
        chosen = [fallback if fallback in ids else ids[0]]
    return chosen


def choices(selected: list[str], query: str, scope: str,
            settings: config.Settings) -> list[dict]:
    """给模板生成引擎 chip：点已选中=取消，点未选中=加上；不允许取消到 0 个。"""
    ids = [e["id"] for e in available(settings)]
    out: list[dict] = []
    for spec in available(settings):
        on = spec["id"] in selected
        if on:
            nxt = [x for x in selected if x != spec["id"]]
            if not nxt:             # 最后一个引擎不允许取消
                nxt = list(selected)
        else:
            nxt = selected + [spec["id"]]
        nxt = [i for i in ENGINE_ORDER if i in nxt]
        href = "/search?q={}&scope={}&engines={}".format(
            quote(query or "", safe=""),
            quote(scope, safe=""),
            quote(",".join(nxt) or ids[0], safe=","),
        )
        out.append({**spec, "selected": on, "href": href})
    return out
