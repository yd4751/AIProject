# -*- mode: python ; coding: utf-8 -*-
"""PyInstaller 打包描述文件。

默认生成单文件 dist/NewsStation.exe；设置环境变量 NS_ONEDIR=1 则生成
dist/NewsStation/ 目录版（启动更快、便于替换模板，但文件较多）。

首次运行 exe 时，app/runtime.py 会把 config.yaml / sources.yaml /
social_sources.yaml 从内嵌资源释放到 exe 同目录，之后只认外部那一份，
改端口、改媒体源都不需要重新打包。
"""

import os
from pathlib import Path

from PyInstaller.utils.hooks import collect_data_files, collect_submodules

ROOT = Path(SPECPATH).resolve()
ONEDIR = os.environ.get("NS_ONEDIR") == "1"

# ---- 随包携带的数据文件 ----
datas = [
    (str(ROOT / "app/web/templates"), "app/web/templates"),
    (str(ROOT / "app/web/static"), "app/web/static"),
    (str(ROOT / "app/analyze/dicts"), "app/analyze/dicts"),
    # 三份「种子配置」：只用于首次启动释放，不直接读它
    (str(ROOT / "config.yaml"), "."),
    (str(ROOT / "sources.yaml"), "."),
    (str(ROOT / "social_sources.yaml"), "."),
]
datas += collect_data_files("jieba")        # dict.txt / idf.txt / stop_words.txt
datas += collect_data_files("certifi")      # HTTPS 根证书，缺了 httpx 会全线报证书错误

# ---- 动态导入兜底 ----
hiddenimports = [
    "app", "app.main", "app.server", "app.runtime",
    "uvicorn.lifespan.on",
    "uvicorn.protocols.http.auto",
    "uvicorn.protocols.http.httptools_impl",
    "uvicorn.protocols.http.h11_impl",
    "uvicorn.protocols.websockets.auto",
    "uvicorn.loops.auto",
    "uvicorn.loops.asyncio",
    "httptools", "h11", "websockets", "watchfiles",
    "apscheduler.schedulers.asyncio",
    "apscheduler.executors.asyncio",
    "ahocorasick", "jieba", "feedparser", "certifi",
]
hiddenimports += collect_submodules("app")
hiddenimports += collect_submodules("uvicorn")

a = Analysis(
    [str(ROOT / "launcher.py")],
    pathex=[str(ROOT)],
    binaries=[],
    datas=datas,
    hiddenimports=hiddenimports,
    hookspath=[],
    hooksconfig={},
    runtime_hooks=[],
    excludes=[
        "torch", "hanlp", "tkinter", "matplotlib", "pytest",
        # 站点不做数值计算：这些是 venv 里其它实验留下的，被间接分析进来纯属浪费体积
        "numpy", "pandas", "scipy",
    ],
    noarchive=False,
    optimize=0,
)

# hook-jieba 会把 lac_small 模型（约 10 MB）一并收进来，本项目只用 jieba.lcut，
# 连同 numpy 残留数据一起剔除；dict.txt / idf.txt 等分词必需文件保留。
def _lean(entry) -> bool:
    dest = entry[0].replace("\\", "/")
    return not (
        dest.startswith("jieba/lac_small/")
        or dest.startswith("numpy")
        or dest.startswith("numpy.libs")
    )


a.datas = [e for e in a.datas if _lean(e)]
a.binaries = [e for e in a.binaries if _lean(e)]

pyz = PYZ(a.pure)

if ONEDIR:
    exe = EXE(
        pyz, a.scripts, [],
        exclude_binaries=True,
        name="NewsStation",
        debug=False,
        bootloader_ignore_signals=False,
        strip=False,
        upx=False,
        console=True,
        disable_windowed_traceback=False,
        argv_emulation=False,
        target_arch=None,
        codesign_identity=None,
        entitlements_file=None,
    )
    coll = COLLECT(
        exe, a.binaries, a.zipfiles, a.datas,
        strip=False,
        upx=False,
        upx_exclude=[],
        name="NewsStation",
    )
else:
    exe = EXE(
        pyz,
        a.scripts,
        a.binaries,
        a.zipfiles,
        a.datas,
        [],
        name="NewsStation",
        debug=False,
        bootloader_ignore_signals=False,
        strip=False,
        upx=False,                 # UPX 压缩易被杀软误报，且启动更慢
        upx_exclude=[],
        runtime_tmpdir=None,
        console=True,              # 服务端程序保留控制台，方便看日志/报错
        disable_windowed_traceback=False,
        argv_emulation=False,
        target_arch=None,
        codesign_identity=None,
        entitlements_file=None,
    )
