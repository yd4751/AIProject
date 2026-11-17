"""热度公式。页面只读不算，避免每次打开都因时间漂移导致排序跳动。"""

from __future__ import annotations

import math

HOUR = 3600


def compute(times: list[int], sources: set[str], now: int, half_life_hours: float = 6.0,
            window_hours: int = 24) -> dict:
    """times：事件内所有文章的发布时间戳（缺失时用 fetched_at）。

    计数窗口取 24h 而不是 6h：半衰期也是 6h，若基数只看 6h，隔夜事件会瞬间归零，
    早上的首页会一片 0 热度。改为「24h 体量 × 指数衰减」，衰减项照样把旧事件压下去。
    """
    if not times:
        return {"heat": 0.0, "momentum": 0.0, "recent": 0, "source_count": len(sources)}

    recent = sum(1 for t in times if now - t <= window_hours * HOUR)
    last3h = sum(1 for t in times if now - t <= 3 * HOUR)
    prev3h = sum(1 for t in times if now - t <= 6 * HOUR) - last3h
    # 用「后 3h 占近 6h 的比例」算增速，天然落在 [-1, 1]：
    # 原式 (last-prev)/max(prev,1) 在 prev=0 时直接饱和成 1，单篇新稿也能吃满加成。
    if last3h + prev3h >= 3:
        momentum = (last3h - prev3h) / (last3h + prev3h)
    else:
        momentum = 0.0  # 样本太少，不谈增速

    base = math.log(1 + recent) * (1 + 0.3 * (len(sources) - 1)) * (1 + 0.5 * momentum)
    hours_since = max(0.0, (now - max(times)) / HOUR)
    heat = base * math.exp(-hours_since / half_life_hours)
    return {
        "heat": round(heat, 4),
        "momentum": round(momentum, 3),
        "recent": recent,
        "source_count": len(sources),
    }
