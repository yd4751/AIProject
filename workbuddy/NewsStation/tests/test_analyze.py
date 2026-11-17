"""M2 分析管道测试：标签归一化、边界、聚类、热度、管道幂等。"""

from __future__ import annotations

import time

import pytest

from app import config, db
from app.analyze import heat, pipeline
from app.analyze.cluster import Clusterer, cosine, jaccard
from app.analyze.tagger import DictTagger
from app.analyze.tokenize import detect_lang, weighted_counter


@pytest.fixture(scope="module")
def tagger() -> DictTagger:
    return DictTagger()


def _dims(tags) -> dict[str, set[str]]:
    out: dict[str, set[str]] = {}
    for t in tags:
        out.setdefault(t.dim, set()).add(t.value)
    return out


# ---- 标签 ----

def test_region_alias_normalized(tagger):
    """US / United States / 美国 必须落到同一个 region:us。"""
    for text in ("US officials said", "United States officials", "美国官员表示"):
        dims = _dims(tagger.tag(text))
        assert "us" in dims.get("region", set()), text


def test_pronoun_us_not_tagged(tagger):
    """小写 us 是代词，不能当成美国。"""
    dims = _dims(tagger.tag("Justices weigh whether the law applies to us and other firms"))
    assert "us" not in dims.get("region", set())


def test_word_boundary(tagger):
    """ASCII 词面必须整词命中，"chairman" 里不该命中 "china"。"""
    dims = _dims(tagger.tag("The chairman of the board resigned"))
    assert "cn" not in dims.get("region", set())


def test_hk_mo_tw_parent_cn(tagger):
    assert tagger.parents.get("hk") == "cn"
    assert tagger.parents.get("tw") == "cn"
    assert tagger.parents.get("mo") == "cn"


def test_type_pattern_and_industry(tagger):
    dims = _dims(tagger.tag("英伟达第三季度财报超预期，营收同比增长 94%", "美国芯片厂商公布季报"))
    assert "earnings" in dims.get("type", set())
    assert "tech" in dims.get("industry", set())
    assert "us" in dims.get("region", set())


def test_no_industry_from_summary_only(tagger):
    """只在摘要里命中一次行业，精度太差，应当被丢掉。"""
    dims = _dims(tagger.tag("某公司发布公告", "涉及半导体业务"))
    assert "tech" not in dims.get("industry", set())


def test_boolean_like_value(tagger):
    """YAML 里 value: no（挪威）会被解析成布尔，装载层必须转字符串。"""
    dims = _dims(tagger.tag("Norway raises rates"))
    assert "no" in dims.get("region", set())


# ---- 分词 ----

def test_detect_lang():
    assert detect_lang("这是一条中文新闻标题") == "zh"
    assert detect_lang("This is an English headline") == "en"
    assert detect_lang("") == "en"


def test_title_weight():
    c = weighted_counter("芯片 芯片", "芯片", "zh")
    assert c["芯片"] == pytest.approx(3.0 + 1.0)


# ---- 相似度与聚类 ----

def test_cosine_and_jaccard():
    a = weighted_counter("美国中期选举结果出炉", "", "zh")
    b = weighted_counter("美国中期选举结果出炉", "", "zh")
    assert cosine(a, b) == pytest.approx(1.0)
    assert jaccard({"region:us"}, {"region:us", "industry:politics"}) == pytest.approx(0.5)
    assert jaccard(set(), {"a"}) == 0.0


