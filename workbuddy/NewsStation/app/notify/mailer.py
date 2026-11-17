"""SMTP 摘要邮件：一轮抓取的全部新命中合成一封，每订阅最多 5 条、按热度排序。

smtp.pass 为空时 enabled=False，链路自动降级为「仅站内」，不报错不发信。
只用标准库 email/smtplib，不引入新依赖。
"""

from __future__ import annotations

import smtplib
from collections import defaultdict
from datetime import datetime
from email.message import EmailMessage

from app import db

from .matcher import describe

MAX_PER_SUB = 5


def build_digest(db_path, hit_ids: list[int]) -> dict | None:
    """把本轮新增的命中按订阅分组，生成一封邮件的主题与纯文本正文。"""
    if not hit_ids:
        return None
    conn = db.connect(db_path)
    try:
        ph = ",".join("?" * len(hit_ids))
        rows = [
            dict(r)
            for r in conn.execute(
                f"SELECT h.id, h.subscription_id, h.title, h.url, h.heat, h.matched_at,"
                f" s.keyword, s.tag_filter FROM subscription_hits h"
                f" JOIN subscriptions s ON s.id = h.subscription_id"
                f" WHERE h.id IN ({ph}) AND s.notify_email = 1",
                hit_ids,
            )
        ]
    finally:
        conn.close()
    if not rows:
        return None

    groups: dict[int, list[dict]] = defaultdict(list)
    for r in rows:
        groups[r["subscription_id"]].append(r)

    blocks = []
    total = 0
    stamp = datetime.fromtimestamp(max(r["matched_at"] for r in rows)).strftime("%m-%d %H:%M")
    for sid, items in sorted(
        groups.items(), key=lambda kv: -max(i["heat"] for i in kv[1])
    ):
        picked = sorted(items, key=lambda x: -x["heat"])[:MAX_PER_SUB]
        total += len(items)
        label = describe({"keyword": items[0]["keyword"], "tag_filter": items[0]["tag_filter"]})
        lines = [f"【{label}】命中 {len(items)} 条，展示热度最高的 {len(picked)} 条："]
        for it in picked:
            lines.append(f"  · {it['title']}（热度 {it['heat']:.2f}）")
            lines.append(f"    {it['url']}")
        blocks.append("\n".join(lines))

    subject = f"NewsStation 订阅摘要 · {total} 条新命中 · {stamp}"
    text = "\n\n".join(
        [
            f"NewsStation 订阅摘要 · {stamp}",
            f"本轮 {len(groups)} 个订阅共命中 {total} 条报道；每订阅最多 {MAX_PER_SUB} 条，按事件热度排序。",
            *blocks,
            "— 本地运行 · 仅存标题与摘要 · 点击链接跳原文",
        ]
    )
    return {
        "subject": subject, "text": text, "count": total,
        "groups": len(groups), "body": text,
    }


def send_digest(settings, digest: dict | None) -> bool:
    smtp = settings.smtp
    if not digest or not smtp.enabled:
        return False
    msg = EmailMessage()
    msg["Subject"] = digest["subject"]
    msg["From"] = smtp.user
    msg["To"] = smtp.to
    msg.set_content(digest["text"])
    with smtplib.SMTP_SSL(smtp.host, smtp.port, timeout=15) as server:
        server.login(smtp.user, smtp.password)
        server.send_message(msg)
    return True
