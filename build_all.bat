@echo off
chcp 65001 >nul
setlocal enabledelayedexpansion

echo [1/5] 整理依赖并运行单元测试...
go mod tidy
go test -v ./...
if errorlevel 1 ( pause & exit /b 1 )

for /f "tokens=1-3 delims=/ " %%a in ('date /t') do set VER=v%%a.%%b.%%c
set LDFLAGS=-s -w -X "main.AppVersion=%VER%"

if exist dist rmdir /s /q dist
mkdir dist\windows dist\debian dist\android

echo [2/5] 编译 Windows GUI...
set CGO_ENABLED=0
set GOOS=windows & set GOARCH=amd64
go build -ldflags="%LDFLAGS% -H=windowsgui" -o dist\windows\ChronoMorph.exe .
copy /y setup_win.ps1 dist\windows\ >nul 2>&1
copy /y uninstall_win.ps1 dist\windows\ >nul 2>&1
copy /y client.example.json dist\windows\client.json >nul 2>&1

echo [3/5] 编译 Debian 服务端...
set GOOS=linux & set GOARCH=amd64
go build -ldflags="%LDFLAGS%" -o dist\debian\chronomorph_amd64 .
set GOARCH=arm64
go build -ldflags="%LDFLAGS%" -o dist\debian\chronomorph_arm64 .
copy /y cm.sh dist\debian\ >nul 2>&1

echo [4/5] 编译 Android ARM64...
set GOOS=linux & set GOARCH=arm64
go build -ldflags="%LDFLAGS%" -o dist\android\chronomorph_android_arm64 .

echo [5/5] 打包完成！
dir /s /b dist
pause
