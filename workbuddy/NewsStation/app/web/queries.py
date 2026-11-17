"""展示层查询：四个视图共用一套筛选语义，页面只读不重算热度。

设计要点
- 筛选落在 event_tags 上，用 EXISTS 表达「同时满足多个维度」，天然支持交叉；
- 地区选中「中国」时按 tags.parent 自动展开 cn/hk/mo/tw；
- locked=-1 是人工屏蔽标签，所有展示查询都排除它（见 pipeline._refresh_event_tags）；
- 点阵/矩阵/象限的坐标在服务端算好，模板只负责画 SVG，没有前端构建链。
"""

from __future__ import annotations

import json
import math
import random
import time
from collections import defaultdict
from datetime import datetime

DIMS = ("region", "industry", "type", "entity")
FILTER_DIMS = ("region", "industry", "type")
DIM_LABELS = {"region": "地区", "industry": "行业", "type": "类型", "entity": "实体"}
DOT_DIMS = ("region", "industry", "type")

# 行业固定顺序 + 固定 12 色调色板：点阵与热力矩阵共用，保证同一行业同色
INDUSTRY_ORDER = (
    "politics", "finance", "tech", "energy", "auto", "health",
    "consumer", "realestate", "defense", "culture", "society", "climate",
)
INDUSTRY_COLORS = {
    "politics": "#4c6ef5", "finance": "#e8590c", "tech": "#7048e8",
    "energy": "#f08c00", "auto": "#1098ad", "health": "#e64980",
    "consumer": "#66a80f", "realestate": "#845ef7", "defense": "#5c7cfa",
    "culture": "#f06595", "society": "#0ca678", "climate": "#82c91e",
}
EXTRA_COLORS = (
    "#4c6ef5", "#e8590c", "#7048e8", "#f08c00", "#1098ad", "#e64980",
    "#66a80f", "#845ef7", "#5c7cfa", "#f06595", "#0ca678", "#82c91e",
    "#d0393b", "#495057", "#087f5b", "#c2255c", "#364fc7", "#a61e4d",
)
NONE_COLOR = "#c8c8c3"

# 单源行情快讯模板稿：某财经源一天连发多条「XX ETF 标的指数涨超 N%」，
# 会自成一簇并挤进热度榜。热度榜/卡流剔除，但事件详情仍可访问。
_MARKET_WORDS = (
    "ETF", "标的指数", "跟踪指数", "指数大涨", "涨停", "涨超", "红盘",
    "短线拉升", "板块拉升", "净值", "盘中涨", "振幅",
)

HOUR = 3600
SLOT = 2 * HOUR


def _now() -> int:
    return int(time.time())


def label_map(conn) -> dict[tuple[str, str], str]:
    return {
        (r["dim"], r["value"]): r["label"]
        for r in conn.execute("SELECT dim, value, label FROM tags")
    }


def tag_options(conn) -> dict[str, list[dict]]:
    """筛选条可选项：按事件命中数降序，地区 52 个值全给（模板里折叠）。"""
    counts = {
        (r["dim"], r["value"]): r["n"]
        for r in conn.execute(
            "SELECT dim, value, COUNT(*) n FROM event_tags WHERE locked <> -1"
            " GROUP BY dim, value"
        )
    }
    out: dict[str, list[dict]] = {d: [] for d in DIMS}
    for r in conn.execute("SELECT dim, value, label FROM tags"):
        d, v = r["dim"], r["value"]
        out.setdefault(d, []).append(
            {"value": v, "label": r["label"], "count": counts.get((d, v), 0)}
        )
    order = {v: i for i, v in enumerate(INDUSTRY_ORDER)}
    for d in out:
        if d == "industry":
            out[d].sort(key=lambda x: (order.get(x["value"], 99), x["value"]))
        else:
            out[d].sort(key=lambda x: (-x["count"], x["value"]))
    return out


