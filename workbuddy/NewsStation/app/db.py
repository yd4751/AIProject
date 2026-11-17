"""SQLite 访问层。写少读多，用 WAL + 短连接，避免跨线程共享连接。"""

from __future__ import annotations

import sqlite3
from pathlib import Path

SCHEMA = """
CREATE TABLE IF NOT EXISTS articles (
  id INTEGER PRIMARY KEY,
  source_id TEXT NOT NULL,
  url TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL,
  summary TEXT,
  lang TEXT NOT NULL,
  category TEXT,
  published_at INTEGER,
  fetched_at INTEGER NOT NULL,
  event_id INTEGER,
  analyzed INTEGER DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_articles_time ON articles(published_at DESC);
CREATE INDEX IF NOT EXISTS idx_articles_cat ON articles(category, published_at DESC);
CREATE INDEX IF NOT EXISTS idx_articles_event ON articles(event_id);

CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY,
  title TEXT NOT NULL,
  category TEXT,
  heat REAL DEFAULT 0,
  momentum REAL DEFAULT 0,
  article_count INTEGER DEFAULT 0,
  source_count INTEGER DEFAULT 0,
  keywords TEXT,
  first_seen INTEGER,
  last_seen INTEGER
);
CREATE INDEX IF NOT EXISTS idx_events_heat ON events(heat DESC, last_seen DESC);

CREATE TABLE IF NOT EXISTS subscriptions (
  id INTEGER PRIMARY KEY,
  keyword TEXT,
  tag_filter TEXT,
  created_at INTEGER,
  last_matched_at INTEGER,
  unread_count INTEGER DEFAULT 0,
  notify_email INTEGER DEFAULT 1
);

CREATE TABLE IF NOT EXISTS source_state (
  source_id TEXT PRIMARY KEY,
  enabled INTEGER DEFAULT 1,
  last_run INTEGER,
  last_status TEXT,
  fail_streak INTEGER DEFAULT 0,
  last_count INTEGER
);

-- M4：订阅命中时间线。UNIQUE 保证同一篇文章对同一订阅只计一次未读。
CREATE TABLE IF NOT EXISTS subscription_hits (
  id INTEGER PRIMARY KEY,
  subscription_id INTEGER NOT NULL,
  article_id INTEGER NOT NULL,
  event_id INTEGER,
  title TEXT NOT NULL,
  url TEXT NOT NULL,
  heat REAL DEFAULT 0,
  matched_at INTEGER NOT NULL,
  unread INTEGER DEFAULT 1,
  UNIQUE(subscription_id, article_id)
);
CREATE INDEX IF NOT EXISTS idx_sh_sub ON subscription_hits(subscription_id, matched_at DESC);
CREATE INDEX IF NOT EXISTS idx_sh_unread ON subscription_hits(unread);

CREATE TABLE IF NOT EXISTS tags (
  id INTEGER PRIMARY KEY,
  dim TEXT NOT NULL,
  value TEXT NOT NULL,
  label TEXT NOT NULL,
  aliases TEXT,
  parent TEXT,
  UNIQUE(dim, value)
);
CREATE INDEX IF NOT EXISTS idx_tags_dim ON tags(dim, value);

CREATE TABLE IF NOT EXISTS article_tags (
  article_id INTEGER NOT NULL,
  dim TEXT NOT NULL,
  value TEXT NOT NULL,
  confidence REAL,
  origin TEXT,
  PRIMARY KEY (article_id, dim, value)
);
CREATE INDEX IF NOT EXISTS idx_at_value ON article_tags(dim, value);

CREATE TABLE IF NOT EXISTS event_tags (
  event_id INTEGER NOT NULL,
  dim TEXT NOT NULL,
  value TEXT NOT NULL,
  hit_count INTEGER DEFAULT 0,
  locked INTEGER DEFAULT 0,
  PRIMARY KEY (event_id, dim, value)
);
CREATE INDEX IF NOT EXISTS idx_et_value ON event_tags(dim, value);
CREATE INDEX IF NOT EXISTS idx_et_event ON event_tags(event_id, dim, hit_count DESC);

-- M5：一键翻译结果缓存。同一文本 + 目标语言只翻一次，失败 1 小时内不重试。
CREATE TABLE IF NOT EXISTS translations (
  id INTEGER PRIMARY KEY,
  source_hash TEXT NOT NULL,
  source_text TEXT NOT NULL,
  target_lang TEXT NOT NULL,
  backend TEXT NOT NULL,
  translated TEXT,
  status TEXT NOT NULL,             -- ok / failed / skipped
  created_at INTEGER NOT NULL,
  UNIQUE(source_hash, target_lang)
);
CREATE INDEX IF NOT EXISTS idx_tr_lookup ON translations(source_hash, target_lang);

-- M6：社交媒体热榜快照。一轮抓取共用一个 batch_id；rank_delta 为与上一批的排名差，
-- NULL 表示新进榜。与 articles 分开存，因为榜单是「排名/热度随时间变化」的快照，
-- 一篇文章 URL 只入库一次的语义不适用。
CREATE TABLE IF NOT EXISTS social_trends (
  id INTEGER PRIMARY KEY,
  batch_id INTEGER NOT NULL,
  platform TEXT NOT NULL,
  source_id TEXT NOT NULL,
  title TEXT NOT NULL,
  url TEXT NOT NULL,
  rank INTEGER NOT NULL,
  heat_value REAL DEFAULT 0,
  rank_delta INTEGER,
  summary TEXT,
  lang TEXT DEFAULT 'zh',
  extra TEXT,
  captured_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_st_batch ON social_trends(batch_id, platform, rank);
CREATE INDEX IF NOT EXISTS idx_st_platform ON social_trends(platform, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_st_time ON social_trends(captured_at DESC);

-- M7：站内阅读缓存。服务端代取并清洗过的正文按 URL hash 缓存，避免重复请求原站。
-- status: ok（正文充足）/ thin（内容不足，通常是登录墙或 JS 渲染页）/ error
CREATE TABLE IF NOT EXISTS reader_cache (
  url_hash TEXT PRIMARY KEY,
  url TEXT NOT NULL,
  final_url TEXT,
  title TEXT,
  site TEXT,
  description TEXT,
  content_html TEXT,
  text_len INTEGER DEFAULT 0,
  status TEXT NOT NULL,
  note TEXT,
  transport TEXT,
  fetched_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_rc_time ON reader_cache(fetched_at);

-- M8：域名路由记忆。记下某站点上次走「直连」还是「代理」成功，
-- 下次同域名直接优先该通道，避免每次都从直连试起、白等一次超时。
CREATE TABLE IF NOT EXISTS reader_routes (
  host TEXT PRIMARY KEY,
  transport TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);

-- M7：外部搜索结果缓存。同一关键字 + 同一 provider 在 TTL 内复用，避免频繁打搜索站。
CREATE TABLE IF NOT EXISTS search_cache (
  key TEXT PRIMARY KEY,
  provider TEXT NOT NULL,
  query TEXT NOT NULL,
  payload TEXT NOT NULL,
  fetched_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sc_time ON search_cache(fetched_at);
"""


