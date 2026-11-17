"""M10 修正：引擎选择必须出现在「搜索栏」本身，而不只是搜索页下方的一行标签。

覆盖顶栏搜索框、搜索页主输入框、参数透传、配置禁用后的降级，
以及缺上下文页面（阅读页等）不会因为新变量而渲染失败。
"""

from __future__ import annotations

import re

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from app import config, db, runtime
from app.web.routes import router, templates


@pytest.fixture
def env(tmp_path):
    db_path = tmp_path / "picker.db"
    db.init_db(db_path)
    settings = config.Settings(db_path=str(db_path))
    settings.search.external_enabled = False   # 页面测试一律不发真实外网请求
    settings.search.providers = ["bing_rss"]
    app = FastAPI()
    app.state.settings = settings
    app.include_router(router)
    return settings, TestClient(app)


def _topbar_form(html: str) -> str:
    m = re.search(r'<form class="topsearch".*?</form>', html, re.S)
    assert m, "顶栏搜索表单未渲染"
    return m.group(0)


def _selected_values(html: str) -> list[str]:
    return re.findall(r'<option value="([^"]+)"[^>]*\sselected', html)


# ---------------------------------------------------------------- 顶栏搜索框

def test_topbar_search_bar_has_engine_dropdown_on_all_pages(env):
    _, cl = env
    for path in ("/", "/industries", "/social", "/search"):
        r = cl.get(path, params={"scope": "local"})
        assert r.status_code == 200, path
        form = _topbar_form(r.text)
        assert 'name="engines"' in form, path
        assert 'class="ts-engine"' in form, path
        assert 'data-engine-choice' in form, path
        # 下拉只有「默认引擎 / 谷歌引擎」两项，不常驻「全部引擎」
        assert re.findall(r'<option value="([^"]+)"', form) == ["default", "google"], path
        assert 'name="q"' in form and 'action="/search"' in form
        # 三段顺序：下拉 → 输入框 → 按钮
        assert form.index("<select") < form.index('name="q"') < form.index("<button")


def test_topbar_dropdown_reflects_url_selection(env):
    _, cl = env
    r = cl.get("/?engines=google")
    assert set(_selected_values(_topbar_form(r.text))) == {"google"}
    r = cl.get("/social?engines=default,google")
    assert set(_selected_values(_topbar_form(r.text))) == {"default,google"}
    r = cl.get("/")
    assert set(_selected_values(_topbar_form(r.text))) == {"default"}


def test_topbar_accepts_legacy_single_engine_param(env):
    _, cl = env
    r = cl.get("/?engine=google")
    form = _topbar_form(r.text)
    assert '<option value="google" title=' in form
    assert "google" in _selected_values(form)


def test_all_param_normalizes_to_both_engines(env):
    _, cl = env
    r = cl.get("/search", params={"q": "AI", "scope": "local", "engines": "all"})
    assert r.status_code == 200
    assert set(_selected_values(r.text)) == {"default,google"}
    form = re.search(r'<form class="sch-form".*?</form>', r.text, re.S).group(0)
    # 组合状态才临时补一条「全部引擎」，平时下拉仍是两项
    assert re.findall(r'<option value="([^"]+)"', form) == [
        "default", "google", "default,google"]
    assert "全部引擎" in form


# ---------------------------------------------------------------- 搜索页主输入框

def test_search_main_form_has_engine_select(env):
    _, cl = env
    r = cl.get("/search", params={"q": "AI", "scope": "local"})
    assert r.status_code == 200
    m = re.search(r'<form class="sch-form".*?</form>', r.text, re.S)
    assert m, "搜索页主表单未渲染"
    form = m.group(0)
    assert 'id="sch-engine"' in form
    assert 'class="sch-engine-select" name="engines"' in form
    assert 'data-engine-choice' in form
    assert 'name="q"' in form
    assert 'name="scope" value="local"' in form
    assert "<label" not in form                       # 三段式，不要多一段文字标签
    assert re.search(r'<form class="sch-form"[^>]*>\s*<select', form, re.S)
    assert form.index("<select") < form.index('name="q"') < form.index("<button")
    assert re.findall(r'<option value="([^"]+)"', form) == ["default", "google"]
    assert '<input type="hidden" name="engines"' not in form
    assert set(_selected_values(form)) == {"default"}


def test_search_form_engine_value_changes_resulting_blocks(env):
    _, cl = env
    r = cl.get("/search", params={"q": "AI", "scope": "local", "engines": "google"})
    assert r.status_code == 200
    form = re.search(r'<form class="sch-form".*?</form>', r.text, re.S).group(0)
    assert set(_selected_values(form)) == {"google"}
    assert 'id="engine-google"' in r.text
    assert 'id="engine-default"' not in r.text   # 下拉选择真的生效，不是摆设


# ---------------------------------------------------------------- 配置降级与兼容

def test_single_engine_config_hides_both_dropdowns(env):
    settings, cl = env
    settings.search.engines = ["default"]
    r = cl.get("/search", params={"q": "AI", "scope": "local"})
    assert r.status_code == 200
    assert 'class="ts-engine"' not in _topbar_form(r.text)
    assert 'id="sch-engine"' not in r.text
    assert 'type="hidden" name="engines" value="default"' in r.text


def test_disabled_google_hides_both_dropdowns(env):
    settings, cl = env
    settings.search.google.enabled = False
    r = cl.get("/search", params={"q": "AI", "scope": "local"})
    assert r.status_code == 200
    assert 'class="ts-engine"' not in _topbar_form(r.text)
    assert 'id="sch-engine"' not in r.text
    assert 'value="google"' not in r.text
    assert 'type="hidden" name="engines" value="default"' in r.text


def test_base_template_renders_without_site_context():
    """阅读页等只渲染 base 骨架的场合，新变量缺省也不能抛异常。"""
    html = templates.env.get_template("base.html").render()
    assert 'class="topsearch"' in html
    assert 'class="ts-engine"' not in html


def test_static_assets_contain_picker_styles_and_memory():
    from app import runtime

    css = (runtime.static_dir() / "app.css").read_text(encoding="utf-8")
    js = (runtime.static_dir() / "app.js").read_text(encoding="utf-8")
    assert ".topsearch select.ts-engine" in css
    assert ".sch-form select.sch-engine-select" in css
    assert ".sch-form { display: flex; gap: 0; }" in css      # 三段连成一条
    assert "appearance: none" in css                          # 明显的下拉箭头
    assert ".topsearch { display: none; }" not in css      # 窄屏也不隐藏搜索栏
    assert "flex-wrap: wrap; height: auto" in css
    assert "ns_search_engines" in js
    assert "initSearchEngineMemory()" in js


def test_base_template_versions_assets_and_shows_build_tag():
    """防止旧进程/浏览器缓存页面：CSS/JS 带版本号，页脚有构建标记。"""
    html = (runtime.templates_dir() / "base.html").read_text(encoding="utf-8")
    assert "/static/app.css?v=" in html
    assert "/static/app.js?v=" in html
    assert "页面构建" in html
    assert "服务未重启" in html
