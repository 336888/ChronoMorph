#Requires -RunAsAdministrator
$ErrorActionPreference = 'Stop'
$names = @('Slacker-windows-amd64.exe', 'Slacker.exe', 'ChronoMorph-windows-amd64.exe', 'ChronoMorph.exe')
$files = @(foreach ($name in $names) {
    $path = Join-Path $PSScriptRoot $name
    if (Test-Path -LiteralPath $path -PathType Leaf) { (Get-Item -LiteralPath $path).FullName }
})
if (-not $files.Count) { throw 'Place this script next to the Slacker executable.' }
Get-Process -Name 'Slacker', 'Slacker-windows-amd64', 'ChronoMorph', 'ChronoMorph-windows-amd64' -ErrorAction SilentlyContinue |
    Where-Object { $files -contains $_.Path } |
    ForEach-Object { Stop-Process -Id $_.Id -Force; $_.WaitForExit() }

$restored = Start-Process -FilePath $files[0] -ArgumentList 'restore-proxy' -PassThru -Wait
if ($restored.ExitCode -ne 0) { throw 'Could not restore the recorded proxy settings.' }
$runKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
$entry = Get-ItemPropertyValue -Path $runKey -Name 'ChronoMorph' -ErrorAction SilentlyContinue
if ($entry -and $files -contains $entry.Trim('"')) { Remove-ItemProperty -Path $runKey -Name 'ChronoMorph' }
Get-NetFirewallRule -DisplayName 'ChronoMorph LAN Proxy' -ErrorAction SilentlyContinue | Remove-NetFirewallRule
Write-Host 'Slacker network cleanup complete.'
