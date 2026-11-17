"""翻译服务：语言检测 + SQLite 缓存 + 批量调度 + 失败降级。

设计要点：
- 中文（或已是目标语言）的文本不送后端，页面保持原文；
- 译文按 (文本 hash, 目标语言) 缓存，刷新页面/换视图都不重复请求；
- 后端未配置或单条失败都不抛异常，只让该条保持原文；
- 外部端点失败写 1 小时冷却，避免反复打一个不通的接口。
"""
from __future__ import annotations

import hashlib
import re
import time
from pathlib import Path

from app import config, db
from app.translate.backends import chain_of, get_backend

# 中日韩表意文字 + 日文假名 + 韩文音节
CJK = re.compile(r"[\u3400-\u4dbf\u4e00-\u9fff\u3040-\u30ff\uac00-\ud7af]")
LATIN = re.compile(r"[A-Za-z]")
RETRY_AFTER = 3600        # 失败结果 1 小时内不重试
MAX_TEXT = 1000           # 单条截断，避免公开端点长度限制
MAX_ITEMS = 200           # 单次请求上限


def text_hash(text: str, target: str) -> str:
    return hashlib.sha1(f"{target}\n{text}".encode("utf-8")).hexdigest()


def needs_translation(text: str | None, target: str = "zh-CN") -> bool:
    """只翻明显不是目标语言的文本；中英混排的中文标题原样保留。"""
    if not text:
        return False
    s = text.strip()
    if len(s) < 3:
        return False
    letters = len(LATIN.findall(s))
    cjk = len(CJK.findall(s))
    if letters + cjk == 0:          # 纯数字 / 标点 / URL
        return False
    ratio = cjk / (letters + cjk)
    if target.lower().startswith("zh"):
        return ratio < 0.2 and letters >= 2
    if target.lower().startswith("en"):
        return ratio > 0.2
    return ratio < 0.2


def load_cache(conn, target: str | None = None) -> dict[str, str]:
    """一次读回全部成功译文，供模板注入 data-zh，避免逐条查库（N+1）。"""
    if target:
        rows = conn.execute(
            "SELECT source_hash, translated FROM translations"
            " WHERE status='ok' AND target_lang=?",
            (target,),
        )
    else:
        rows = conn.execute(
            "SELECT source_hash, translated FROM translations WHERE status='ok'"
        )
    return {r["source_hash"]: r["translated"] for r in rows if r["translated"]}


def _cached(conn, texts: list[str], target: str) -> dict[str, dict]:
    """命中：ok 直接返回译文；1h 内的 failed 返回 None（不再请求外部端点）。"""
    hashes = {text_hash(t, target): t for t in texts}
    if not hashes:
        return {}
    ph = ",".join("?" * len(hashes))
    rows = conn.execute(
        "SELECT source_hash, translated, status, created_at FROM translations"
        f" WHERE target_lang=? AND source_hash IN ({ph})",
        (target, *hashes.keys()),
    )
    now = time.time()
    out: dict[str, dict] = {}
    for r in rows:
        text = hashes[r["source_hash"]]
        if r["status"] == "ok" and r["translated"]:
            out[text] = {"translated": r["translated"], "status": "ok"}
        elif r["status"] == "failed" and now - r["created_at"] < RETRY_AFTER:
            out[text] = {"translated": None, "status": "failed"}
    return out


def _write(conn, items: list[tuple[str, str | None]], target: str, backend: str) -> None:
    now = int(time.time())
    conn.executemany(
        "INSERT INTO translations"
        " (source_hash, source_text, target_lang, backend, translated, status, created_at)"
        " VALUES (?,?,?,?,?,?,?)"
        " ON CONFLICT(source_hash, target_lang) DO UPDATE SET"
        " translated=excluded.translated, status=excluded.status,"
        " backend=excluded.backend, created_at=excluded.created_at",
        [
            (text_hash(t, target), t, target, backend, zh, "ok" if zh else "failed", now)
            for t, zh in items
        ],
    )
    conn.commit()


def translate_texts(
    db_path: str | Path,
    settings: config.Settings,
    texts: list[str],
    target: str | None = None,
    force: bool = False,
) -> dict:
    """翻译一批文本，返回 {原文: 译文}（只含成功项）与统计。

    force=True 忽略已有缓存重新翻译（清除失败冷却用）。
    """
    target = (target or settings.translate.target or "zh-CN").strip()
    backend = get_backend(settings)

    uniq: list[str] = []
    seen: set[str] = set()
    for t in texts or []:
        s = str(t).strip()[:MAX_TEXT]
        if s and s not in seen:
            seen.add(s)
            uniq.append(s)
    uniq = uniq[:MAX_ITEMS]

    conn = db.connect(db_path)
    try:
        cache = {} if force else _cached(conn, uniq, target)
        todo = [t for t in uniq if t not in cache and needs_translation(t, target)]
        if force:
            todo = [t for t in uniq if needs_translation(t, target)]

        newly_ok = 0
        newly_failed = 0
        wrote: list[tuple[str, str | None]] = []

        if todo and backend.name != "none":
            size = max(1, settings.translate.batch_size)
            for i in range(0, len(todo), size):
                batch = todo[i:i + size]
                results = backend.translate_batch(batch, target, settings)
                for t, zh in zip(batch, results):
                    zh = str(zh).strip() if zh else ""
                    if zh and zh != t:
                        cache[t] = {"translated": zh, "status": "ok"}
                        newly_ok += 1
                    else:
                        cache[t] = {"translated": None, "status": "failed"}
                        newly_failed += 1
                    wrote.append((t, cache[t]["translated"]))
            if wrote:
                _write(conn, wrote, target, backend.name)
        elif todo and backend.name == "none":
            # 后端没配好：不写失败缓存，用户填好配置后立刻可用
            for t in todo:
                cache[t] = {"translated": None, "status": "failed"}
            newly_failed = len(todo)
    finally:
        conn.close()

    ok_count = sum(1 for t in uniq if t in cache and cache[t]["status"] == "ok")
    return {
        "results": {
            t: cache[t]["translated"]
            for t in uniq
            if t in cache and cache[t]["status"] == "ok"
        },
        "backend": backend.name,
        "enabled": backend.name != "none",
        "target": target,
        "stats": {
            "requested": len(uniq),
            "skipped": sum(1 for t in uniq if not needs_translation(t, target)),
            "cached": ok_count - newly_ok,
            "translated": newly_ok,
            "failed": newly_failed,
        },
    }


def status(db_path: str | Path, settings: config.Settings) -> dict:
    """给页面和运维看的状态：后端、代理、缓存量。"""
    backend = get_backend(settings)
    conn = db.connect(db_path)
    try:
        counts = {
            r["status"]: r["n"]
            for r in conn.execute(
                "SELECT status, COUNT(*) n FROM translations GROUP BY status"
            )
        }
    finally:
        conn.close()
    return {
        "backend": backend.name,
        "configured": (settings.translate.backend or "").lower(),
        "chain": chain_of(settings),
        "enabled": backend.name != "none",
        "target": settings.translate.target,
        "proxy": bool(settings.translate.use_proxy and settings.proxy.url),
        "cached_ok": counts.get("ok", 0),
        "cached_failed": counts.get("failed", 0),
    }
