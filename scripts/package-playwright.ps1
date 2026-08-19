$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$version = $env:CINEKO_PLAYWRIGHT_VERSION
if (-not $version) { throw 'CINEKO_PLAYWRIGHT_VERSION is required' }
$version = $version.TrimStart('v')
$source = Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) "ms-playwright-go\$version"
$releaseDir = 'build/release'
$archive = Join-Path $releaseDir "cineko-playwright-$version-windows-amd64.zip"
if (-not (Test-Path -LiteralPath (Join-Path $source 'node.exe'))) { throw "Playwright driver is missing: $source" }
New-Item -ItemType Directory -Force -Path $releaseDir | Out-Null
Compress-Archive -LiteralPath (Join-Path $source 'node.exe'), (Join-Path $source 'package') -DestinationPath $archive -Force
node scripts/write-release-metadata.mjs playwright $version windows/amd64 $archive node.exe
