@echo off
setlocal
rem =====================================================================
rem  SiteDock nginx one-shot installer (Windows, ASCII only)
rem
rem  WHY: a ports-*.conf fragment contains server{} blocks only. nginx
rem  rejects server{} outside http{}, e.g.
rem    "server" directive is not allowed here
rem  This script installs the FULL main config (worker_processes / events /
rem  http / server) as conf\nginx.conf, so include position no longer matters.
rem
rem  Usage:  fix-nginx.cmd [nginx_root]
rem  Default: C:\Users\Data\Server\nginx
rem  Put this .cmd next to nginx-full-7000-7010-cors.conf, or put both
rem  files in the nginx root directory.
rem =====================================================================

set "ROOT=%~1"
if "%ROOT%"=="" set "ROOT=C:\Users\Data\Server\nginx"
set "FULLNAME=nginx-full-7000-7010-cors.conf"
set "SRC=%~dp0%FULLNAME%"
if not exist "%SRC%" set "SRC=%ROOT%\%FULLNAME%"
set "CONF=%ROOT%\conf\nginx.conf"
set "EXE=%ROOT%\nginx.exe"
set "CRT=%ROOT%\cert\selfsigned.crt"
set "KEY=%ROOT%\cert\selfsigned.key"
set "BAK="

echo [1/6] nginx root : %ROOT%
echo       full config: %SRC%

if not exist "%EXE%" (
  echo ERROR: nginx.exe not found: %EXE%
  goto :fail
)
if not exist "%SRC%" (
  echo ERROR: full config not found: %SRC%
  echo        copy nginx-full-7000-7010-cors.conf next to this script.
  goto :fail
)
if not exist "%CRT%" (
  echo ERROR: certificate not found: %CRT%
  goto :fail
)
if not exist "%KEY%" (
  echo ERROR: private key not found: %KEY%
  goto :fail
)
if not exist "%ROOT%\conf" (
  echo ERROR: conf directory missing: %ROOT%\conf
  goto :fail
)
if not exist "%ROOT%\logs" mkdir "%ROOT%\logs" 2>nul

echo [2/6] include lines in the ORIGINAL nginx.conf (for reference):
if exist "%CONF%" (
  findstr /n /i "include" "%CONF%"
) else (
  echo        no existing conf\nginx.conf
)

set "BAK=%CONF%.bak.%RANDOM%"
if exist "%CONF%" (
  copy /y "%CONF%" "%BAK%" >nul
  if errorlevel 1 (
    echo ERROR: backup failed
    goto :fail
  )
  echo [3/6] backup: %BAK%
) else (
  echo [3/6] no existing nginx.conf, backup skipped
  set "BAK="
)

copy /y "%SRC%" "%CONF%" >nul
if errorlevel 1 (
  echo ERROR: failed to install full config
  goto :rollback
)
echo [4/6] installed full config as conf\nginx.conf

pushd "%ROOT%"
"%EXE%" -t -p "%ROOT%"
set "RC=%ERRORLEVEL%"
popd
if not "%RC%"=="0" (
  echo [5/6] nginx -t FAILED, code %RC%. Rolling back...
  goto :rollback
)
echo [5/6] nginx -t OK
echo [6/6] Done.
echo.
echo NEXT (in nginx root):
echo   nginx.exe -s reload      if nginx is already running
echo   start nginx.exe          if it is not running
echo.
if not "%BAK%"=="" echo Original config saved at: %BAK%
echo NOTE: this main config does NOT include myconf\*.conf.
echo       old fragments in myconf are ignored; no need to delete them.
goto :eof

:rollback
if not "%BAK%"=="" if exist "%BAK%" copy /y "%BAK%" "%CONF%" >nul
echo Rolled back. Original config restored, nothing changed.
exit /b 1

:fail
exit /b 1
