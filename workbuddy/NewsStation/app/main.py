"""FastAPI 入口：初始化 DB/源、启动定时抓取、挂载页面。"""

from __future__ import annotations

import asyncio
from contextlib import asynccontextmanager

from apscheduler.schedulers.asyncio import AsyncIOScheduler
from fastapi import FastAPI
from fastapi.staticfiles import StaticFiles

from app import config, db, runtime
from app.analyze import pipeline
from app.ingest import registry, social
from app.ingest.fetch import run_once
from app.web.routes import router


@asynccontextmanager
async def lifespan(app: FastAPI):
    settings = config.load()
    db_path = config.abs_db_path(settings)
    db.init_db(db_path)
    registry.load_sources_yaml()
    social.load_social_yaml()
    app.state.settings = settings

    # 启动时把上次没分析完的文章补齐（进程被 Ctrl-C 打断的常见场景）
    await asyncio.to_thread(pipeline.process, db_path, settings)

    scheduler: AsyncIOScheduler | None = None
    minutes = settings.fetch.interval_minutes
    if minutes > 0:
        scheduler = AsyncIOScheduler(timezone="Asia/Shanghai")
        scheduler.add_job(run_once, "interval", minutes=minutes, args=[settings], id="fetch", max_instances=1)
        scheduler.start()

    app.state.scheduler = scheduler
    try:
        yield
    finally:
        if scheduler is not None:
            scheduler.shutdown(wait=False)


app = FastAPI(title="NewsStation", lifespan=lifespan)


@app.middleware("http")
async def no_cache_html(request, call_next):
    """页面 HTML 一律不缓存：本地改版后刷新就能看到，不会命中浏览器里的旧页面。

    静态资源仍走 StaticFiles 的 ETag/Last-Modified，靠 app.css/app.js 的 ?v= 版本号破缓存。
    """
    response = await call_next(request)
    if response.headers.get("content-type", "").startswith("text/html"):
        response.headers["Cache-Control"] = "no-store"
    return response


app.mount("/static", StaticFiles(directory=str(runtime.static_dir())), name="static")
app.include_router(router)
