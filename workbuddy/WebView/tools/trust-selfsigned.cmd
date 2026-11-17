@echo off
setlocal
chcp 65001 >nul 2>&1

set "SERVER_CERT=C:\Users\Data\Server\nginx\cert\selfsigned.crt"
set "LOCAL_CERT=%~dp0..\deploy\nginx\cert\selfsigned.crt"

echo ============================================================
echo  把自签证书加入本机受信任根证书（仅影响这台电脑）
echo  用途：让 WorkBuddy 预览 / 本机浏览器直接访问
echo        https://bighat.icu:7000 时不再报证书错误
echo ============================================================
echo.

net session >nul 2>&1
if errorlevel 1 (
  echo [!] 需要管理员权限。请右键本文件，选择「以管理员身份运行」。
  echo.
  pause
  exit /b 1
)

set "CERT_FILE="
if exist "%SERVER_CERT%" set "CERT_FILE=%SERVER_CERT%"
if not defined CERT_FILE if exist "%LOCAL_CERT%" set "CERT_FILE=%LOCAL_CERT%"
if not defined CERT_FILE (
  echo [!] 找不到 selfsigned.crt。请先运行：
  echo     node tools/gen-selfsigned-cert.js --domain bighat.icu
  echo.
  pause
  exit /b 1
)

echo 即将导入：%CERT_FILE%
echo 注意：只导入你自己生成的这张证书，不要导入来路不明的根证书。
echo.
choice /C YN /M "确认导入? "
if errorlevel 2 (
  echo 已取消。
  pause
  exit /b 0
)

certutil -addstore -f "Root" "%CERT_FILE%" >nul
if errorlevel 1 (
  echo [!] 导入失败，请确认证书文件有效且以管理员身份运行。
  pause
  exit /b 1
)

echo.
echo [OK] 已导入「受信任的根证书颁发机构」。
echo.
echo 接下来：
echo   1. 完全关闭并重新打开 WorkBuddy 预览面板（或重启浏览器）
echo   2. 刷新页面，nginx 已配置 CORS，此时应能实时拉取 7000 端口内容
echo.
echo 局限：只对这台电脑的浏览器/预览生效；手机真机、体验版、正式版不受影响。
echo 撤销：certutil -delstore "Root" "bighat.icu"
echo.
pause
