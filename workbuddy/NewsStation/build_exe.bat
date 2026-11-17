@echo off
setlocal
REM 一键打包为 Windows exe。默认单文件；设 NS_ONEDIR=1 可生成目录版（启动更快）。
cd /d "%~dp0"
set "PY=.venv\Scripts\python.exe"

if not exist "%PY%" (
  echo [错误] 找不到 %PY%，请先准备虚拟环境并安装依赖（或先运行一次 run.bat）。
  goto :error
)

"%PY%" -c "import PyInstaller" >nul 2>nul
if errorlevel 1 (
  echo [1/2] 安装 PyInstaller...
  "%PY%" -m pip install "pyinstaller>=6.10" || goto :error
)

echo [2/2] 正在打包（首次约 1-3 分钟）...
"%PY%" -m PyInstaller --noconfirm --clean newsstation.spec || goto :error

if "%NS_ONEDIR%"=="1" (set "OUTDIR=dist\NewsStation") else (set "OUTDIR=dist")
REM 把可编辑配置放到 exe 同目录；已有文件不覆盖，避免重新构建时冲掉用户改过的端口/源
if not exist "%OUTDIR%\config.yaml" copy /Y config.yaml "%OUTDIR%\config.yaml" >nul
if not exist "%OUTDIR%\sources.yaml" copy /Y sources.yaml "%OUTDIR%\sources.yaml" >nul
if not exist "%OUTDIR%\social_sources.yaml" copy /Y social_sources.yaml "%OUTDIR%\social_sources.yaml" >nul
REM 始终输出一份最新默认配置模板：程序升级新增配置段时，用它对比自己的 config.yaml
copy /Y config.yaml "%OUTDIR%\config.example.yaml" >nul

echo.
echo ============================================================
echo  打包完成，输出目录：%OUTDIR%
echo  可执行文件：%OUTDIR%\NewsStation.exe
echo.
echo  改端口/监听地址：编辑 %OUTDIR%\config.yaml 的 server 段后重启
echo  排障：NewsStation.exe --print-config
echo ============================================================
goto :eof

:error
echo.
echo 打包失败，请检查上面的错误信息。
pause
exit /b 1
