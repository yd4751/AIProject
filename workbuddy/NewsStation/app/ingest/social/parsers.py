"""社交媒体热榜解析器。

每个解析器签名统一为 (text: str, limit: int) -> list[dict]，
dict 至少含 title / url / heat_value，可选 summary / extra。
不直接发请求，方便用真实响应样本做离线测试。

关键约定：URL 必须**稳定**。榜单每轮都会返回，但平台自带的跳转链接常带
随机曝光参数（头条的 log_pb 每轮都变），照抄会让同一个话题反复入库。
没有内容页的平台（微博/百度/抖音）一律用热搜词构造搜索链接。
"""

from __future__ import annotations

import html
import json
import re
from urllib.parse import quote

_SDATA_RE = re.compile(r"<!--s-data:(.*?)-->", re.S)
_WS_RE = re.compile(r"\s+")
_TAG_RE = re.compile(r"<[^>]+>")


def _clean(value) -> str:
    s = _TAG_RE.sub(" ", html.unescape(str(value or "")))
    return _WS_RE.sub(" ", s).strip()


def _num(value) -> float:
    try:
        return float(str(value or "").replace(",", "").strip())
    except (TypeError, ValueError):
        return 0.0


def _take(rows: list[dict], limit: int) -> list[dict]:
    """过滤空值、按标题去重，并重新编号 rank（平台原始 index 常为 0 或缺失）。"""
    out: list[dict] = []
    seen: set[str] = set()
    for row in rows:
        title = _clean(row.get("title"))
        url = (row.get("url") or "").strip()
        if not title or not url or title in seen:
            continue
        seen.add(title)
        row["title"] = title
        row["rank"] = len(out) + 1
        row["heat_value"] = _num(row.get("heat_value"))
        row["summary"] = _clean(row.get("summary"))[:500]
        out.append(row)
        if len(out) >= max(1, limit):
            break
    return out


def weibo(text: str, limit: int) -> list[dict]:
    """微博热搜：data.realtime[].word / num。"""
    data = json.loads(text)
    rows = (data.get("data") or {}).get("realtime") or []
    out = []
    for x in rows:
        word = _clean(x.get("word") or x.get("note"))
        if not word:
            continue
        scheme = x.get("word_scheme") or word
        note = _clean(x.get("note"))
        out.append(
            {
                "title": word,
                "url": "https://s.weibo.com/weibo?q=" + quote(scheme, safe=""),
                "heat_value": x.get("num"),
                "summary": "" if note == word else note,
                "extra": {"label": _clean(x.get("label_name"))},
            }
        )
    return _take(out, limit)


def baidu(text: str, limit: int) -> list[dict]:
    """百度热搜：HTML 注释 <!--s-data:{...}--> 里的 data.cards[0].content[]。"""
    m = _SDATA_RE.search(text)
    if not m:
        raise ValueError("百度热榜页面缺少 s-data 数据块")
    data = json.loads(m.group(1))
    cards = (data.get("data") or {}).get("cards") or []
    out = []
    for card in cards[:1]:  # 第一个 hotList 卡片就是实时热搜榜
        for x in card.get("content") or []:
            word = _clean(x.get("word"))
            if not word:
                continue
            out.append(
                {
                    "title": word,
                    "url": "https://www.baidu.com/s?wd=" + quote(word, safe=""),
                    "heat_value": x.get("hotScore"),
                    "summary": x.get("desc"),
                    "extra": {"hot_change": _clean(x.get("hotChange")), "hot_tag": _clean(x.get("hotTag"))},
                }
            )
    return _take(out, limit)


def toutiao(text: str, limit: int) -> list[dict]:
    """今日头条热榜：data[].Title / HotValue / ClusterId。"""
    data = json.loads(text)
    rows = data.get("data") or []
    out = []
    for x in rows:
        title = _clean(x.get("Title"))
        cid = x.get("ClusterId")
        if not title or not cid:
            continue
        # 只用 ClusterId 造链接：原 Url 的 log_pb 含每轮变化的 impr_id
        out.append(
            {
                "title": title,
                "url": f"https://www.toutiao.com/trending/{cid}/",
                "heat_value": x.get("HotValue"),
                "summary": "",
                "extra": {"label": _clean(x.get("Label"))},
            }
        )
    return _take(out, limit)


def douyin(text: str, limit: int) -> list[dict]:
    """抖音热点榜：word_list[].word / hot_value。"""
    data = json.loads(text)
    rows = data.get("word_list") or []
    out = []
    for x in rows:
        word = _clean(x.get("word"))
        if not word:
            continue
        out.append(
            {
                "title": word,
                "url": "https://www.douyin.com/search/" + quote(word, safe=""),
                "heat_value": x.get("hot_value"),
                "summary": "",
                "extra": {"label": _clean(x.get("label"))},
            }
        )
    return _take(out, limit)


