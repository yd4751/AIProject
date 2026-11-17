"""M5 一键翻译验收：语言检测、缓存、后端降级、自动回退、API 与模板标记。

与 M3/M4 一致：临时库 + 独立 FastAPI app（不跑 lifespan，不碰生产库），
外部翻译端点一律用 FakeBackend / mock httpx 替身，测试不依赖网络。
"""
from __future__ import annotations

import time

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from app import config, db, translate
from app.translate import backends, service
from app.web.routes import router

EN = "Apple launches a new iPhone in China"
EN2 = "The central bank raised interest rates"
ZH = "苹果发布新手机"


class FakeBackend:
    def __init__(self, mapping=None, fail=False):
        self.name = "fake"
        self.mapping = mapping or {}
        self.fail = fail
        self.calls = 0

    def translate_batch(self, texts, target, settings):
        self.calls += 1
        if self.fail:
            return [None] * len(texts)
        return [self.mapping.get(t, "【译】" + t) for t in texts]


@pytest.fixture()
def db_path(tmp_path):
    p = tmp_path / "tr.db"
    db.init_db(p)
    return p


def settings_for(p, backend="none", **kw):
    return config.Settings(db_path=str(p), translate={"backend": backend, **kw})


def client_for(settings):
    app = FastAPI()
    app.state.settings = settings
    app.include_router(router)
    return TestClient(app)


def seed_pages(p):
    """建一个含英文 + 中文事件的最小站点数据，供模板标记测试用。"""
    conn = db.connect(p)
    now = int(time.time())
    conn.executemany(
        "INSERT INTO tags(dim,value,label,aliases,parent) VALUES(?,?,?,'',NULL)",
        [("region", "us", "美国"), ("industry", "tech", "科技"),
         ("type", "product", "产品发布"), ("entity", "apple", "苹果")],
    )
    conn.executemany(
        "INSERT INTO events(id,title,category,heat,momentum,article_count,source_count,"
        " keywords,first_seen,last_seen) VALUES(?,?,?,?,?,?,?,'[]',?,?)",
        [(1, EN, "tech", 5.0, 0.2, 1, 1, now - 3600, now),
         (2, ZH, "tech", 3.0, 0.0, 1, 1, now - 3600, now)],
    )
    conn.executemany(
        "INSERT INTO event_tags(event_id,dim,value,hit_count,locked) VALUES(?,?,?,?,0)",
        [(1, "industry", "tech", 1), (2, "industry", "tech", 1)],
    )
    conn.executemany(
        "INSERT INTO articles(id,source_id,url,title,summary,lang,published_at,fetched_at,"
        " event_id,analyzed) VALUES(?,?,?,?,?,'en',?,?,?,1)",
        [(1, "bbc", "https://e.com/1", EN, "A new phone was unveiled", now - 3600, now, 1),
         (2, "src", "https://e.com/2", ZH, "摘要", now - 3600, now, 2)],
    )
    conn.commit()
    conn.close()


# ---------------------------------------------------------------- 语言检测

def test_language_detection():
    assert translate.needs_translation(EN, "zh-CN") is True
    assert translate.needs_translation(ZH, "zh-CN") is False
    assert translate.needs_translation("英伟达财报超预期 NVIDIA earnings", "zh-CN") is False
    assert translate.needs_translation("AI", "zh-CN") is False            # 太短
    assert translate.needs_translation("2026/11/05 10:30", "zh-CN") is False  # 纯数字
    assert translate.needs_translation(ZH, "en") is True                  # 反向：中→英
    assert translate.needs_translation(EN, "en") is False


# ---------------------------------------------------------------- 缓存与降级

def test_cache_hit_skips_backend(db_path, monkeypatch):
    fake = FakeBackend()
    monkeypatch.setattr(service, "get_backend", lambda s: fake)
    s = settings_for(db_path)

    r1 = translate.translate_texts(db_path, s, [EN, EN2, ZH])
    assert r1["stats"]["translated"] == 2 and r1["stats"]["skipped"] == 1
    assert fake.calls == 1

    r2 = translate.translate_texts(db_path, s, [EN, EN2])
    assert r2["stats"]["cached"] == 2 and r2["stats"]["translated"] == 0
    assert fake.calls == 1, "命中缓存后不应再调用外部后端"

    conn = db.connect(db_path)
    assert conn.execute("SELECT COUNT(*) FROM translations").fetchone()[0] == 2
    assert all(r["status"] == "ok" for r in conn.execute("SELECT status FROM translations"))
    conn.close()


def test_backend_failure_degrades_with_cooldown(db_path, monkeypatch):
    fake = FakeBackend(fail=True)
    monkeypatch.setattr(service, "get_backend", lambda s: fake)
    s = settings_for(db_path)

    r = translate.translate_texts(db_path, s, [EN, EN2])
    assert r["results"] == {} and r["stats"]["failed"] == 2, "单条失败不能抛异常"

    conn = db.connect(db_path)
    assert all(x["status"] == "failed" for x in conn.execute("SELECT status FROM translations"))
    conn.close()

    again = translate.translate_texts(db_path, s, [EN, EN2])
    assert fake.calls == 1, "失败结果 1 小时内不重试"
    assert again["stats"]["translated"] == 0


def test_unconfigured_backend_writes_no_failure_cache(db_path, monkeypatch):
    """后端没配好时不落失败缓存，用户填好配置后应立刻可用。"""
    s = settings_for(db_path)
    r = translate.translate_texts(db_path, s, [EN])
    assert r["enabled"] is False and r["results"] == {}
    conn = db.connect(db_path)
    assert conn.execute("SELECT COUNT(*) FROM translations").fetchone()[0] == 0
    conn.close()

    fake = FakeBackend(mapping={EN: "苹果在中国推出新款 iPhone"})
    monkeypatch.setattr(service, "get_backend", lambda s: fake)
    r2 = translate.translate_texts(db_path, s, [EN])
    assert r2["results"][EN] == "苹果在中国推出新款 iPhone"


