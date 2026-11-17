"""抓取编排：并发拉取所有源 -> 归一化 URL -> 去重入库 -> 记录源状态。

用法：
    python -m app.ingest.fetch                 # 抓全部
    python -m app.ingest.fetch --source bbc-world
"""

from __future__ import annotations

import argparse
import asyncio
import time
from datetime import datetime, timezone
from urllib.parse import parse_qsl, urlencode, urlsplit, urlunsplit

import httpx

from app import config, db
from app.ingest import registry, social
from app.ingest.registry import Article
from app.ingest.social.store import save_social_trends

_TRACKING_PREFIXES = ("utm_", "at_campaign", "at_medium", "at_custom", "spm", "from", "ref", "share_")


def normalize_url(url: str) -> str:
    """去掉 fragment 与跟踪参数，减少同一篇文章的重复入库。"""
    parts = urlsplit(url.strip())
    query = [
        (k, v)
        for k, v in parse_qsl(parts.query, keep_blank_values=True)
        if not k.lower().startswith(_TRACKING_PREFIXES)
    ]
    query.sort()
    return urlunsplit((parts.scheme.lower(), parts.netloc.lower(), parts.path, urlencode(query), ""))


def _article_count(conn) -> int:
    return conn.execute("SELECT COUNT(*) FROM articles").fetchone()[0]


def save_articles(db_path, articles: list[Article], max_age_days: int = 7) -> int:
    if not articles:
        return 0
    now = int(time.time())
    cutoff = now - max_age_days * 86400
    rows = []
    skipped_stale = 0
    for a in articles:
        url = normalize_url(a.url)
        if not url or not a.title:
            continue
        published = int(a.published_at.timestamp()) if a.published_at else None
        if published and published < cutoff:
            skipped_stale += 1
            continue
        rows.append((a.source_id, url, a.title[:500], a.summary, a.lang, published, now))
    if not rows:
        return 0
    conn = db.connect(db_path)
    try:
        before = _article_count(conn)
        conn.executemany(
            "INSERT OR IGNORE INTO articles (source_id, url, title, summary, lang, published_at, fetched_at)"
            " VALUES (?, ?, ?, ?, ?, ?, ?)",
            rows,
        )
        conn.commit()
        return _article_count(conn) - before
    finally:
        conn.close()


def record_state(db_path, source_id: str, status: str, count: int, ok: bool) -> None:
    conn = db.connect(db_path)
    try:
        conn.execute(
            "INSERT INTO source_state (source_id, enabled, last_run, last_status, fail_streak, last_count)"
            " VALUES (?, 1, ?, ?, ?, ?)"
            " ON CONFLICT(source_id) DO UPDATE SET"
            "   last_run=excluded.last_run,"
            "   last_status=excluded.last_status,"
            "   last_count=excluded.last_count,"
            "   fail_streak=CASE WHEN ? THEN 0 ELSE fail_streak + 1 END",
            # 首次插入也要带上正确的 fail_streak：ON CONFLICT 分支此时不会执行
            (source_id, int(time.time()), status[:200], 0 if ok else 1, count, 1 if ok else 0),
        )
        conn.commit()
    finally:
        conn.close()


