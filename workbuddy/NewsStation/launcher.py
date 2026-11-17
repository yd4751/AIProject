"""PyInstaller 入口（放在项目根，`app` 包才能被正常解析）。

打包后的 NewsStation.exe 与源码的 `python -m app.server` 行为完全一致。
真正的启动逻辑在 app/server.py，这里只做一层薄转发。
"""

from app.server import run

if __name__ == "__main__":
    raise SystemExit(run())
