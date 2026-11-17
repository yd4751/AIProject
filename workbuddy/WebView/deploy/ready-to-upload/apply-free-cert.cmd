@echo off
setlocal
rem =====================================================================
rem  Apply the Tencent Cloud free DV certificate (TrustAsia) to nginx.
rem
rem  WHY: nginx-full-7000-7010-cors.conf points all 11 server blocks at
rem       cert\selfsigned.crt / cert\selfsigned.key. Overwriting those two
rem       files swaps every 7000~7010 port to the CA-signed cert at once,
rem       WITHOUT editing nginx.conf and WITHOUT Node.js on the server.
rem
rem  Usage: apply-free-cert.cmd [cert_dir] [nginx_root]
rem    cert_dir    folder containing bighat.icu_bundle.crt + bighat.icu.key
rem                (default: this script's folder)
rem    nginx_root  default: C:\Users\Data\Server\nginx
rem
rem  Put bighat.icu_bundle.crt and bighat.icu.key next to this script,
rem  copy all three files to the server, then run as Administrator.
rem =====================================================================

set "SRCDIR=%~1"
if "%SRCDIR%"=="" set "SRCDIR=%~dp0"
if "%SRCDIR:~-1%"=="\" set "SRCDIR=%SRCDIR:~0,-1%"
set "ROOT=%~2"
if "%ROOT%"=="" set "ROOT=C:\Users\Data\Server\nginx"
if "%ROOT:~-1%"=="\" set "ROOT=%ROOT:~0,-1%"

set "CRT=%SRCDIR%\bighat.icu_bundle.crt"
set "KEY=%SRCDIR%\bighat.icu.key"
set "DEST=%ROOT%\cert"
set "EXE=%ROOT%\nginx.exe"
set "OLDCRT=%DEST%\selfsigned.crt"
set "OLDKEY=%DEST%\selfsigned.key"

echo [1/5] cert source : %SRCDIR%
echo       nginx root : %ROOT%

if not exist "%CRT%" (
  echo ERROR: missing %CRT%
  goto :fail
)
if not exist "%KEY%" (
  echo ERROR: missing %KEY%
  goto :fail
)
if not exist "%DEST%" (
  echo ERROR: missing cert dir %DEST%
  goto :fail
)

set "B1=%OLDCRT%.bak.%RANDOM%"
set "B2=%OLDKEY%.bak.%RANDOM%"
if exist "%OLDCRT%" copy /y "%OLDCRT%" "%B1%" >nul
if exist "%OLDKEY%" copy /y "%OLDKEY%" "%B2%" >nul
echo [2/5] old self-signed cert backed up

copy /y "%CRT%" "%OLDCRT%" >nul
if errorlevel 1 goto :rollback
copy /y "%KEY%" "%OLDKEY%" >nul
if errorlevel 1 goto :rollback
echo [3/5] CA-signed cert installed; nginx.conf unchanged

if not exist "%EXE%" (
  echo [4/5] WARNING: nginx.exe not found here, skipped nginx -t
  goto :done
)
pushd "%ROOT%"
"%EXE%" -t -p "%ROOT%"
set "RC=%ERRORLEVEL%"
popd
if not "%RC%"=="0" (
  echo [4/5] nginx -t FAILED, rolling back
  goto :rollback
)
echo [4/5] nginx -t OK

:done
echo [5/5] Done. Reload manually:
echo       cd /d %ROOT%
echo       nginx.exe -s reload
echo.
echo Verify after reload:
echo       openssl s_client -connect bighat.icu:7000 -servername bighat.icu -showcerts
echo       expect: Verify return code: 0 ^(ok^)
echo       issuer: TrustAsia DV TLS RSA CA 2024
echo.
echo NOTE: this free cert expires 2027-02-04, re-apply before it lapses.
echo       ICP filing is still required for real devices / release builds.
if exist "%B1%" echo Backup cert: %B1%
if exist "%B2%" echo Backup key : %B2%
goto :eof

:rollback
if exist "%B1%" copy /y "%B1%" "%OLDCRT%" >nul
if exist "%B2%" copy /y "%B2%" "%OLDKEY%" >nul
echo Rolled back: original self-signed cert restored, nothing changed.
exit /b 1

:fail
exit /b 1
