@echo off
REM NewsStation 启动脚本。注意：本机 py 启动器指向已损坏的 E:\install\py3，一律用 venv 内的解释器。
REM 监听地址/端口等参数不再写死在这里，统一读 config.yaml 的 server 段（见 app/server.py）。
cd /d "%~dp0"

if not exist ".venv\Scripts\python.exe" (
  echo [1/2] 创建虚拟环境...
  python -m venv .venv || goto :error
  echo [2/2] 安装依赖...
  ".venv\Scripts\python.exe" -m pip install -q -e . || goto :error
)

REM 支持透传参数，例如：run.bat --port 8080 --print-config
".venv\Scripts\python.exe" -m app.server %*
if errorlevel 1 goto :error
goto :eof

:error
echo 启动失败，请检查上面的错误信息（可先运行 run.bat --print-config 查看生效配置）。
pause