def test_auto_backend_falls_back(db_path, monkeypatch):
    calls = []

    class Fail:
        name = "mymemory"

        def translate_batch(self, texts, target, settings):
            calls.append("mymemory")
            return [None] * len(texts)

    class Ok:
        name = "google_free"

        def translate_batch(self, texts, target, settings):
            calls.append("google_free")
            return ["【回退】" + t for t in texts]

    monkeypatch.setitem(backends.BACKENDS, "mymemory", Fail)
    monkeypatch.setitem(backends.BACKENDS, "google_free", Ok)
    s = settings_for(db_path, "auto")

    r = translate.translate_texts(db_path, s, [EN, EN2])
    assert calls == ["mymemory", "google_free"], "第一个后端全失败才回退"
    assert r["results"][EN].startswith("【回退】")
    assert r["stats"]["translated"] == 2 and r["stats"]["failed"] == 0


# ---------------------------------------------------------------- 后端解析

def test_openai_backend_parses_fenced_json(monkeypatch, tmp_path):
    class FakeClient:
        def __init__(self, **kw):
            pass

        def __enter__(self):
            return self

        def __exit__(self, *args):
            return False

        def post(self, url, **kw):
            class R:
                def raise_for_status(self):
                    pass

                def json(self):
                    return {"choices": [{"message": {
                        "content": '```json\n["译 A","译 B"]\n```'
                    }}]}

            return R()

    monkeypatch.setattr(backends.httpx, "Client", FakeClient)
    s = config.Settings(
        db_path=str(tmp_path / "x.db"),
        translate={"backend": "openai",
                   "openai": {"base_url": "https://api.test/v1", "api_key": "k", "model": "m"}},
    )
    b = backends.get_backend(s)
    assert b.name == "openai"
    assert b.translate_batch(["A", "B"], "zh-CN", s) == ["译 A", "译 B"]


# ---------------------------------------------------------------- Web 层

def test_api_translate_roundtrip(db_path, monkeypatch):
    fake = FakeBackend(mapping={EN: "苹果在中国推出新款 iPhone"})
    monkeypatch.setattr(service, "get_backend", lambda s: fake)
    s = settings_for(db_path)
    c = client_for(s)

    st = c.get("/api/translate/status").json()
    assert st["enabled"] is True and st["backend"] == "fake"

    r = c.post("/api/translate", json={"texts": [EN, ZH, EN]}).json()
    assert r["results"][EN] == "苹果在中国推出新款 iPhone"
    assert r["stats"]["requested"] == 2, "重复文本去重"
    assert r["stats"]["skipped"] == 1, "中文不送后端"
    assert c.get("/api/translate/status").json()["cached_ok"] == 1

    # 坏 payload 不能把接口打崩
    assert c.post("/api/translate", json={"texts": "not-a-list"}).status_code == 200
    assert c.post("/api/translate", content="not json").status_code == 200


def test_template_marks_foreign_only(db_path):
    seed_pages(db_path)
    s = settings_for(db_path)
    c = client_for(s)

    html = c.get("/").text
    assert 'id="tr-toggle"' in html
    assert 'data-tr data-text="%s"' % EN in html, "英文标题需打标"
    assert 'data-text="%s"' % ZH not in html, "中文标题保持原文、不打标"
    assert html.count("data-tr") >= 2, "事件标题与最新报道标题都要能翻"

    detail = c.get("/event/1").text
    assert detail.count("data-tr") >= 2
    assert 'class="summary" data-tr' in detail, "详情页摘要也参与一键翻译"

    # 缓存译文由服务端直接注入 data-zh，刷新后无需重翻
    conn = db.connect(db_path)
    conn.execute(
        "INSERT INTO translations(source_hash,source_text,target_lang,backend,translated,status,created_at)"
        " VALUES(?,?,?,'fake',?,'ok',?)",
        (translate.text_hash(EN, s.translate.target), EN, s.translate.target,
         "苹果在中国推出新款 iPhone", int(time.time())),
    )
    conn.commit()
    conn.close()
    assert 'data-zh="苹果在中国推出新款 iPhone"' in c.get("/").text


def test_template_marks_translation_scope(db_path):
    """M8 粒度化：标题标 title、摘要标 para，顶栏三档范围控件齐全。"""
    import re

    seed_pages(db_path)
    s = settings_for(db_path)
    s.reader.enabled = False           # /read 不做网络代取
    c = client_for(s)

    home = c.get("/").text
    assert 'id="tr-group"' in home
    for scope in ("title", "para", "all"):
        assert 'data-tr-scope="%s"' % scope in home, "顶栏需有 %s 档" % scope
    assert home.count('data-tr-scope="title"') >= 2, "标题节点默认标 title"
    # 节点上只允许两种 scope，顶栏那三个是按钮不是节点标记
    node_scopes = set(re.findall(r'data-text="[^"]*" data-zh="[^"]*" data-tr-scope="(\w+)"', home))
    assert node_scopes <= {"title", "para"}

    detail = c.get("/event/1").text
    assert 'class="summary" data-tr data-text=' in detail
    assert 'data-tr-scope="para"' in detail, "摘要/段落应归到 para 档"

    read = c.get("/read?url=https%3A%2F%2Fexample.com%2Fa").text
    assert 'id="rd-tr-group"' in read, "阅读页需有三档控件"
    for scope in ("title", "para", "all"):
        assert 'data-rd-scope="%s"' % scope in read, "阅读页需有 %s 档" % scope
