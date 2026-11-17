"""订阅通知：抓取/分析完成后的「匹配 → 摘要 → 发信」链路。

run_once 只调用 after_fetch 一次；站内命中始终记录，邮件仅在 smtp.pass 配置后发送。
"""

from __future__ import annotations

from .mailer import MAX_PER_SUB, build_digest, send_digest
from .matcher import describe, match_new, parse_tag_filter, split_keywords, unread_total

__all__ = [
    "after_fetch", "describe", "parse_tag_filter", "split_keywords", "unread_total",
    "match_new", "build_digest", "send_digest", "MAX_PER_SUB",
]


def after_fetch(db_path, settings, min_article_id: int) -> dict:
    stats = match_new(db_path, min_article_id)
    digest = build_digest(db_path, stats["hit_ids"]) if stats["hit_ids"] else None
    stats["digest_subject"] = digest["subject"] if digest else ""
    stats["digest_count"] = digest["count"] if digest else 0
    stats["smtp_enabled"] = settings.smtp.enabled
    stats["mail_sent"] = bool(
        digest and settings.smtp.enabled and send_digest(settings, digest)
    )
    return stats