def test_cluster_assigns_similar_article(tmp_path):
    db_path = tmp_path / "c.db"
    db.init_db(db_path)
    conn = db.connect(db_path)
    from app.analyze.tagger import get_tagger

    tg = get_tagger()
    cl = Clusterer(conn, threshold=0.45)
    title = "西班牙交通部长在列车事故后辞职"
    counter = weighted_counter(title, "", "zh")
    tags = {f"{t.dim}:{t.value}" for t in tg.tag(title)}
    event_id, _ = cl.assign({"title": title, "source_id": "s1"}, counter, tags)
    assert event_id is None  # 没有候选，必须新建
    cl.create(100, {"title": title, "source_id": "s1"}, counter, tags, int(time.time()))

    sim_title = "西班牙交通部长因列车事故辞职"
    merged_id, sim = cl.assign(
        {"title": sim_title, "source_id": "s2"},
        weighted_counter(sim_title, "", "zh"),
        {f"{t.dim}:{t.value}" for t in tg.tag(sim_title)},
    )
    assert merged_id == 100 and sim >= 0.45

    far = "英超曼联主场逆转利物浦"
    none_id, _ = cl.assign(
        {"title": far, "source_id": "s3"},
        weighted_counter(far, "", "zh"),
        {f"{t.dim}:{t.value}" for t in tg.tag(far)},
    )
    assert none_id is None
    conn.close()


# ---- 热度 ----

def test_heat_decay_and_momentum():
    now = 1_700_000_000
    fresh = [now - 600, now - 1200, now - 1800]
    old = [now - 20 * 3600, now - 21 * 3600, now - 22 * 3600]
    h_fresh = heat.compute(fresh, {"a", "b"}, now)
    h_old = heat.compute(old, {"a", "b"}, now)
    assert h_fresh["heat"] > h_old["heat"]
    assert h_fresh["momentum"] > 0
    # 20 小时前的事件仍在 24h 计数窗口内，但指数衰减把它压到新鲜事件的 1/10 以下
    assert h_old["recent"] == 3
    assert h_old["heat"] < h_fresh["heat"] * 0.1


def test_momentum_needs_sample():
    now = 1_700_000_000
    single = [now - 300]
    assert heat.compute(single, {"a"}, now)["momentum"] == 0.0


# ---- 管道 ----

def _seed(db_path, rows):
    conn = db.connect(db_path)
    now = int(time.time())
    conn.executemany(
        "INSERT INTO articles (source_id, url, title, summary, lang, published_at, fetched_at)"
        " VALUES (?,?,?,?,?,?,?)",
        [(r[0], r[1], r[2], r[3], r[4], now - r[5], now) for r in rows],
    )
    conn.commit()
    conn.close()


def test_pipeline_end_to_end_and_idempotent(tmp_path):
    db_path = tmp_path / "p.db"
    db.init_db(db_path)
    _seed(db_path, [
        ("bbc-world", "https://x/1", "Spain transport minister resigns after train crash", "", "en", 600),
        ("nyt-world", "https://x/2", "Spain's transport minister resigns after deadly train crash", "", "en", 900),
        ("sina-world", "https://x/3", "英超：曼联主场逆转利物浦", "", "zh", 1200),
    ])
    settings = config.Settings()

    first = pipeline.process(db_path, settings)
    assert first["articles"] == 3
    assert first["events_new"] == 2  # 两条西班牙同事件合并

    second = pipeline.process(db_path, settings)
    assert second["articles"] == 0  # 幂等：没有新文章就不重复处理

    conn = db.connect(db_path)
    merged = conn.execute(
        "SELECT article_count, source_count FROM events WHERE article_count = 2"
    ).fetchone()
    assert merged["source_count"] == 2
    assert conn.execute("SELECT COUNT(*) FROM article_tags").fetchone()[0] > 0
    assert conn.execute(
        "SELECT COUNT(*) FROM articles WHERE event_id IS NULL"
    ).fetchone()[0] == 0
    conn.close()


def test_pipeline_marks_analyzed(tmp_path):
    db_path = tmp_path / "m.db"
    db.init_db(db_path)
    _seed(db_path, [("sspai", "https://y/1", "少数派发布新版客户端", "", "zh", 60)])
    pipeline.process(db_path, config.Settings())
    conn = db.connect(db_path)
    row = conn.execute("SELECT analyzed, category FROM articles").fetchone()
    assert row["analyzed"] == 1
    conn.close()
