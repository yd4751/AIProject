"""外部搜索源插件：统一「构造 URL → 服务端代取 → 解析成结果列表」。

每个 provider 都是独立函数，新增一个搜索站只需在 URLS / PARSERS 里加一项
并在 config.yaml 的 search.providers 里挂上。任何一源失败都不影响站内检索。
"""

from __future__ import annotations

import html as html_lib
import re
from urllib.parse import parse_qs, quote, urljoin, urlsplit

import feedparser

URLS = {
    "bing_rss": "https://www.bing.com/search?q={q}&format=rss",
    "sogou_news": "https://news.sogou.com/news?query={q}",
    "baidu_news": "https://www.baidu.com/s?tn=news&wd={q}",
    # 谷歌引擎：两路 Google 资讯 RSS（本地版 / 全球版），参数不同所以结果互补。
    # google_web 仅作向后兼容保留：Google 网页搜索强制 JS，服务端只能拿到空壳。
    "google_news": "https://news.google.com/rss/search?q={q}&hl={hl}&gl={gl}&ceid={ceid}",
    "google_global": "https://news.google.com/rss/search?q={q}&hl={ghl}&gl={ggl}&ceid={gceid}",
    "google_web": "https://www.google.com/search?q={q}&num={num}&hl={hl}",
    # 谷歌引擎的「网页结果」补充源：Google 网页搜索对数据中心 IP 一律 429 + 强制 JS，
    # 服务端取不到；DuckDuckGo 的 HTML 版可以正常代取，结果结构稳定、链接是真实 URL。
    "ddg_web": "https://html.duckduckgo.com/html/?q={q}&kl={kl}",
}
LABELS = {
    "bing_rss": "Bing 全网", "sogou_news": "搜狗资讯", "baidu_news": "百度资讯",
    "google_news": "Google 资讯（本地）", "google_global": "Google 资讯（全球）",
    "google_web": "Google 网页", "ddg_web": "DuckDuckGo 网页",
}
GOOGLE_PROVIDERS = ("google_news", "google_global", "google_web")
WEB_PROVIDERS = ("ddg_web",)
DEFAULT_OPTS = {
    "hl": "zh-CN", "gl": "CN", "ceid": "CN:zh-Hans", "num": 10,
    "ghl": "en-US", "ggl": "US", "gceid": "US:en", "kl": "cn-zh",
}


def search_url(name: str, query: str, opts: dict | None = None) -> str:
    """构造搜索 URL；opts 只影响 Google 的语言/地区/条数参数。"""
    tpl = URLS.get(name)
    if not tpl:
        return ""
    p = {**DEFAULT_OPTS, **(opts or {})}
    try:
        num = max(1, min(int(p["num"]), 50))
    except (TypeError, ValueError):
        num = 10
    return tpl.format(
        q=quote(query, safe=""),
        hl=quote(str(p["hl"]), safe=""),
        gl=quote(str(p["gl"]), safe=""),
        ceid=quote(str(p["ceid"]), safe=":"),
        ghl=quote(str(p["ghl"]), safe=""),
        ggl=quote(str(p["ggl"]), safe=""),
        gceid=quote(str(p["gceid"]), safe=":"),
        num=num,
        kl=quote(str(p.get("kl") or "cn-zh"), safe=""),
    )


def direct_google_urls(query: str, opts: dict | None = None) -> list[dict]:
    """「直接在 Google 中打开」的入口。

    Google 网页搜索需要浏览器执行 JS，服务端代取不到结果；这两条链接让用户
    一键直达真实的 Google 搜索页，与站内展示的资讯结果互补。
    """
    p = {**DEFAULT_OPTS, **(opts or {})}
    q = quote(query or "", safe="")
    return [
        {
            "label": "Google 网页搜索 ↗",
            "url": "https://www.google.com/search?q={}&num={}&hl={}".format(
                q, p["num"], quote(str(p["hl"]), safe="")),
            "hint": "在浏览器中打开完整的 Google 网页搜索结果（需要能访问 Google）",
        },
        {
            "label": "Google 资讯 ↗",
            "url": "https://news.google.com/search?q={}&hl={}&gl={}&ceid={}".format(
                q, quote(str(p["hl"]), safe=""), quote(str(p["gl"]), safe=""),
                quote(str(p["ceid"]), safe=":")),
            "hint": "在浏览器中打开 Google 资讯搜索页",
        },
    ]


def _text(raw: str) -> str:
    raw = re.sub(r"<!--.*?-->", "", raw, flags=re.S)
    raw = re.sub(r"<[^>]+>", "", raw)
    return re.sub(r"\s+", " ", html_lib.unescape(raw)).strip()


def _host(url: str) -> str:
    return (urlsplit(url).hostname or "").replace("www.", "")


