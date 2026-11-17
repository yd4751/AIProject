"""M6 社媒热榜展示查询层。

榜单是「每平台一批快照」，所以查询总是先找各平台的最新 batch_id，
再取该批次的条目；排名升降在落库时已算好（rank_delta，NULL = 新进榜）。
"""

from __future__ import annotations

import json
import re

# 平台元数据：展示顺序、名称、品牌色、徽标文字。境外平台见下方 hackernews/lobsters/github。
PLATFORM_META = {
    "weibo": {"name": "微博热搜", "short": "微博", "color": "#e6162d", "badge": "微", "kind": "word", "region": "cn"},
    "baidu": {"name": "百度热搜", "short": "百度", "color": "#2932e1", "badge": "百", "kind": "word", "region": "cn"},
    "toutiao": {"name": "头条热榜", "short": "头条", "color": "#f04142", "badge": "头", "kind": "word", "region": "cn"},
    "douyin": {"name": "抖音热点", "short": "抖音", "color": "#161823", "badge": "抖", "kind": "word", "region": "cn"},
    "bilibili": {"name": "B 站热门", "short": "B 站", "color": "#00a1d6", "badge": "B", "kind": "video", "region": "cn"},
    "hackernews": {"name": "Hacker News", "short": "HN", "color": "#ff6600", "badge": "H", "kind": "word", "region": "global"},
    "lobsters": {"name": "Lobsters", "short": "Lobsters", "color": "#b9322d", "badge": "L", "kind": "word", "region": "global"},
    "github": {"name": "GitHub 趋势", "short": "GitHub", "color": "#24292f", "badge": "G", "kind": "dev", "region": "global"},
}

_PUNCT_RE = re.compile(r"[\s#，,。！？!?、：:；;「」『』\"'（）()【】\[\]《》<>~·\-—_]+")


def _norm(title: str) -> str:
    return _PUNCT_RE.sub("", (title or "")).lower()


def heat_label(value) -> str:
    n = float(value or 0)
    if n >= 1e8:
        return f"{n / 1e8:.1f}亿"
    if n >= 1e4:
        return f"{n / 1e4:.1f}万"
    return str(int(n))


def delta_label(delta) -> tuple[str, str]:
    """(文字, 样式类)。"""
    if delta is None:
        return "新", "new"
    if delta > 0:
        return f"↑{delta}", "up"
    if delta < 0:
        return f"↓{-delta}", "down"
    return "—", "flat"


def _decorate(row: dict) -> dict:
    item = dict(row)
    try:
        item["extra_obj"] = json.loads(item.get("extra") or "{}")
    except (TypeError, ValueError):
        item["extra_obj"] = {}
    item["heat_label"] = heat_label(item.get("heat_value"))
    item["delta_text"], item["delta_class"] = delta_label(item.get("rank_delta"))
    return item


def boards(conn, limit: int = 30) -> dict:
    """{platform: {batch_id, captured_at, items}}，按 PLATFORM_META 顺序。"""
    limit = max(1, min(int(limit), 50))
    lasts = {
        r["platform"]: r["b"]
        for r in conn.execute(
            "SELECT platform, MAX(batch_id) AS b FROM social_trends GROUP BY platform"
        )
    }
    out: dict = {}
    for platform in PLATFORM_META:
        batch = lasts.get(platform)
        if batch is None:
            continue
        rows = conn.execute(
            "SELECT * FROM social_trends WHERE platform = ? AND batch_id = ?"
            " ORDER BY rank LIMIT ?",
            (platform, batch, limit),
        ).fetchall()
        items = [_decorate(dict(r)) for r in rows]
        out[platform] = {
            "batch_id": batch,
            "captured_at": items[0]["captured_at"] if items else 0,
            "items": items,
        }
    return out


def resonance(data: dict, min_platforms: int = 2, top: int = 10) -> list[dict]:
    """跨平台共振：同一话题在多个热搜词平台同时上榜。

    只比较「词榜」类平台（微博/百度/头条/抖音），B 站是视频标题，
    长标题容易包含热搜词造成误合并，不参与聚合。
    """
    entries: list[tuple[str, dict, str]] = []
    for platform, board in data.items():
        if PLATFORM_META.get(platform, {}).get("kind") != "word":
            continue
        for item in board["items"]:
            entries.append((_norm(item["title"]), item, platform))

    groups: list[dict] = []
    for norm, item, platform in sorted(entries, key=lambda x: -len(x[0])):
        if not norm:
            continue
        for g in groups:
            same = norm == g["norm"]
            contains = len(norm) >= 6 and (norm in g["norm"] or g["norm"] in norm)
            if same or contains:
                if platform not in g["platforms"]:
                    g["platforms"].append(platform)
                    g["members"].append({"platform": platform, "rank": item["rank"]})
                break
        else:
            groups.append(
                {
                    "norm": norm, "title": item["title"], "url": item["url"],
                    "platforms": [platform],
                    "members": [{"platform": platform, "rank": item["rank"]}],
                    "heat": float(item.get("heat_value") or 0),
                }
            )
    hits = [g for g in groups if len(g["platforms"]) >= min_platforms]
    hits.sort(key=lambda g: (-len(g["platforms"]), -g["heat"]))
    return hits[:top]


def trends(conn, platform: str = "", limit: int = 30) -> list[dict]:
    """JSON 接口用：单平台或全部平台的最新榜单。"""
    limit = max(1, min(int(limit), 50))
    if platform and platform in PLATFORM_META:
        batch = conn.execute(
            "SELECT MAX(batch_id) FROM social_trends WHERE platform = ?", (platform,)
        ).fetchone()[0]
        if batch is None:
            return []
        rows = conn.execute(
            "SELECT * FROM social_trends WHERE platform = ? AND batch_id = ?"
            " ORDER BY rank LIMIT ?",
            (platform, batch, limit),
        ).fetchall()
        return [_decorate(dict(r)) for r in rows]
    out: list[dict] = []
    for board in boards(conn, limit).values():
        out.extend(board["items"][:limit])
    return out


def status(conn) -> list[dict]:
    """各平台最近一次抓取状态，来源是复用的 source_state 表。"""
    states = {
        r["source_id"]: dict(r)
        for r in conn.execute("SELECT * FROM source_state WHERE source_id LIKE 'social:%'")
    }
    out = []
    for key, meta in PLATFORM_META.items():
        st = states.get(f"social:{key}", {})
        out.append(
            {
                "platform": key,
                "name": meta["name"],
                "status": st.get("last_status") or "未运行",
                "count": st.get("last_count") or 0,
                "fail_streak": st.get("fail_streak") or 0,
                "last_run": st.get("last_run"),
            }
        )
    return out
