param()

$ErrorActionPreference = 'Stop'

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = Split-Path -Parent $scriptDir
Set-Location -LiteralPath $repoRoot

$env:GOCACHE = Join-Path $repoRoot '.gocache'

Write-Host 'Generating NyaRelay update signing keys...'
Write-Host ''
go run ./cmd/update-keygen
Write-Host ''
Write-Host 'Copy the two values above into GitHub repository Secrets.'
Write-Host 'Do not use SSH public/private keys for these secrets.'
