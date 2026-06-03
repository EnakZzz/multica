$ErrorActionPreference = "Stop"

$ExtensionRoot = Split-Path -Parent $PSScriptRoot
$DistDir = Join-Path $ExtensionRoot "dist"
$ManifestPath = Join-Path $DistDir "manifest.json"
$ReleaseDir = Join-Path $ExtensionRoot "releases"

if (!(Test-Path -LiteralPath $ManifestPath)) {
  throw "Missing dist manifest. Run pnpm --filter @multica/browser-extension build first."
}

$manifest = Get-Content -LiteralPath $ManifestPath -Raw | ConvertFrom-Json
$version = [string]$manifest.version
if ([string]::IsNullOrWhiteSpace($version)) {
  throw "dist/manifest.json does not contain a version."
}

New-Item -ItemType Directory -Force -Path $ReleaseDir | Out-Null
$zipPath = Join-Path $ReleaseDir "multica-review-extension-$version.zip"
if (Test-Path -LiteralPath $zipPath) {
  Remove-Item -LiteralPath $zipPath -Force
}

Compress-Archive -Path (Join-Path $DistDir "*") -DestinationPath $zipPath -Force
Write-Host "Created $zipPath"
