"""临时脚本：批量实测候选 RSS 源连通性（国内直连 / 国外代理）。
用法：python scripts/check_feeds.py
输出每个候选的 HTTP 状态、feedparser 解析条目数、bozo 警告。只收录 OK 且有条目的源。
"""
from __future__ import annotations

import asyncio
import time

import httpx
import feedparser

PROXY = "http://127.0.0.1:7890"
UA = "NewsStation/0.1 (+personal news aggregator)"

# (id, name, feed, lang) —— 国内直连候选池
DOMESTIC = [
    ("xinhua-politics", "新华网 · 时政", "http://www.xinhuanet.com/politics/news_politics.xml", "zh"),
    ("people-politics", "人民网 · 时政", "http://www.people.com.cn/rss/politics.xml", "zh"),
    ("people-china", "人民网 · 国内", "http://www.people.com.cn/rss/china.xml", "zh"),
    ("chinanews-china", "中新网 · 国内", "http://www.chinanews.com.cn/rss/scroll-news.xml", "zh"),
    ("sina-china", "新浪 · 国内", "https://rss.sina.com.cn/news/china/focus15.xml", "zh"),
    ("sina-tech", "新浪 · 科技", "https://rss.sina.com.cn/tech/rollnews.xml", "zh"),
    ("sina-finance", "新浪 · 财经", "https://finance.sina.com.cn/stock/roll.php?lid=cjxh&xwx=1", "zh"),
    ("netease-news", "网易 · 新闻", "https://www.163.com/rss/", "zh"),
    ("ifeng-news", "凤凰网 · 资讯", "https://www.ifeng.com/rss/index.xml", "zh"),
    ("thepaper", "澎湃新闻", "https://www.thepaper.cn/rss_newsList.jsp", "zh"),
    ("36kr", "36氪", "https://36kr.com/feed", "zh"),
    ("huxiu", "虎嗅", "https://www.huxiu.com/rss/0.xml", "zh"),
    ("guancha", "观察者网", "https://www.guancha.cn/rss/index.xml", "zh"),
    ("pingwest", "品玩", "https://www.pingwest.com/feed", "zh"),
    ("geekpark", "极客公园", "https://www.geekpark.net/rss", "zh"),
    ("leiphone", "雷锋网", "https://www.leiphone.com/rss", "zh"),
    ("cls", "财联社", "https://www.cls.cn/rss", "zh"),
    ("yicai", "第一财经", "https://www.yicai.com/rss/", "zh"),
    ("zaobao-china", "联合早报 · 中国", "https://www.zaobao.com/rss/realtime/china", "zh"),
    ("chinadaily-china", "中国日报 · 中国", "https://www.chinadaily.com.cn/rss/china_rss.xml", "zh"),
    ("cctv-news", "央视新闻", "http://news.cctv.com/rss_news.xml", "zh"),
    ("eastmoney", "东方财富", "https://finance.eastmoney.com/rss/", "zh"),
]

# (id, name, feed, lang) —— 国外代理候选池
FOREIGN = [
    ("ap-news", "AP News", "https://apnews.com/index.rss", "en"),
    ("wapo-world", "Washington Post · World", "https://feeds.washingtonpost.com/rss/world", "en"),
    ("reuters", "Reuters", "https://www.reuters.com/arc/outboundfeeds/rss/top-news/", "en"),
    ("economist", "The Economist", "https://www.economist.com/finance-and-economics/rss.xml", "en"),
    ("aljazeera", "Al Jazeera", "https://www.aljazeera.com/xml/rss/all.xml", "en"),
    ("france24", "France 24", "https://www.france24.com/en/rss", "en"),
    ("dw", "DW", "https://rss.dw.com/rdf/rss-en-all", "en"),
    ("nhk", "NHK", "https://www3.nhk.or.jp/rss/news.xml", "en"),
    ("verge", "The Verge", "https://www.theverge.com/rss/index.xml", "en"),
    ("techcrunch", "TechCrunch", "https://techcrunch.com/feed/", "en"),
    ("arstechnica", "Ars Technica", "https://feeds.arstechnica.com/arstechnica/index", "en"),
    ("wired", "Wired", "https://www.wired.com/feed/rss", "en"),
    ("npr", "NPR", "https://feeds.npr.org/1001/rss.xml", "en"),
    ("bbc-business", "BBC · Business", "https://feeds.bbci.co.uk/news/business/rss.xml", "en"),
    ("bbc-tech", "BBC · Technology", "https://feeds.bbci.co.uk/news/technology/rss.xml", "en"),
    ("bbc-science", "BBC · Science", "https://feeds.bbci.co.uk/news/science_and_environment/rss.xml", "en"),
    ("hn-frontpage", "Hacker News", "https://hnrss.org/frontpage", "en"),
    ("bloomberg", "Bloomberg", "https://feeds.bloomberg.com/markets/news.rss", "en"),
    ("sciam", "Scientific American", "https://www.scientificamerican.com/feed/", "en"),
    ("nature", "Nature", "https://www.nature.com/nature.rss", "en"),
    ("science", "Science", "https://www.science.org/rss/news_current.xml", "en"),
]


async def check(client: httpx.AsyncClient, item):
    sid, name, feed, lang = item
    try:
        r = await client.get(feed)
        if r.status_code != 200:
            return (sid, name, f"HTTP {r.status_code}", 0, "")
        parsed = await asyncio.to_thread(feedparser.parse, r.content)
        n = len(parsed.entries)
        bozo = parsed.bozo
        # 取一条标题验证可解析出文本
        sample = parsed.entries[0].get("title", "") if n else ""
        return (sid, name, f"{'OK' if n else 'EMPTY'} bozo={bozo}", n, sample)
    except Exception as e:
        return (sid, name, f"ERR {type(e).__name__}: {str(e)[:50]}", 0, "")


async def run_group(label, items, client, sem):
    print(f"\n===== {label} ({len(items)} 个候选) =====")
    out = []

    async def limited(it):
        async with sem:
            await asyncio.sleep(0.15)
            return await check(client, it)

    res = await asyncio.gather(*(limited(it) for it in items))
    for sid, name, status, n, sample in sorted(res, key=lambda x: (x[3] <= 0, x[0])):
        mark = "✅" if n > 0 and status.startswith("OK") else "❌"
        out.append((sid, name, n, status))
        print(f"  {mark} {sid:<20s} {n:>3} 条  {status:<28s} {sample[:40]}")
    ok = [o for o in out if o[2] > 0]
    print(f"  >> 通过 {len(ok)} / {len(items)}")
    return ok


async def main():
    t0 = time.time()
    direct = httpx.AsyncClient(headers={"User-Agent": UA}, timeout=12.0, follow_redirects=True)
    proxied = httpx.AsyncClient(proxy=PROXY, headers={"User-Agent": UA}, timeout=18.0, follow_redirects=True)
    sem_d = asyncio.Semaphore(8)
    sem_f = asyncio.Semaphore(4)
    try:
        dom_ok = await run_group("国内直连", DOMESTIC, direct, sem_d)
        for_nok = await run_group("国外代理", FOREIGN, proxied, sem_f)
    finally:
        await direct.aclose()
        await proxied.aclose()

    print("\n===== 汇总（可直接加入 sources.yaml 的 id 列表）=====")
    print("国内通过:", ", ".join(o[0] for o in dom_ok))
    print("国外通过:", ", ".join(o[0] for o in for_nok))
    print(f"耗时 {time.time()-t0:.1f}s")


if __name__ == "__main__":
    asyncio.run(main())
