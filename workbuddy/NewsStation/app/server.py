"""统一启动入口：源码模式与 exe 模式都从这里起服务。

    .venv\\Scripts\\python.exe -m app.server
    NewsStation.exe
    NewsStation.exe --port 8080 --config D:\\ns\\config.yaml

监听地址、端口、日志级别、是否自动开浏览器等全部来自 config.yaml 的 `server` 段；
命令行同名参数只做临时覆盖，不写回配置文件。
"""

from __future__ import annotations

import argparse
import multiprocessing
import os
import sys
import threading
import webbrowser
from pathlib import Path

VALID_LOG_LEVELS = {"critical", "error", "warning", "info", "debug", "trace"}
DEFAULT_LOG_LEVEL = "info"


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        prog="NewsStation",
        description="全球新闻捕获站点：运行参数默认读 config.yaml 的 server 段",
    )
    parser.add_argument("--config", help="配置文件路径（默认 <运行目录>/config.yaml）")
    parser.add_argument("--home", help="运行目录（默认 exe 同目录 / 源码模式为项目根）")
    parser.add_argument("--host", help="临时覆盖 server.host")
    parser.add_argument("--port", type=int, help="临时覆盖 server.port")
    parser.add_argument("--no-browser", action="store_true", help="本次不自动打开浏览器")
    parser.add_argument(
        "--print-config", action="store_true",
        help="只打印生效的运行参数与文件路径，不启动服务（排障用）",
    )
    parser.add_argument(
        "--self-check", action="store_true",
        help="检查内嵌资源与配置是否可用（模板/词典/分词/源清单），不启动服务",
    )
    return parser.parse_args(argv)


def _browser_url(host: str, port: int) -> str:
    shown = "127.0.0.1" if host in ("0.0.0.0", "::", "") else host
    if ":" in shown and not shown.startswith("["):   # IPv6
        shown = f"[{shown}]"
    return f"http://{shown}:{port}/"


def _open_later(url: str) -> None:
    def go() -> None:
        try:
            webbrowser.open(url)
        except Exception as exc:                     # 无图形环境时不影响服务
            print(f"[提示] 自动打开浏览器失败：{exc}", flush=True)

    timer = threading.Timer(1.2, go)
    timer.daemon = True
    timer.start()


def _banner(settings, config_file: Path, created: list[Path], warnings: list[str], overridden: list[str]) -> str:
    sv = settings.server
    lines = [
        "=" * 62,
        "  NewsStation · 全球新闻捕获站点",
        "=" * 62,
        f"  运行方式   : {'EXE 打包' if _is_frozen() else '源码'}",
        f"  配置文件   : {config_file}",
        f"  运行目录   : {_home()}",
        f"  数据库     : {_db(settings)}",
        f"  监听地址   : http://{sv.host}:{sv.port}",
        f"  日志级别   : {sv.log_level}  · 访问日志 {'开' if sv.access_log else '关'}",
    ]
    if overridden:
        lines.append(f"  命令行覆盖 : {', '.join(overridden)}（未写回配置文件）")
    if created:
        lines.append("  首次启动已释放可编辑配置：")
        lines += [f"    - {p}" for p in created]
    if warnings:
        lines += [f"  [警告] {w}" for w in warnings]
    lines += ["=" * 62, "  Ctrl+C 停止服务", ""]
    return "\n".join(lines)


# 这三个小函数延迟导入，避免模块级就固定住运行目录
def _is_frozen() -> bool:
    from app import runtime
    return runtime.IS_FROZEN


def _home() -> Path:
    from app import runtime
    return runtime.home()


def _db(settings) -> Path:
    from app import config
    return config.abs_db_path(settings)