def connect(db_path: str | Path) -> sqlite3.Connection:
    conn = sqlite3.connect(str(db_path), timeout=15.0)
    conn.row_factory = sqlite3.Row
    conn.execute("PRAGMA journal_mode=WAL")
    conn.execute("PRAGMA synchronous=NORMAL")
    conn.execute("PRAGMA busy_timeout=15000")
    return conn


def init_db(db_path: str | Path) -> None:
    Path(db_path).parent.mkdir(parents=True, exist_ok=True)
    conn = connect(db_path)
    try:
        conn.executescript(SCHEMA)
        _migrate(conn)
        conn.commit()
    finally:
        conn.close()


# 老库补列：CREATE TABLE IF NOT EXISTS 不会给已存在的表加字段，必须显式 ALTER
MIGRATIONS = (
    ("articles", "analyzed", "INTEGER DEFAULT 0"),
    ("events", "momentum", "REAL DEFAULT 0"),
)


def _migrate(conn: sqlite3.Connection) -> None:
    for table, column, decl in MIGRATIONS:
        cols = {r["name"] for r in conn.execute(f"PRAGMA table_info({table})")}
        if column not in cols:
            conn.execute(f"ALTER TABLE {table} ADD COLUMN {column} {decl}")
    conn.execute("CREATE INDEX IF NOT EXISTS idx_articles_analyzed ON articles(analyzed)")
    conn.execute("CREATE INDEX IF NOT EXISTS idx_events_last ON events(last_seen DESC)")
