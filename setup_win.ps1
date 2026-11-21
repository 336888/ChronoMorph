#Requires -RunAsAdministrator
param([switch]$AllowLan)
$ErrorActionPreference = 'Stop'

if ($AllowLan) {
    $ruleName = 'ChronoMorph LAN Proxy'
    if (-not (Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue)) {
        New-NetFirewallRule -DisplayName $ruleName -Direction Inbound -Protocol TCP -LocalPort 1080 -Profile Private -RemoteAddress LocalSubnet -Action Allow | Out-Null
    }
}
Start-Service w32time
w32tm /resync
