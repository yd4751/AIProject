"""一键翻译：可插拔后端 + SQLite 缓存 + 语言检测降级。

后端只负责「把一批文本翻成目标语言」；语言判断、缓存、失败降级都在 service。
"""
from __future__ import annotations

from app.translate.backends import BACKENDS, chain_of, get_backend
from app.translate.service import (
    load_cache,
    needs_translation,
    status,
    text_hash,
    translate_texts,
)

__all__ = [
    "BACKENDS",
    "chain_of",
    "get_backend",
    "load_cache",
    "needs_translation",
    "status",
    "text_hash",
    "translate_texts",
]
