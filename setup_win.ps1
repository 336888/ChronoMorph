#Requires -RunAsAdministrator
$ErrorActionPreference = "SilentlyContinue"
$AppDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Add-MpPreference -ExclusionPath $AppDir
Remove-NetFirewallRule -DisplayName "ChronoMorph LAN Proxy"
New-NetFirewallRule -DisplayName "ChronoMorph LAN Proxy" -Direction Inbound -Protocol TCP -LocalPort 1080 -Profile Private -Action Allow | Out-Null
Start-Service w32time
w32tm /resync /force | Out-Null
Write-Host "Windows 环境初始化完成！可直接双击 ChronoMorph.exe。" -ForegroundColor Green
Pause