def self_check(settings, config_file: Path) -> int:
    """打包自检：真正去加载模板、词典、分词表与源清单，而不只是看路径存在。"""
    from app import config, runtime

    results: list[tuple[str, bool, str]] = []

    def check(name: str, fn) -> None:
        try:
            results.append((name, True, str(fn())))
        except Exception as exc:
            results.append((name, False, f"{type(exc).__name__}: {exc}"))

    def check_config():
        if not config_file.is_file():
            raise FileNotFoundError(config_file)
        return f"{config_file.stat().st_size} 字节，端口 {settings.server.port}"

    def check_sources():
        from app.ingest import registry, social
        return f"媒体 {registry.load_sources_yaml()} 源 / 社媒 {social.load_social_yaml()} 源"

    def check_tagger():
        from app.analyze.tagger import get_tagger
        return f"{len(get_tagger().labels)} 个标签面"

    def check_jieba():
        import jieba
        return "/".join(jieba.lcut("全球新闻热点与行业趋势分析"))

    def check_templates():
        files = sorted(runtime.templates_dir().glob("*.html"))
        if not files:
            raise FileNotFoundError(runtime.templates_dir())
        return f"{len(files)} 个模板"

    def check_static():
        files = list(runtime.static_dir().iterdir())
        if not files:
            raise FileNotFoundError(runtime.static_dir())
        return f"{len(files)} 个文件：{', '.join(p.name for p in files)}"

    def check_engines():
        from app.search import engines
        rows = engines.available(settings)
        return " / ".join(f"{e['label']}（{len(e['providers'])} 源）" for e in rows)

    def check_db():
        from app import db
        path = config.abs_db_path(settings)
        db.init_db(path)
        return f"{path} 可读写"

    check("配置文件", check_config)
    check("源清单", check_sources)
    check("标签词典", check_tagger)
    check("中文分词表", check_jieba)
    check("页面模板", check_templates)
    check("静态资源", check_static)
    check("搜索引擎", check_engines)
    check("数据库", check_db)

    print("=" * 62)
    print(f"  NewsStation 自检 · {'EXE 打包' if runtime.IS_FROZEN else '源码'} · {runtime.home()}")
    print("=" * 62)
    for name, ok, detail in results:
        print(f"  [{'OK' if ok else 'FAIL'}] {name:<12} {detail}")
    failed = [n for n, ok, _ in results if not ok]
    print("=" * 62)
    print(f"  结果：{len(results) - len(failed)}/{len(results)} 项通过")
    if failed:
        print(f"  失败项：{', '.join(failed)}")
    return 1 if failed else 0


def main(argv: list[str] | None = None) -> int:
    # Windows 上 PyInstaller 必须调用，否则多进程/某些库会反复自启
    multiprocessing.freeze_support()

    args = parse_args(argv)
    if args.home:
        os.environ["NEWSSTATION_HOME"] = str(Path(args.home).expanduser().resolve())
    if args.config:
        os.environ["NEWSSTATION_CONFIG"] = str(Path(args.config).expanduser().resolve())

    from app import config, runtime

    created, warnings = runtime.ensure_seed_files()
    config_file = runtime.config_path()
    settings = config.load(config_file)

    overridden: list[str] = []
    if args.host:
        settings.server.host = args.host
        overridden.append(f"host={args.host}")
    if args.port is not None:
        settings.server.port = args.port
        overridden.append(f"port={args.port}")
    if args.no_browser and settings.server.auto_open_browser:
        settings.server.auto_open_browser = False
        overridden.append("auto_open_browser=false")

    if settings.server.log_level not in VALID_LOG_LEVELS:
        warnings.append(
            f"server.log_level={settings.server.log_level!r} 无效，回退 {DEFAULT_LOG_LEVEL!r}"
        )
        settings.server.log_level = DEFAULT_LOG_LEVEL

    if args.print_config:
        print(_banner(settings, config_file, created, warnings, overridden))
        for key, value in runtime.describe().items():
            print(f"  {key:<16}= {value}")
        return 0

    if args.self_check:
        return self_check(settings, config_file)

    print(_banner(settings, config_file, created, warnings, overridden), flush=True)

    sv = settings.server
    url = _browser_url(sv.host, sv.port)
    if sv.auto_open_browser:
        _open_later(url)

    import uvicorn
    from app.main import app

    common = dict(
        host=sv.host,
        port=sv.port,
        log_level=sv.log_level,
        access_log=sv.access_log,
        proxy_headers=sv.proxy_headers,
        forwarded_allow_ips=sv.forwarded_allow_ips,
    )
    # 单文件 exe 不支持 reload / workers>1：冻结态一律用内嵌 app 对象直接跑
    use_runner = not runtime.IS_FROZEN and (sv.reload or sv.workers > 1)
    if use_runner:
        print(
            "[提示] 源码模式的 reload / workers 由 uvicorn 子进程托管；"
            "打包成 exe 后自动忽略这两项。",
            flush=True,
        )
        uvicorn.run(
            "app.main:app", reload=sv.reload, workers=sv.workers, **common
        )
    else:
        uvicorn.Server(uvicorn.Config(app, **common)).run()
    return 0


def run(argv: list[str] | None = None) -> int:
    """给 launcher.py / `__main__` 共用：双击 exe 时把异常留在屏幕上。"""
    try:
        return main(argv)
    except SystemExit:
        raise
    except BaseException as exc:
        import traceback
        traceback.print_exc()
        if getattr(sys, "frozen", False) and sys.stdin and sys.stdin.isatty():
            input(f"\n启动失败：{exc}\n按回车键退出...")
        return 1


if __name__ == "__main__":
    raise SystemExit(run())
