#Requires -RunAsAdministrator
$ErrorActionPreference = "SilentlyContinue"
$AppDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Stop-Process -Name "ChronoMorph" -Force
$RegPath = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings"
Set-ItemProperty -Path $RegPath -Name "ProxyEnable" -Value 0
Remove-ItemProperty -Path $RegPath -Name "AutoConfigURL"
Remove-ItemProperty -Path "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run" -Name "ChronoMorph"
Remove-NetFirewallRule -DisplayName "ChronoMorph LAN Proxy"
Remove-MpPreference -ExclusionPath $AppDir
Write-Host "Windows 网络与代理设置已完全还原。" -ForegroundColor Green
Pause
