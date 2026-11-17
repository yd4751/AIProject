@echo off
chcp 65001 >nul
REM frptun 多平台构建脚本 (Windows)
setlocal
set OUT=dist
if not exist "%OUT%" mkdir "%OUT%"

echo >>> building windows/amd64
set GOOS=windows& set GOARCH=amd64& set CGO_ENABLED=0
go build -trimpath -ldflags "-s -w" -o %OUT%\frptun-windows-amd64.exe .\cmd\frptun

echo >>> building linux/amd64
set GOOS=linux& set GOARCH=amd64& set CGO_ENABLED=0
go build -trimpath -ldflags "-s -w" -o %OUT%\frptun-linux-amd64 .\cmd\frptun

echo >>> building linux/arm64
set GOOS=linux& set GOARCH=arm64& set CGO_ENABLED=0
go build -trimpath -ldflags "-s -w" -o %OUT%\frptun-linux-arm64 .\cmd\frptun

echo === 构建完成，输出目录: %OUT% ===
dir %OUT%
endlocal
