$ErrorActionPreference = "Stop"
go mod tidy
go vet ./...
go test -v ./...
$env:CGO_ENABLED = "0"
$env:GOOS = "windows"; $env:GOARCH = "amd64"; go build -o NUL .
$env:GOOS = "linux";   $env:GOARCH = "amd64"; go build -o NUL .
$env:GOOS = "linux";   $env:GOARCH = "arm64"; go build -o NUL .
Write-Host "全部自检通过，可安全推送至 GitHub！" -ForegroundColor Green