def expand_region(conn, value: str) -> list[str]:
    """选中国自动含港澳台；其余地区就是自身。"""
    if not value:
        return []
    values = {value}
    for r in conn.execute(
        "SELECT value FROM tags WHERE dim='region' AND parent=?", (value,)
    ):
        values.add(r["value"])
    return sorted(values)


def build_where(conn, filters: dict | None, alias: str = "e") -> tuple[list[str], list]:
    """筛选 → SQL 片段。所有视图、/api/events、测试都走这里，避免口径漂移。"""
    where: list[str] = []
    params: list = []
    for dim in FILTER_DIMS:
        value = (filters or {}).get(dim, "") or ""
        if not value:
            continue
        values = expand_region(conn, value) if dim == "region" else [value]
        ph = ",".join("?" * len(values))
        where.append(
            f"EXISTS (SELECT 1 FROM event_tags t WHERE t.event_id = {alias}.id"
            f" AND t.dim = ? AND t.value IN ({ph}) AND t.locked <> -1)"
        )
        params += [dim, *values]
    return where, params


def _where_sql(filters, conn, alias="e") -> tuple[str, list]:
    where, params = build_where(conn, filters, alias)
    return (" WHERE " + " AND ".join(where)) if where else "", params


def _is_boilerplate(ev: dict) -> bool:
    return (
        ev["source_count"] == 1
        and ev["article_count"] >= 3
        and any(w in ev["title"] for w in _MARKET_WORDS)
    )


def load_events(conn, filters: dict | None, limit: int = 800, now: int | None = None) -> list[dict]:
    """按热度取事件，附带四维标签与最新 3 条报道。一次批量查完，避免 N+1。"""
    where_sql, params = _where_sql(filters, conn)
    rows = conn.execute(
        "SELECT e.* FROM events e" + where_sql
        + " ORDER BY e.heat DESC, e.last_seen DESC LIMIT ?",
        (*params, limit),
    ).fetchall()
    if not rows:
        return []
    ids = [r["id"] for r in rows]
    ph = ",".join("?" * len(ids))
    lm = label_map(conn)

    tags: dict[int, dict[str, list[dict]]] = defaultdict(lambda: defaultdict(list))
    for t in conn.execute(
        f"SELECT event_id, dim, value, hit_count, locked FROM event_tags"
        f" WHERE locked <> -1 AND event_id IN ({ph})"
        " ORDER BY event_id, dim, hit_count DESC",
        ids,
    ):
        tags[t["event_id"]][t["dim"]].append(
            {
                "value": t["value"],
                "label": lm.get((t["dim"], t["value"]), t["value"]),
                "locked": t["locked"],
                "count": t["hit_count"],
            }
        )

    headlines: dict[int, list[dict]] = defaultdict(list)
    for a in conn.execute(
        f"SELECT id, event_id, title, url, source_id,"
        f" COALESCE(published_at, fetched_at) ts FROM articles"
        f" WHERE event_id IN ({ph}) ORDER BY ts DESC",
        ids,
    ):
        bucket = headlines[a["event_id"]]
        if len(bucket) < 3:
            bucket.append(
                {"title": a["title"], "url": a["url"], "source": a["source_id"], "ts": a["ts"]}
            )

    events = []
    for r in rows:
        tg = {d: tags[r["id"]].get(d, []) for d in DIMS}
        ev = dict(r)
        ev["tags"] = tg
        ev["tag_list"] = [t for d in DIMS for t in tg[d]]
        ev["headlines"] = headlines[r["id"]]
        ev["keywords"] = json.loads(r["keywords"] or "[]")
        m = ev["momentum"] or 0.0
        ev["trend"] = "up" if m > 0.15 else ("down" if m < -0.15 else "flat")
        ev["boilerplate"] = _is_boilerplate(ev)
        events.append(ev)
    return events


def visible_events(events: list[dict]) -> list[dict]:
    return [e for e in events if not e["boilerplate"]]


