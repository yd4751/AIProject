"""M4 验收：订阅匹配、去重/未读、摘要邮件、页面与端到端抓取命中。"""

from __future__ import annotations

import asyncio
import time
from datetime import datetime, timezone
from email.utils import format_datetime

import httpx
import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from app import config, db, notify
from app.ingest import fetch, registry
from app.ingest.fetch import LAST_ANALYSIS, run_once
from app.ingest.rss import RSSSource
from app.notify import mailer
from app.web.routes import router


@pytest.fixture()
def env(tmp_path):
    db_path = tmp_path / "notify.db"
    db.init_db(db_path)
    conn = db.connect(db_path)
    now = int(time.time())

    conn.executemany(
        "INSERT INTO tags(dim,value,label,aliases,parent) VALUES(?,?,?,'',NULL)",
        [
            ("region", "cn", "中国"), ("region", "us", "美国"),
            ("industry", "tech", "科技"), ("industry", "finance", "财经"),
            ("type", "earnings", "财报业绩"),
        ],
    )
    conn.executemany(
        "INSERT INTO events(id,title,category,heat,momentum,article_count,source_count,"
        " keywords,first_seen,last_seen) VALUES(?,?,?,?,?,?,?,'[]',?,?)",
        [(1, "英伟达财报", "tech", 5.0, 0.5, 5, 2, now - 7200, now - 1800),
         (2, "苹果供应链", "finance", 2.0, 0.0, 2, 1, now - 7200, now - 1800)],
    )
    conn.executemany(
        "INSERT INTO event_tags(event_id,dim,value,hit_count,locked) VALUES(?,?,?,?,0)",
        [(1, "region", "cn", 5), (1, "industry", "tech", 5), (1, "type", "earnings", 3),
         (2, "region", "us", 2), (2, "industry", "finance", 2)],
    )
    articles = [
        (1, "英伟达财报超预期 NVIDIA earnings", 1),
        (2, "英伟达新品发布", 1),
        (3, "AI 芯片需求强劲，财报亮眼", 1),
        (4, "英伟达数据中心业务增长", 1),
        (5, "英伟达股价创新高", 1),
        (6, "苹果供应链消息", 2),
        (7, "美股收盘综述", 2),
    ]
    conn.executemany(
        "INSERT INTO articles(id,source_id,url,title,summary,lang,published_at,fetched_at,"
        " event_id,analyzed) VALUES(?,?,?,?,?,'zh',?,?,?,1)",
        [(aid, "src", f"https://e.com/{aid}", title, "摘要",
          now - 3600, now - 3600, eid) for aid, title, eid in articles],
    )
    conn.commit()

    settings = config.Settings(db_path=str(db_path))
    app = FastAPI()
    app.state.settings = settings
    app.include_router(router)
    client = TestClient(app)
    yield client, conn, db_path, settings, now
    conn.close()


def test_keyword_subscription_and_dedup(env):
    client, conn, db_path, settings, now = env
    r = client.post(
        "/subscriptions/add", data={"keyword": "英伟达", "notify_email": "on"},
        follow_redirects=False,
    )
    assert r.status_code == 303

    stats = notify.match_new(db_path, 0)
    assert stats["new_hits"] == 4  # 4 篇标题含「英伟达」
    assert notify.unread_total(conn) == 4
    assert conn.execute("SELECT unread_count FROM subscriptions").fetchone()[0] == 4

    again = notify.match_new(db_path, 0)
    assert again["new_hits"] == 0, "同一篇文章不能重复计未读"
    assert notify.unread_total(conn) == 4

    # 大小写不敏感 + 多关键字 OR
    client.post("/subscriptions/add", data={"keyword": "NVIDIA, 美股"}, follow_redirects=False)
    stats2 = notify.match_new(db_path, 0)
    assert stats2["new_hits"] == 2  # a1 含 NVIDIA、a7 含美股


def test_tag_subscription_requires_all_dimensions(env):
    client, conn, db_path, settings, now = env
    client.post(
        "/subscriptions/add",
        data={"region": "cn", "industry": "tech", "type": "earnings"},
        follow_redirects=False,
    )
    stats = notify.match_new(db_path, 0)
    assert stats["new_hits"] == 5, "只命中同时满足三个维度的事件 1"

    # 维度内 OR：region=us 或 cn 都能命中
    client.post("/subscriptions/add", data={"region": "us"}, follow_redirects=False)
    stats2 = notify.match_new(db_path, 0)
    assert stats2["new_hits"] == 2, "事件 2 的两篇文章"


def test_keyword_and_tag_are_anded(env):
    client, conn, db_path, settings, now = env
    client.post(
        "/subscriptions/add",
        data={"keyword": "NVIDIA", "industry": "tech"},
        follow_redirects=False,
    )
    stats = notify.match_new(db_path, 0)
    assert stats["new_hits"] == 1
    hit = conn.execute("SELECT title FROM subscription_hits").fetchone()[0]
    assert "NVIDIA" in hit