def _parse_bing_rss(content: bytes, limit: int) -> list[dict]:
    feed = feedparser.parse(content)
    out = []
    for e in feed.entries[:limit]:
        link = (e.get("link") or "").strip()
        if not link.startswith(("http://", "https://")):
            continue
        out.append({
            "title": _text(e.get("title") or "")[:200],
            "url": link, "summary": _text(e.get("description") or "")[:240],
            "source": _host(link) or "Bing", "date": _text(e.get("published") or "")[:40],
        })
    return out


def _parse_html_results(content: bytes, limit: int, base: str,
                        title_pat: str) -> list[dict]:
    try:
        html = content.decode("utf-8", "replace")
    except Exception:
        return []
    out: list[dict] = []
    for m in re.finditer(title_pat, html, re.S | re.I):
        href, title_html = m.group(1), m.group(2)
        title = _text(title_html)
        if not title or len(title) < 4:
            continue
        url = urljoin(base, html_lib.unescape(href))
        if not url.startswith(("http://", "https://")):
            continue
        tail = html[m.end(): m.end() + 1400]
        sm = re.search(r'<p[^>]*class="[^"]*(?:star-wiki|text|abstract|summary)[^"]*"[^>]*>(.*?)</p>',
                       tail, re.S | re.I)
        summary = _text(sm.group(1))[:240] if sm else ""
        fm = re.search(r'<p[^>]*class="[^"]*(?:news-from|source|c-author)[^"]*"[^>]*>(.*?)</p>',
                       tail, re.S | re.I)
        raw_meta = fm.group(1) if fm else ""
        spans = [_text(x) for x in re.findall(r"<span[^>]*>(.*?)</span>", raw_meta, re.S | re.I)]
        date = ""
        dm = re.search(r"(20\d{2}[年\-/]\d{1,2}[月\-/]\d{1,2}日?)", " ".join(spans) or _text(raw_meta))
        if dm:
            date = dm.group(1)
        source = _host(url)
        if spans and spans[0]:
            source = spans[0][:24]
        elif raw_meta:
            source = _text(raw_meta)[:24] or source
        out.append({"title": title[:200], "url": url, "summary": summary,
                    "source": source, "date": date})
        if len(out) >= limit:
            break
    return out


def _parse_sogou(content: bytes, limit: int) -> list[dict]:
    pat = r'<h3[^>]*class="[^"]*vr-title[^"]*"[^>]*>\s*<a[^>]*href="([^"]+)"[^>]*>(.*?)</a>'
    return _parse_html_results(content, limit, "https://news.sogou.com", pat)


def _parse_baidu(content: bytes, limit: int) -> list[dict]:
    pat = r'<h3[^>]*class="[^"]*c-title[^"]*"[^>]*>\s*<a[^>]*href="([^"]+)"[^>]*>(.*?)</a>'
    return _parse_html_results(content, limit, "https://www.baidu.com", pat)


# ---------------------------------------------------------------- M10 谷歌引擎

_GOOGLE_OWN = ("google.com", "gstatic.com", "googleusercontent.com", "googleapis.com")
_SNIPPET_CLASS = r"VwiC3b|IsZvec|MUxGbd|hgKElc|aCOpMb|st"


def _google_real_url(href: str, base: str) -> str:
    """解出结果真实 URL：Google 常用 /url?q= 包装，另外要滤掉它自己的页面链接。"""
    href = html_lib.unescape(href)
    if href.startswith("/url?"):
        qs = parse_qs(urlsplit(href).query)
        target = (qs.get("q") or qs.get("url") or [""])[0]
        return target if target.startswith(("http://", "https://")) else ""
    url = urljoin(base, href)
    if not url.startswith(("http://", "https://")):
        return ""
    host = (urlsplit(url).hostname or "").lower()
    if any(host == h or host.endswith("." + h) for h in _GOOGLE_OWN):
        return ""
    return url


def _parse_google_web(content: bytes, limit: int) -> list[dict]:
    try:
        markup = content.decode("utf-8", "replace")
    except Exception:
        return []
    out: list[dict] = []
    # 新版结果：<a href="...">…<h3>标题</h3>…</a>，摘要在 a 之后若干字符内
    for m in re.finditer(r'<a\b[^>]*href="([^"]+)"[^>]*>(.*?)</a>', markup, re.S | re.I):
        hm = re.search(r"<h3[^>]*>(.*?)</h3>", m.group(2), re.S | re.I)
        if not hm:
            continue
        title = _text(hm.group(1))
        if len(title) < 4:
            continue
        url = _google_real_url(m.group(1), "https://www.google.com")
        if not url:
            continue
        tail = markup[m.end(): m.end() + 1600]
        sm = re.search(
            r'<(?:div|span)[^>]*class="[^"]*(?:' + _SNIPPET_CLASS + r')[^"]*"[^>]*>(.*?)</(?:div|span)>',
            tail, re.S | re.I,
        )
        summary = _text(sm.group(1))[:240] if sm else ""
        dm = re.search(r"(20\d{2}[年\-/]\d{1,2}[月\-/]\d{1,2}日?)", _text(tail[:600]))
        out.append({
            "title": title[:200], "url": url, "summary": summary,
            "source": _host(url), "date": dm.group(1) if dm else "",
        })
        if len(out) >= limit:
            break
    return out