async def run_once(settings: config.Settings, only: list[str] | None = None) -> list[dict]:
    db_path = config.abs_db_path(settings)
    db.init_db(db_path)

    # 本轮开始前的水位：抓取结束后只把 id 大于它的文章交给订阅匹配，
    # 避免把库里已有的旧文章重新算成新命中。
    conn0 = db.connect(db_path)
    try:
        min_article_id = conn0.execute(
            "SELECT COALESCE(MAX(id), 0) FROM articles"
        ).fetchone()[0]
    finally:
        conn0.close()

    # RSS 源来自 sources.yaml；社媒源由 lifespan / CLI 显式装载，
    # 脚本和测试不装载就不会触发额外网络请求，保持原有行为。
    sources: list = list(registry.all_sources())
    if settings.social.enabled:
        sources += list(social.all_sources())
    if only:
        sources = [s for s in sources if s.id in only]

    ua = settings.fetch.user_agent
    timeout = settings.fetch.timeout_seconds
    limits = httpx.Limits(max_connections=max(4, settings.fetch.concurrency * 2))

    common = dict(headers={"User-Agent": ua}, timeout=timeout, follow_redirects=True, limits=limits)
    direct = httpx.AsyncClient(**common)
    proxied = httpx.AsyncClient(proxy=settings.proxy.url, **common) if settings.proxy.url else None

    sem = asyncio.Semaphore(settings.fetch.concurrency)
    delay = settings.fetch.per_source_delay_ms / 1000
    batch_id = int(time.time())   # 一轮内所有平台共用一个榜单批次
    results: list[dict] = []

    async def run_one(source) -> None:
        if source.proxy_required and proxied is None:
            record_state(db_path, source.id, "skipped: no proxy", 0, ok=True)
            results.append({"id": source.id, "status": "skip", "fetched": 0, "new": 0, "note": "无代理"})
            return
        client = proxied if (source.proxy_required and settings.proxy.for_foreign and proxied) else direct
        async with sem:
            await asyncio.sleep(delay)
            is_social = bool(getattr(source, "platform", ""))
            kind = "social" if is_social else ("html" if getattr(source, "parser", "") else "rss")
            try:
                articles = await source.fetch(client)
                if is_social:
                    # 榜单走快照表；另按 inject_top 注入主事件流，让热点自动获得
                    # 四维标签、聚类、订阅命中与一键翻译。URL 稳定，重复轮次不会重复入库。
                    save_social_trends(db_path, articles, batch_id, settings.social.retention_days)
                    injected = 0
                    if settings.social.inject_articles:
                        inj_top = getattr(source, "inject_top", None) or settings.social.inject_top
                        top = [a for a in articles if a.rank <= max(0, inj_top)]
                        injected = save_articles(db_path, top, settings.fetch.max_age_days)
                    count = len(articles)
                    note = f"榜单 {count} 条 · 注入 {injected} 条"
                else:
                    injected = save_articles(db_path, articles, settings.fetch.max_age_days)
                    count = injected
                    note = ""
                record_state(db_path, source.id, "ok", count, ok=True)
                results.append(
                    {
                        "id": source.id, "status": "ok",
                        "kind": kind,
                        "fetched": len(articles), "new": injected, "note": note,
                    }
                )
            except Exception as exc:  # 单源失败不影响其它源
                msg = f"{type(exc).__name__}: {exc}"
                record_state(db_path, source.id, f"error: {msg}", 0, ok=False)
                results.append(
                    {
                        "id": source.id, "status": "fail",
                        "kind": kind,
                        "fetched": 0, "new": 0, "note": msg[:60],
                    }
                )

    try:
        await asyncio.gather(*(run_one(s) for s in sources))
    finally:
        await direct.aclose()
        if proxied is not None:
            await proxied.aclose()

    # 抓完立刻分析，管道是纯 CPU 活，丢线程池里不阻塞事件循环
    from app.analyze import pipeline

    if not only:
        LAST_ANALYSIS.clear()
        LAST_ANALYSIS.update(
            await asyncio.to_thread(pipeline.process, db_path, settings)
        )
        # 分析完再匹配：标签订阅依赖 event_tags，事件归属此时才确定
        from app.notify import after_fetch

        LAST_ANALYSIS["notify"] = await asyncio.to_thread(
            after_fetch, db_path, settings, min_article_id
        )

    return sorted(results, key=lambda r: r["id"])


LAST_ANALYSIS: dict = {}


def main() -> None:
    parser = argparse.ArgumentParser(description="NewsStation 抓取器")
    parser.add_argument("--source", action="append", help="只抓指定源，可重复")
    parser.add_argument("--config", default=None)
    args = parser.parse_args()

    settings = config.load(args.config)
    loaded = registry.load_sources_yaml()
    if not loaded:
        raise SystemExit("sources.yaml 未加载到任何源")
    social.load_social_yaml()

    started = datetime.now(timezone.utc)
    results = asyncio.run(run_once(settings, only=args.source))
    elapsed = (datetime.now(timezone.utc) - started).total_seconds()

    total_new = sum(r["new"] for r in results)
    total_fetched = sum(r["fetched"] for r in results)
    ok = sum(1 for r in results if r["status"] == "ok")
    fail = sum(1 for r in results if r["status"] == "fail")
    skip = sum(1 for r in results if r["status"] == "skip")

    print(f"{'源':<16}{'状态':<8}{'抓到':>6}{'新增':>6}  备注")
    print("-" * 62)
    for r in results:
        print(f"{r['id']:<16}{r['status']:<8}{r['fetched']:>6}{r['new']:>6}  {r['note']}")
    print("-" * 62)
    print(
        f"共 {len(results)} 源：ok={ok} fail={fail} skip={skip} | "
        f"抓到 {total_fetched} 条，新增 {total_new} 条 | 耗时 {elapsed:.1f}s"
    )
    if LAST_ANALYSIS:
        a = LAST_ANALYSIS
        print(
            f"分析：{a['articles']} 篇 → 新事件 {a['events_new']}、并入 {a['events_updated']}，"
            f"标签 {a['tags']} 条，清理过期 {a['purged']} 篇，耗时 {a['seconds']}s"
        )
        n = a.get("notify")
        if n and (n["new_hits"] or n["mail_sent"]):
            print(
                f"订阅：新文章 {n['new_articles']} 篇 → 命中 {n['new_hits']} 条"
                f"（{n['subscriptions_hit']} 个订阅）· "
                f"邮件{'已发送' if n['mail_sent'] else '未配置，仅站内'}"
                + (f" · {n['digest_subject']}" if n["digest_subject"] else "")
            )


if __name__ == "__main__":
    main()
