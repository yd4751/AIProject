"""运行时路径解析：开发态与 PyInstaller 冻结态共用一套规则。

设计目标：**所有可编辑的东西都放在「运行目录」，所有代码资产都在「程序目录」**。

- 运行目录 `home()`：开发态是项目根；打包态默认 exe 同目录（只读时退回
  `%APPDATA%/NewsStation`）。config.yaml / sources.yaml / social_sources.yaml /
  data/news.db 都在这里，用户可直接改，改完重启生效。
- 程序目录 `BUNDLE_DIR`：开发态仍是项目根；打包态是 PyInstaller 解压出的
  临时目录，里面放模板、静态文件、标签词典、以及三份「种子配置」。
  首次启动会把种子配置复制到运行目录，之后只认运行目录里那一份。

可用环境变量 / CLI 覆盖：
- `NEWSSTATION_HOME`      指定运行目录（`--home`）
- `NEWSSTATION_CONFIG`    指定配置文件（`--config`）
"""

from __future__ import annotations

import os
import shutil
import sys
from pathlib import Path

IS_FROZEN = bool(getattr(sys, "frozen", False))
DEV_ROOT = Path(__file__).resolve().parent.parent
BUNDLE_DIR = Path(getattr(sys, "_MEIPASS", DEV_ROOT)).resolve()

SEED_FILES = ("config.yaml", "sources.yaml", "social_sources.yaml")

_home_override: Path | None = None


def _writable(directory: Path) -> bool:
    """exe 可能被放在 Program Files 等只读位置，先探测再决定配置写哪。"""
    try:
        probe = directory / f".newsstation-{os.getpid()}.tmp"
        probe.write_text("ok", encoding="utf-8")
        probe.unlink()
        return True
    except OSError:
        return False


def home() -> Path:
    """可写运行目录：配置、源清单、数据库都相对它解析。"""
    global _home_override
    if _home_override is not None:
        return _home_override
    env = os.environ.get("NEWSSTATION_HOME")
    if env:
        p = Path(env).expanduser().resolve()
        p.mkdir(parents=True, exist_ok=True)
        _home_override = p
        return p
    if not IS_FROZEN:
        return DEV_ROOT
    exe_dir = Path(sys.executable).resolve().parent
    if _writable(exe_dir):
        return exe_dir
    base = os.environ.get("APPDATA") or str(Path.home())
    p = (Path(base) / "NewsStation").resolve()
    p.mkdir(parents=True, exist_ok=True)
    return p


def set_home(path: str | Path) -> Path:
    """CLI 指定运行目录（必须在导入 app.main / app.config 之前调用）。"""
    global _home_override
    p = Path(path).expanduser().resolve()
    p.mkdir(parents=True, exist_ok=True)
    _home_override = p
    return p


def resolve(path: str | Path) -> Path:
    """相对路径按运行目录解析；绝对路径原样返回。db_path 用它。"""
    p = Path(path)
    return p if p.is_absolute() else home() / p


def config_path() -> Path:
    env = os.environ.get("NEWSSTATION_CONFIG")
    return Path(env).expanduser().resolve() if env else home() / "config.yaml"


def sources_path() -> Path:
    return home() / "sources.yaml"


def social_sources_path() -> Path:
    return home() / "social_sources.yaml"


def _asset_dir(rel: str) -> Path:
    """运行目录里有同名目录就优先用（便于改模板/词典），否则用包内资产。"""
    external = home() / rel
    return external if external.is_dir() else BUNDLE_DIR / rel


def templates_dir() -> Path:
    return _asset_dir("app/web/templates")


def static_dir() -> Path:
    return _asset_dir("app/web/static")


def dicts_dir() -> Path:
    return _asset_dir("app/analyze/dicts")


def ensure_seed_files() -> tuple[list[Path], list[str]]:
    """打包态首次启动：把内嵌的默认配置释放到运行目录（已存在则不动）。

    返回 (新建的文件, 警告)。开发态是 no-op，源码目录本来就有这些文件。
    """
    if not IS_FROZEN:
        return [], []
    created: list[Path] = []
    warnings: list[str] = []
    for name in SEED_FILES:
        target = home() / name
        if target.exists():
            continue
        src = BUNDLE_DIR / name
        if not src.exists():
            warnings.append(f"内嵌默认配置缺失：{name}")
            continue
        try:
            shutil.copy2(src, target)
            created.append(target)
        except OSError as exc:
            warnings.append(f"无法写出 {target}：{exc}")
    return created, warnings


def describe() -> dict[str, str]:
    """启动横幅 / 排障用：把关键路径一次列清楚。"""
    return {
        "mode": "exe" if IS_FROZEN else "source",
        "home": str(home()),
        "bundle": str(BUNDLE_DIR),
        "config": str(config_path()),
        "sources": str(sources_path()),
        "social_sources": str(social_sources_path()),
        "templates": str(templates_dir()),
        "static": str(static_dir()),
        "dicts": str(dicts_dir()),
    }