def test_mark_read_delete_and_api(env):
    client, conn, db_path, settings, now = env
    client.post("/subscriptions/add", data={"keyword": "英伟达"}, follow_redirects=False)
    notify.match_new(db_path, 0)
    sid = conn.execute("SELECT id FROM subscriptions").fetchone()[0]
    assert conn.execute("SELECT unread_count FROM subscriptions WHERE id=?", (sid,)).fetchone()[0] == 4
    assert client.get("/api/subscriptions").json()["unread"] == 4
    assert client.get("/api/stats").json()["unread"] == 4

    client.post(f"/subscriptions/{sid}/read", follow_redirects=False)
    assert conn.execute("SELECT unread_count FROM subscriptions WHERE id=?", (sid,)).fetchone()[0] == 0
    assert conn.execute("SELECT COUNT(*) FROM subscription_hits WHERE unread=1").fetchone()[0] == 0
    assert client.get("/api/subscriptions").json()["unread"] == 0

    client.post(f"/subscriptions/{sid}/delete", follow_redirects=False)
    assert conn.execute("SELECT COUNT(*) FROM subscriptions").fetchone()[0] == 0
    assert conn.execute("SELECT COUNT(*) FROM subscription_hits").fetchone()[0] == 0


def test_subscription_page_and_form_validation(env):
    client, conn, db_path, settings, now = env
    r = client.get("/subscriptions")
    assert r.status_code == 200
    assert "新增订阅" in r.text and "命中时间线" in r.text
    assert "未配置 smtp.pass" in r.text

    r = client.post("/subscriptions/add", data={}, follow_redirects=False)
    assert r.headers["location"].endswith("error=empty")

    client.post(
        "/subscriptions/add",
        data={"keyword": "英伟达", "region": "cn", "industry": "tech", "notify_email": "on"},
        follow_redirects=False,
    )
    page = client.get("/subscriptions").text
    assert "关键字「英伟达」" in page
    assert "标签 地区=cn" in page
    assert "我的订阅" in page

    notify.match_new(db_path, 0)
    page = client.get("/subscriptions").text
    assert "英伟达财报超预期" in page
    assert "未读" in page
    assert 'href="/subscriptions' in page


def test_digest_caps_five_and_sends_one_mail(env, monkeypatch):
    client, conn, db_path, settings, now = env
    conn.execute(
        "INSERT INTO subscriptions(keyword,tag_filter,created_at,unread_count,notify_email)"
        " VALUES('英伟达',NULL,?,0,1)", (now,)
    )
    sid = conn.execute("SELECT last_insert_rowid()").fetchone()[0]
    for i in range(7):
        conn.execute(
            "INSERT INTO subscription_hits(subscription_id,article_id,title,url,heat,matched_at,unread)"
            " VALUES(?,?,?,?,?,?,1)",
            (sid, 100 + i, f"标题{i}", f"https://e.com/{i}", float(i), now),
        )
    conn.commit()
    ids = [r[0] for r in conn.execute("SELECT id FROM subscription_hits")]

    digest = mailer.build_digest(db_path, ids)
    assert digest["count"] == 7 and digest["groups"] == 1
    assert digest["text"].count("https://e.com/") == 5, "每订阅最多 5 条"
    assert "标题6" in digest["text"] and "标题1" not in digest["text"], "按热度降序取前 5"
    assert "7 条新命中" in digest["subject"]

    sent = []

    class FakeSMTP:
        def __init__(self, host, port, timeout=None):
            sent.append(("init", host, port))

        def __enter__(self):
            return self

        def __exit__(self, *args):
            return False

        def login(self, user, password):
            sent.append(("login", user, password))

        def send_message(self, msg):
            sent.append(("send", msg["To"], msg["Subject"], msg.get_content()))

    monkeypatch.setattr(mailer.smtplib, "SMTP_SSL", FakeSMTP)
    smtp_settings = config.Settings(
        db_path=str(db_path),
        smtp={"host": "smtp.test", "port": 465, "user": "me@test",
              "pass": "secret", "to": "you@test"},
    )
    assert smtp_settings.smtp.enabled
    assert mailer.send_digest(smtp_settings, digest) is True
    sends = [s for s in sent if s[0] == "send"]
    assert len(sends) == 1, "一轮抓取只发一封摘要"
    assert sends[0][1] == "you@test"
    assert "NewsStation 订阅摘要" in sends[0][2]

    # 未配置 SMTP 时降级：照常记录命中、合成摘要，但不发信
    stats = notify.after_fetch(db_path, settings, 0)
    assert stats["smtp_enabled"] is False
    assert stats["mail_sent"] is False
    assert stats["digest_subject"]


def test_end_to_end_fetch_triggers_match(env, monkeypatch):
    """下一轮抓取的新文章，能自动命中订阅并在站内出现未读。"""
    client, conn, db_path, settings, now = env
    client.post("/subscriptions/add", data={"keyword": "英伟达"}, follow_redirects=False)

    feed = (
        '<?xml version="1.0"?><rss version="2.0"><channel><title>mock</title>'
        "<item><title>英伟达发布新一代 GPU</title><link>https://e.com/new-gpu</link>"
        f"<description>摘要</description><pubDate>{format_datetime(datetime.now(timezone.utc))}</pubDate>"
        "</item></channel></rss>"
    ).encode()

    def handler(request):
        return httpx.Response(200, content=feed)

    monkeypatch.setattr(registry, "_registry", {})
    registry.register(
        RSSSource(id="mock", name="Mock", homepage="", lang="zh", feed="https://e.com/feed")
    )
    real_client = httpx.AsyncClient
    monkeypatch.setattr(
        fetch.httpx, "AsyncClient",
        lambda **kw: real_client(transport=httpx.MockTransport(handler), **kw),
    )

    asyncio.run(run_once(settings, None))
    stats = LAST_ANALYSIS["notify"]
    assert stats["new_articles"] >= 1
    assert stats["new_hits"] >= 1, "新抓的文章应命中关键字订阅"
    assert stats["mail_sent"] is False  # 未配 SMTP

    page = client.get("/subscriptions").text
    assert "英伟达发布新一代 GPU" in page
    assert "未读" in page