def bilibili(text: str, limit: int) -> list[dict]:
    """B 站热门视频：data.list[].title / bvid / stat.view。"""
    data = json.loads(text)
    if data.get("code") not in (0, None):
        raise ValueError(f"B 站接口返回 code={data.get('code')}: {data.get('message')}")
    rows = (data.get("data") or {}).get("list") or []
    out = []
    for x in rows:
        title = _clean(x.get("title"))
        bvid = _clean(x.get("bvid"))
        if not title or not bvid:
            continue
        out.append(
            {
                "title": title,
                "url": f"https://www.bilibili.com/video/{bvid}",
                "heat_value": (x.get("stat") or {}).get("view"),
                "summary": x.get("desc"),
                "extra": {
                    "category": _clean(x.get("tname")),
                    "author": _clean((x.get("owner") or {}).get("name")),
                },
            }
        )
    return _take(out, limit)


def hackernews(text: str, limit: int) -> list[dict]:
    """Hacker News 首页热帖（Algolia 官方 API，免鉴权）。

    hits[].title / url（外链，Ask HN 时为 null）/ points / num_comments / objectID。
    无外链时回退到 HN 讨论页，保证 URL 稳定、不重复入库。
    """
    data = json.loads(text)
    hits = data.get("hits") or []
    out = []
    for x in hits:
        title = _clean(x.get("title") or x.get("story_title"))
        oid = x.get("objectID")
        if not title or not oid:
            continue
        url = x.get("url") or f"https://news.ycombinator.com/item?id={oid}"
        pts = x.get("points")
        cm = x.get("num_comments") or 0
        out.append(
            {
                "title": title,
                "url": url,
                "heat_value": pts,
                "summary": f"{cm} 评论 · {pts or 0} 赞",
                "extra": {"comments": cm},
            }
        )
    return _take(out, limit)


def lobsters(text: str, limit: int) -> list[dict]:
    """Lobsters 热门（hottest.json，免鉴权，社区技术新闻）。

    数组元素：title / url（外链，可能为 null）/ score / comment_count / short_id / tags。
    无外链时回退到社区讨论页。
    """
    data = json.loads(text)
    if not isinstance(data, list):
        raise ValueError("Lobsters 返回结构异常，应为数组")
    out = []
    for x in data:
        title = _clean(x.get("title"))
        sid = x.get("short_id")
        if not title or not sid:
            continue
        url = x.get("url") or f"https://lobste.rs/s/{sid}"
        score = x.get("score")
        cm = x.get("comment_count") or 0
        out.append(
            {
                "title": title,
                "url": url,
                "heat_value": score,
                "summary": f"{cm} 评论 · {score or 0} 赞",
                "extra": {"tags": x.get("tags") or []},
            }
        )
    return _take(out, limit)


def github(text: str, limit: int) -> list[dict]:
    """GitHub Trending 仓库（HTML 解析）。

    按 <article class="Box-row"> 切块；仓库链接只从 <h2> 内取，避开块首的
    「登录星标」与顶部赞助位；热度优先用「当日 star 数」(stars today)，
    缺失时回退总 star 数。
    """
    blocks = text.split('<article class="Box-row">')
    out = []
    for blk in blocks[1:]:
        h2 = re.search(r"<h2[^>]*>(.*?)</h2>", blk, re.S)
        if not h2:
            continue
        repo_m = re.search(r'href="/([^"]+)"', h2.group(1))
        if not repo_m:
            continue
        repo = repo_m.group(1).strip("/")
        if repo.count("/") != 1 or repo.split("/")[0] in ("login", "sponsors", "join"):
            continue
        dm = re.search(r'<p[^>]*class="col-9[^"]*"[^>]*>(.*?)</p>', blk, re.S)
        desc = _clean(dm.group(1)) if dm else ""
        lm = re.search(r'itemprop="programmingLanguage">([^<]+)<', blk)
        lang = _clean(lm.group(1)) if lm else ""
        today_m = re.search(r"([\d,]+)\s*stars today", blk)
        today = _num(today_m.group(1)) if today_m else 0.0
        total_m = re.search(r'href="/[^"]*/stargazers"[^>]*>(?:<[^>]*>)*\s*([\d,]+)', blk)
        total = _num(total_m.group(1)) if total_m else 0.0
        out.append(
            {
                "title": repo,
                "url": "https://github.com/" + repo,
                "heat_value": today or total,
                "summary": desc,
                "extra": {"language": lang, "stars_today": today, "stars_total": total},
            }
        )
    return _take(out, limit)


PARSERS = {
    "weibo": weibo,
    "baidu": baidu,
    "toutiao": toutiao,
    "douyin": douyin,
    "bilibili": bilibili,
    "hackernews": hackernews,
    "lobsters": lobsters,
    "github": github,
}