# ---------------------------------------------------------------- 热度 × 增速象限

def quad_data(events: list[dict], limit: int = 40, small: bool = False) -> dict:
    pts = events[:limit]
    w, h = (320, 210) if small else (560, 340)
    pad_l, pad_r, pad_t, pad_b = (32, 10, 12, 22) if small else (46, 18, 18, 32)
    heats = sorted((e["heat"] or 0.0) for e in pts)
    max_heat = max(heats + [1.0])
    mid = heats[len(heats) // 2] if heats else 0.0  # 热度中位数，与页面文案一致
    inner_w, inner_h = w - pad_l - pad_r, h - pad_t - pad_b
    points = []
    for e in pts:
        m = max(-1.0, min(1.0, e["momentum"] or 0.0))
        points.append(
            {
                "x": round(pad_l + min(e["heat"], max_heat) / max_heat * inner_w, 1),
                "y": round(pad_t + (1 - (m + 1) / 2) * inner_h, 1),
                "r": 4 + min(6, math.log1p(e["article_count"]) * 2),
                "id": e["id"],
                "title": e["title"],
                "short": e["title"][:14],
                "heat": round(e["heat"], 2),
                "momentum": round(e["momentum"] or 0, 2),
            }
        )
    # 只给热度前几名标号，避免糊成一团
    for p in points[: (3 if small else 5)]:
        p["label"] = p["short"]
    groups = {k: [] for k in ("爆发", "退潮", "新苗", "长尾")}
    for e, p in zip(pts, points):
        hot = (e["heat"] or 0) >= mid
        rising = (e["momentum"] or 0) >= 0
        key = ("爆发" if hot else "新苗") if rising else ("退潮" if hot else "长尾")
        groups[key].append({"id": e["id"], "title": e["title"], "heat": round(e["heat"], 2)})
    return {
        "w": w, "h": h, "pad_l": pad_l, "pad_t": pad_t, "pad_b": pad_b,
        "inner_w": inner_w, "inner_h": inner_h, "small": small,
        "fs": 9 if small else 11,
        "max_heat": round(max_heat, 2),
        "mid_x": round(pad_l + mid / max_heat * inner_w, 1),
        "zero_y": round(pad_t + inner_h / 2, 1),
        "points": points, "groups": groups,
    }


# ---------------------------------------------------------------- 点阵

def dot_data(conn, filters: dict | None, color_dim: str, events: list[dict],
             now: int | None = None) -> dict:
    """每点一篇文章，按事件成团；着色维度可切换。仅近 24h。"""
    now = now or _now()
    dim = color_dim if color_dim in DOT_DIMS else "industry"
    # 按报道篇数取样，让近 24h 的文章尽量都落在某个簇里（纯按热度会漏掉高密度旧簇）
    sel = sorted(events, key=lambda e: (-(e["article_count"] or 0), -(e["heat"] or 0)))[:80]
    ids = [e["id"] for e in sel]
    lm = label_map(conn)
    if not ids:
        return {"dim": dim, "points": [], "clusters": [], "legend": [], "w": 960, "h": 560,
                "total": 0, "events": 0, "none_color": NONE_COLOR}

    ph = ",".join("?" * len(ids))
    arts = conn.execute(
        f"SELECT a.id, a.event_id, a.title, a.source_id,"
        f" COALESCE(a.published_at, a.fetched_at) ts FROM articles a"
        f" WHERE a.event_id IN ({ph}) AND COALESCE(a.published_at, a.fetched_at) >= ?"
        " ORDER BY a.event_id, ts DESC",
        (*ids, now - 24 * HOUR),
    ).fetchall()
    art_ids = [a["id"] for a in arts]

    art_values: dict[int, str] = {}
    if art_ids:
        aph = ",".join("?" * len(art_ids))
        best: dict[int, tuple[float, str]] = {}
        for t in conn.execute(
            f"SELECT article_id, value, confidence FROM article_tags"
            f" WHERE dim = ? AND article_id IN ({aph})",
            (dim, *art_ids),
        ):
            conf = t["confidence"] or 0
            if t["article_id"] not in best or conf > best[t["article_id"]][0]:
                best[t["article_id"]] = (conf, t["value"])
        art_values = {k: v for k, (_, v) in best.items()}

    ev_first: dict[int, str] = {}
    for t in conn.execute(
        f"SELECT event_id, value FROM event_tags"
        f" WHERE dim = ? AND locked <> -1 AND event_id IN ({ph})"
        " ORDER BY event_id, hit_count DESC",
        (dim, *ids),
    ):
        ev_first.setdefault(t["event_id"], t["value"])

    by_event = {e["id"]: e for e in sel}
    values = sorted(
        {
            v for v in (*art_values.values(), *ev_first.values())
            if v and v in lm_keys(lm, dim)
        }
    )
    palette = {
        v: (INDUSTRY_COLORS.get(v) if dim == "industry" else None)
        or EXTRA_COLORS[i % len(EXTRA_COLORS)]
        for i, v in enumerate(values)
    }
    legend = [
        {"value": v, "label": lm.get((dim, v), v), "color": palette[v]}
        for v in values
    ]

    # 网格布簇心，簇内文章用 article_id 做种子的确定性抖动（刷新不跳）
    w, h, m = 960, 560, 30
    n = len(sel)
    cols = max(5, min(10, math.ceil(math.sqrt(n * 1.7))))
    rows_n = max(1, math.ceil(n / cols))
    cw, ch = (w - 2 * m) / cols, (h - 2 * m) / rows_n
    radius = min(cw, ch) * 0.40
    centers = {
        eid: (m + (i % cols + 0.5) * cw, m + (i // cols + 0.5) * ch)
        for i, eid in enumerate(ids)
    }

    points = []
    for a in arts:
        cx, cy = centers[a["event_id"]]
        rng = random.Random(a["id"] * 2654435761)
        ang = rng.random() * 2 * math.pi
        rad = math.sqrt(rng.random()) * radius
        value = art_values.get(a["id"]) or ev_first.get(a["event_id"]) or ""
        ev = by_event[a["event_id"]]
        points.append(
            {
                "x": round(cx + math.cos(ang) * rad, 1),
                "y": round(cy + math.sin(ang) * rad, 1),
                "color": palette.get(value, NONE_COLOR),
                "value": value,
                "value_label": lm.get((dim, value), "未标注") if value else "未标注",
                "eid": a["event_id"],
                "event": ev["title"],
                "count": ev["article_count"],
                "article": a["title"][:70],
                "source": a["source_id"],
            }
        )

    clusters = []
    for i, eid in enumerate(ids):
        cx, cy = centers[eid]
        ev = by_event[eid]
        clusters.append(
            {"x": round(cx, 1), "y": round(cy, 1), "r": round(radius, 1),
             "count": sum(1 for p in points if p["eid"] == eid),
             "label": ev["title"][:16], "id": eid}
        )

    return {
        "dim": dim, "w": w, "h": h, "points": points, "clusters": clusters,
        "legend": legend, "none_color": NONE_COLOR,
        "total": len(points), "events": n,
    }


def lm_keys(lm: dict, dim: str) -> set[str]:
    return {v for (d, v) in lm if d == dim}


# ---------------------------------------------------------------- 行业热力矩阵

def matrix_data(conn, filters: dict | None, now: int | None = None) -> dict:
    """行 = 12 行业，列 = 近 12 个 2 小时时段，色深 = 文章数。"""
    now = now or _now()
    where_sql, params = _where_sql(filters, conn)
    ids = [
        r["id"]
        for r in conn.execute("SELECT e.id FROM events e" + where_sql, params)
    ]
    lm = label_map(conn)
    cells = {v: [0] * 12 for v in INDUSTRY_ORDER}
    if ids:
        ph = ",".join("?" * len(ids))
        arts = conn.execute(
            f"SELECT a.id, a.event_id, COALESCE(a.published_at, a.fetched_at) ts"
            f" FROM articles a WHERE a.event_id IN ({ph})"
            " AND COALESCE(a.published_at, a.fetched_at) >= ?",
            (*ids, now - 24 * HOUR),
        ).fetchall()
        art_ids = [a["id"] for a in arts]
        inds: dict[int, list[str]] = defaultdict(list)
        if art_ids:
            aph = ",".join("?" * len(art_ids))
            for t in conn.execute(
                f"SELECT article_id, value FROM article_tags"
                f" WHERE dim='industry' AND article_id IN ({aph})",
                art_ids,
            ):
                inds[t["article_id"]].append(t["value"])
        ev_ind: dict[int, str] = {}
        for t in conn.execute(
            f"SELECT event_id, value FROM event_tags"
            f" WHERE dim='industry' AND locked <> -1 AND event_id IN ({ph})"
            " ORDER BY event_id, hit_count DESC",
            ids,
        ):
            ev_ind.setdefault(t["event_id"], t["value"])

        for a in arts:
            slot = int((a["ts"] - (now - 24 * HOUR)) // SLOT)
            slot = max(0, min(11, slot))  # 源里偶有未来时间戳，归入最后一列
            vals = inds.get(a["id"]) or ([ev_ind[a["event_id"]]] if a["event_id"] in ev_ind else [])
            for v in vals:
                if v in cells:
                    cells[v][slot] += 1

    start = now - 24 * HOUR
    cols = [
        {"i": i, "label": datetime.fromtimestamp(start + i * SLOT).strftime("%H:%M")}
        for i in range(12)
    ]
    rows = [
        {"value": v, "label": lm.get(("industry", v), v),
         "color": INDUSTRY_COLORS[v], "cells": cells[v], "total": sum(cells[v])}
        for v in INDUSTRY_ORDER
    ]
    max_count = max([c for r in rows for c in r["cells"]] + [1])
    return {"rows": rows, "cols": cols, "max": max_count,
            "total": sum(r["total"] for r in rows), "events": len(ids)}


# ---------------------------------------------------------------- 事件详情

def event_detail(conn, event_id: int, now: int | None = None) -> dict | None:
    now = now or _now()
    row = conn.execute("SELECT * FROM events WHERE id = ?", (event_id,)).fetchone()
    if not row:
        return None
    lm = label_map(conn)
    ev = dict(row)
    ev["keywords"] = json.loads(row["keywords"] or "[]")

    tags: dict[str, list[dict]] = {d: [] for d in DIMS}
    hidden: list[dict] = []
    for t in conn.execute(
        "SELECT dim, value, hit_count, locked FROM event_tags WHERE event_id = ?"
        " ORDER BY dim, hit_count DESC",
        (event_id,),
    ):
        item = {
            "dim": t["dim"], "value": t["value"],
            "label": lm.get((t["dim"], t["value"]), t["value"]),
            "locked": t["locked"], "count": t["hit_count"],
        }
        if t["locked"] == -1:
            hidden.append(item)
        else:
            tags.setdefault(t["dim"], []).append(item)
    ev["tags"] = tags
    ev["hidden"] = hidden

    articles = [
        dict(a)
        for a in conn.execute(
            "SELECT id, title, url, source_id, lang, summary,"
            " COALESCE(published_at, fetched_at) ts FROM articles"
            " WHERE event_id = ? ORDER BY ts DESC",
            (event_id,),
        )
    ]
    ev["articles"] = articles

    sources: dict[str, int] = defaultdict(int)
    hourly = [0] * 24
    for a in articles:
        sources[a["source_id"]] += 1
        slot = int((a["ts"] - (now - 24 * HOUR)) // HOUR)
        if 0 <= slot < 24:
            hourly[slot] += 1
    ev["hourly"] = hourly
    ev["hourly_max"] = max(hourly + [1])

    # 关系网络：中心事件 + 环形媒体节点
    src = sorted(sources.items(), key=lambda kv: -kv[1])
    cx, cy, R = 260, 190, 130
    max_n = max([n for _, n in src] + [1])
    nodes = []
    for i, (sid, n) in enumerate(src):
        ang = -math.pi / 2 + 2 * math.pi * i / max(1, len(src))
        nodes.append(
            {"id": sid, "count": n,
             "x": round(cx + math.cos(ang) * R, 1),
             "y": round(cy + math.sin(ang) * R, 1),
             "r": round(9 + 14 * n / max_n, 1)}
        )
    ev["network"] = {"cx": cx, "cy": cy, "nodes": nodes, "w": 520, "h": 380}

    # 相关事件：关键词/实体/标题/标签/时间的加权相似度，带关联理由
    from app.web.related import related_events

    ev["related"] = related_events(conn, event_id, limit=5, now=now)
    return ev


# ---------------------------------------------------------------- 行业页

def industry_overview(conn, now: int | None = None) -> dict:
    now = now or _now()
    lm = label_map(conn)
    ev_counts = {
        r["value"]: r["n"]
        for r in conn.execute(
            "SELECT value, COUNT(DISTINCT event_id) n FROM event_tags"
            " WHERE dim='industry' AND locked <> -1 GROUP BY value"
        )
    }
    heat = {
        r["value"]: r["h"]
        for r in conn.execute(
            "SELECT t.value, SUM(e.heat) h FROM event_tags t"
            " JOIN events e ON e.id = t.event_id"
            " WHERE t.dim='industry' AND t.locked <> -1 GROUP BY t.value"
        )
    }
    art_counts = {
        r["value"]: r["n"]
        for r in conn.execute(
            "SELECT at.value, COUNT(*) n FROM article_tags at"
            " JOIN articles a ON a.id = at.article_id"
            " WHERE at.dim='industry'"
            " AND COALESCE(a.published_at, a.fetched_at) >= ? GROUP BY at.value",
            (now - 24 * HOUR,),
        )
    }
    rows = [
        {"value": v, "label": lm.get(("industry", v), v), "color": INDUSTRY_COLORS[v],
         "events": ev_counts.get(v, 0), "articles": art_counts.get(v, 0),
         "heat": round(heat.get(v, 0.0), 1)}
        for v in INDUSTRY_ORDER
    ]
    return {
        "rows": rows, "matrix": matrix_data(conn, {}, now),
        "max_articles": max([r["articles"] for r in rows] + [1]),
    }


def industry_timeline(conn, value: str, now: int | None = None) -> dict:
    """某行业近 24h 的 12 个 2 小时时段文章数。"""
    now = now or _now()
    buckets = [0] * 12
    rows = conn.execute(
        "SELECT COALESCE(a.published_at, a.fetched_at) ts FROM articles a"
        " JOIN article_tags t ON t.article_id = a.id AND t.dim='industry' AND t.value = ?"
        " WHERE COALESCE(a.published_at, a.fetched_at) >= ?",
        (value, now - 24 * HOUR),
    ).fetchall()
    for r in rows:
        slot = max(0, min(11, int((r["ts"] - (now - 24 * HOUR)) // SLOT)))
        buckets[slot] += 1
    start = now - 24 * HOUR
    cols = [
        {"i": i, "n": buckets[i],
         "label": datetime.fromtimestamp(start + i * SLOT).strftime("%H:%M")}
        for i in range(12)
    ]
    return {"cols": cols, "max": max(buckets + [1]), "total": sum(buckets),
            "color": INDUSTRY_COLORS.get(value, "#d0393b")}
