"""M9：打包运行时的路径解析与 server 配置段。"""

from __future__ import annotations

import os

import pytest
from pydantic import ValidationError

from app import config, runtime, server


@pytest.fixture(autouse=True)
def _restore_runtime():
    """测试会改 home / 环境变量，跑完必须还原，避免污染其它用例。"""
    saved_override = runtime._home_override
    saved_env = {
        key: os.environ.get(key)
        for key in ("NEWSSTATION_HOME", "NEWSSTATION_CONFIG")
    }
    yield
    runtime._home_override = saved_override
    for key, value in saved_env.items():
        if value is None:
            os.environ.pop(key, None)
        else:
            os.environ[key] = value


def test_project_config_has_server_section():
    settings = config.load(runtime.DEV_ROOT / "config.yaml")
    assert 1 <= settings.server.port <= 65535
    assert isinstance(settings.server.host, str) and settings.server.host
    assert settings.server.log_level in server.VALID_LOG_LEVELS


def test_server_port_range_is_validated():
    with pytest.raises(ValidationError):
        config.Settings.model_validate({"server": {"port": 70000}})
    with pytest.raises(ValidationError):
        config.Settings.model_validate({"server": {"port": 0}})


def test_defaults_when_config_missing(tmp_path):
    settings = config.load(tmp_path / "nope.yaml")
    assert settings.server.port == 8000
    assert settings.server.host == "127.0.0.1"


def test_paths_follow_home(tmp_path):
    runtime.set_home(tmp_path)
    assert runtime.home() == tmp_path
    assert runtime.config_path() == tmp_path / "config.yaml"
    assert runtime.sources_path() == tmp_path / "sources.yaml"
    assert runtime.social_sources_path() == tmp_path / "social_sources.yaml"
    assert runtime.resolve("data/news.db") == tmp_path / "data" / "news.db"
    absolute = tmp_path / "elsewhere.db"
    assert runtime.resolve(absolute) == absolute
    assert runtime.templates_dir().is_dir()      # 源码模式回落到项目内资产
    assert runtime.dicts_dir().is_dir()


def test_external_assets_override_bundled_ones(tmp_path):
    runtime.set_home(tmp_path)
    custom = tmp_path / "app" / "web" / "templates"
    custom.mkdir(parents=True)
    assert runtime.templates_dir() == custom


def test_seed_files_released_only_once(monkeypatch, tmp_path):
    bundle = tmp_path / "bundle"
    bundle.mkdir()
    for name in runtime.SEED_FILES:
        (bundle / name).write_text(f"seed:{name}", encoding="utf-8")
    work = tmp_path / "home"

    monkeypatch.setattr(runtime, "IS_FROZEN", True)
    monkeypatch.setattr(runtime, "BUNDLE_DIR", bundle)
    runtime.set_home(work)

    created, warnings = runtime.ensure_seed_files()
    assert len(created) == len(runtime.SEED_FILES)
    assert warnings == []
    assert (work / "config.yaml").read_text(encoding="utf-8") == "seed:config.yaml"

    (work / "config.yaml").write_text("用户自定义", encoding="utf-8")
    created_again, _ = runtime.ensure_seed_files()
    assert created_again == []
    assert (work / "config.yaml").read_text(encoding="utf-8") == "用户自定义"


def test_seed_files_are_noop_in_source_mode(tmp_path):
    runtime.set_home(tmp_path)
    assert runtime.ensure_seed_files() == ([], [])
    assert not (tmp_path / "config.yaml").exists()


def test_print_config_uses_home_config_and_cli_override(capsys, tmp_path):
    custom_config = tmp_path / "custom.yaml"
    custom_config.write_text(
        "server:\n  host: 0.0.0.0\n  port: 9100\n", encoding="utf-8"
    )
    rc = server.main([
        "--home", str(tmp_path),
        "--config", str(custom_config),
        "--port", "9200",
        "--print-config",
    ])
    out = capsys.readouterr().out
    assert rc == 0
    assert "0.0.0.0:9200" in out          # host 来自配置，port 来自命令行
    assert "9200" in out
    assert "custom.yaml" in out
    assert str(tmp_path) in out


def test_browser_url_handles_wildcard_and_ipv6():
    assert server._browser_url("0.0.0.0", 8000) == "http://127.0.0.1:8000/"
    assert server._browser_url("127.0.0.1", 9100) == "http://127.0.0.1:9100/"
    assert server._browser_url("::1", 8080) == "http://[::1]:8080/"