def _parse_google_news(content: bytes, limit: int) -> list[dict]:
    feed = feedparser.parse(content)
    out: list[dict] = []
    for e in feed.entries[:limit]:
        link = (e.get("link") or "").strip()
        if not link.startswith(("http://", "https://")):
            continue
        title = _text(e.get("title") or "")[:200]
        if not title:
            continue
        src = e.get("source")
        if isinstance(src, dict):
            src = src.get("title") or ""
        elif not isinstance(src, str):
            src = ""
        if not src and " - " in title:
            src = title.rsplit(" - ", 1)[1]
        src = _text(src)[:24]
        # Google News 的标题一律是「标题 - 来源」，来源已单独有字段，标题里去掉
        if src and title.endswith(" - " + src):
            title = title[: -(len(src) + 3)].strip()
        if not title:
            continue
        summary = _text(e.get("description") or "")[:240]
        # Google News 的 description 常是「标题 + 来源」的重复，清掉避免页面上两行一样
        if not summary or summary.startswith(title[:12]):
            summary = ""
        out.append({
            "title": title, "url": link, "summary": summary,
            "source": src or "Google 资讯",
            "date": _text(e.get("published") or "")[:40],
        })
    return out


def _ddg_real_url(href: str) -> str:
    """DuckDuckGo 的结果链接是 `//duckduckgo.com/l/?uddg=<编码后的真实 URL>`，解出来。"""
    href = html_lib.unescape(href or "").strip()
    if not href:
        return ""
    if href.startswith("//"):
        href = "https:" + href
    if "duckduckgo.com/l/" in href:
        qs = parse_qs(urlsplit(href).query)
        target = (qs.get("uddg") or [""])[0]
        return target if target.startswith(("http://", "https://")) else ""
    if href.startswith(("http://", "https://")):
        host = (urlsplit(href).hostname or "").lower()
        if host.endswith("duckduckgo.com"):
            return ""
        return href
    return ""


def _parse_ddg_web(content: bytes, limit: int) -> list[dict]:
    """DuckDuckGo HTML 版：`<a class="result__a" href="...">标题</a>` + `result__snippet`。

    Lite 版（`lite.duckduckgo.com`）的 class 是 `result-link`，一并兼容。
    """
    try:
        markup = content.decode("utf-8", "replace")
    except Exception:
        return []
    out: list[dict] = []
    for m in re.finditer(r"<a\b([^>]*)>(.*?)</a>", markup, re.S | re.I):
        attrs, inner = m.group(1), m.group(2)
        if not re.search(r'class="[^"]*(?:result__a|result-link)[^"]*"', attrs, re.I):
            continue
        hm = re.search(r'href="([^"]+)"', attrs, re.I)
        if not hm:
            continue
        title = _text(inner)
        if len(title) < 4:
            continue
        url = _ddg_real_url(hm.group(1))
        if not url:
            continue
        tail = markup[m.end(): m.end() + 2500]
        sm = re.search(r'<a\b[^>]*class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</a>',
                       tail, re.S | re.I) or \
             re.search(r'<td\b[^>]*class="[^"]*result-snippet[^"]*"[^>]*>(.*?)</td>',
                       tail, re.S | re.I)
        summary = _text(sm.group(1))[:240] if sm else ""
        out.append({
            "title": title[:200], "url": url, "summary": summary,
            "source": _host(url), "date": "",
        })
        if len(out) >= limit:
            break
    return out


PARSERS = {
    "bing_rss": _parse_bing_rss,
    "sogou_news": _parse_sogou,
    "baidu_news": _parse_baidu,
    "google_news": _parse_google_news,
    "google_global": _parse_google_news,
    "google_web": _parse_google_web,
    "ddg_web": _parse_ddg_web,
}


def parse(name: str, content: bytes, limit: int) -> list[dict]:
    fn = PARSERS.get(name)
    if not fn:
        return []
    items = fn(content, limit)
    seen: set[str] = set()
    out = []
    for it in items:
        if it["url"] in seen:
            continue
        seen.add(it["url"])
        it["provider"] = name
        it["provider_label"] = LABELS.get(name, name)
        out.append(it)
    return out
