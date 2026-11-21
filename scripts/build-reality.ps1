# Compile the companion with the upstream module's own toolchain and checksums.
param(
    [Parameter(Mandatory=$true)][string]$OutputDir,
    [string]$SourceDir
)
$ErrorActionPreference = 'Stop'
$pin = '676eaf66f8f05d4d558ee7a74b4736623d3feeb9'
$root = Split-Path $PSScriptRoot -Parent
$output = (New-Item -ItemType Directory -Force $OutputDir).FullName
$temp = $null
try {
    if (!$SourceDir) {
        $temp = Join-Path ([IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString())
        git init --quiet $temp
        if ($LASTEXITCODE) { throw 'Could not initialize the transport source directory' }
        git -C $temp fetch --quiet --depth=1 https://github.com/XTLS/Xray-core.git $pin
        if ($LASTEXITCODE) { throw 'Could not download the pinned Xray-core sources' }
        git -C $temp checkout --quiet --detach FETCH_HEAD
        if ($LASTEXITCODE) { throw 'Could not check out the pinned sources' }
        $SourceDir = $temp
    }
    Push-Location $SourceDir
    try {
        $revision = git rev-parse HEAD
        if ($LASTEXITCODE -or $revision -ne $pin) { throw 'Unexpected Xray-core revision' }
        New-Item -ItemType Directory -Force 'cmbridge/wire' | Out-Null
        Copy-Item (Join-Path $root 'integrations/reality/main.go') cmbridge/
        Copy-Item (Join-Path $root 'internal/bridgeconfig/config.go') cmbridge/wire/
        $name = if ($env:GOOS -eq 'windows') { 'cm-reality.exe' } else { 'cm-reality' }
        go build -mod=readonly -tags cm_xray -trimpath -buildvcs=false '-ldflags=-s -w' -o (Join-Path $output $name) ./cmbridge
        if ($LASTEXITCODE) { throw 'Could not build the REALITY component' }
        $module = go list -mod=readonly -m -f '{{.Dir}}' github.com/xtls/reality
        if ($LASTEXITCODE -or !$module) { throw 'Could not find the pinned REALITY license' }
        Copy-Item LICENSE (Join-Path $output 'Xray-LICENSE.txt')
        Copy-Item (Join-Path $module 'LICENSE') (Join-Path $output 'REALITY-LICENSE.txt')
    } finally { Pop-Location }
} finally {
    if ($temp -and (Test-Path -LiteralPath $temp)) { Remove-Item -Force -Recurse -LiteralPath $temp }
}
