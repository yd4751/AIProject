"""补充重试：针对首轮 EMPTY/timeout/403 的国内源 + 少数国外源，
用浏览器 UA + 更长超时，并对空响应打印前 200 字节诊断。"""
from __future__ import annotations

import asyncio
import httpx
import feedparser

PROXY = "http://127.0.0.1:7890"
UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

DOMESTIC = [
    ("36kr", "36氪", "https://36kr.com/feed", "zh"),
    ("huxiu", "虎嗅", "https://www.huxiu.com/rss/0.xml", "zh"),
    ("thepaper", "澎湃新闻", "https://www.thepaper.cn/rss_newsList.jsp", "zh"),
    ("zaobao-china", "联合早报 · 中国", "https://www.zaobao.com/rss/realtime/china", "zh"),
    ("pingwest", "品玩", "https://www.pingwest.com/feed", "zh"),
    ("netease-news", "网易 · 新闻", "https://www.163.com/rss/", "zh"),
    ("ifeng-news", "凤凰网 · 资讯", "https://www.ifeng.com/rss/index.xml", "zh"),
    ("guancha", "观察者网", "https://www.guancha.cn/rss/index.xml", "zh"),
    ("cls", "财联社", "https://www.cls.cn/rss", "zh"),
    ("yicai", "第一财经", "https://www.yicai.com/rss/", "zh"),
    ("cctv-news", "央视新闻", "http://news.cctv.com/rss_news.xml", "zh"),
    ("chinadaily-china", "中国日报 · 中国", "https://www.chinadaily.com.cn/rss/china_rss.xml", "zh"),
]

FOREIGN = [
    ("ap-news", "AP News", "https://apnews.com/index.rss", "en"),
    ("nhk", "NHK", "https://www3.nhk.or.jp/news/rss/news.xml", "en"),
    ("nhk2", "NHK(alt)", "https://www.nhk.or.jp/rss/nhknews.xml", "en"),
    ("reuters", "Reuters", "https://www.reuters.com/arc/outboundfeeds/rss/world/", "en"),
]


async def check(client, item, timeout):
    sid, name, feed, lang = item
    try:
        r = await client.get(feed, timeout=timeout)
        if r.status_code != 200:
            return (sid, name, f"HTTP {r.status_code}", 0, "")
        parsed = await asyncio.to_thread(feedparser.parse, r.content)
        n = len(parsed.entries)
        if n == 0:
            head = r.text[:160].replace("\n", " ")
            return (sid, name, f"EMPTY bozo={parsed.bozo}", 0, head)
        return (sid, name, "OK", n, parsed.entries[0].get("title", "")[:40])
    except Exception as e:
        return (sid, name, f"ERR {type(e).__name__}: {str(e)[:40]}", 0, "")


async def main():
    direct = httpx.AsyncClient(headers={"User-Agent": UA}, timeout=20.0, follow_redirects=True)
    proxied = httpx.AsyncClient(proxy=PROXY, headers={"User-Agent": UA}, timeout=20.0, follow_redirects=True)
    sem = asyncio.Semaphore(6)
    print("===== 国内补充重试 =====")
    async def lim(it, cli, to):
        async with sem:
            return await check(cli, it, to)
    rd = await asyncio.gather(*(lim(it, direct, 20) for it in DOMESTIC))
    for sid, name, st, n, extra in sorted(rd, key=lambda x: (x[3] <= 0, x[0])):
        print(f"  {'✅' if n>0 else '❌'} {sid:<18s} {n:>3}  {st:<22s} {extra}")
    print("===== 国外补充重试 =====")
    rf = await asyncio.gather(*(lim(it, proxied, 20) for it in FOREIGN))
    for sid, name, st, n, extra in sorted(rf, key=lambda x: (x[3] <= 0, x[0])):
        print(f"  {'✅' if n>0 else '❌'} {sid:<18s} {n:>3}  {st:<22s} {extra}")
    await direct.aclose(); await proxied.aclose()


if __name__ == "__main__":
    asyncio.run(main())
