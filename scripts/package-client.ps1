$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$version = $env:CINEKO_VERSION
if (-not $version) { throw 'CINEKO_VERSION is required' }
$version = $version.TrimStart('v')
$releaseDir = 'build/release'
$archive = Join-Path $releaseDir "cineko-client-v$version-windows-amd64.zip"
if (-not (Test-Path -LiteralPath 'build/bin/Cineko.exe')) { throw 'Cineko.exe is missing' }
New-Item -ItemType Directory -Force -Path $releaseDir | Out-Null
Compress-Archive -LiteralPath 'build/bin/Cineko.exe' -DestinationPath $archive -Force
node scripts/write-release-metadata.mjs client $version windows/amd64 $archive Cineko.exe
