# Install ciguse on Windows (PowerShell 5.1+):
#   irm https://raw.githubusercontent.com/demetriusk/ciguse/main/install.ps1 | iex
# Options (environment): CIGUSE_VERSION=v0.1.0 to pin a release, CIGUSE_INSTALL_DIR to choose where it goes.
$ErrorActionPreference = 'Stop'

$repo = 'demetriusk/ciguse'
$dir = if ($env:CIGUSE_INSTALL_DIR) { $env:CIGUSE_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\ciguse' }

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  'AMD64' { 'amd64' }
  'ARM64' { 'arm64' }
  default { throw "ciguse: unsupported CPU $env:PROCESSOR_ARCHITECTURE" }
}
$base = if ($env:CIGUSE_VERSION) { "https://github.com/$repo/releases/download/$env:CIGUSE_VERSION" } else { "https://github.com/$repo/releases/latest/download" }
$archive = "ciguse_windows_$arch.zip"

$tmp = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Invoke-WebRequest -UseBasicParsing "$base/$archive" -OutFile (Join-Path $tmp $archive)
  Invoke-WebRequest -UseBasicParsing "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')

  $line = Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { $_ -match " $([regex]::Escape($archive))$" }
  $want = if ($line) { ($line -split ' ')[0] } else { '' }
  $got = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $archive)).Hash.ToLower()
  if (-not $want -or $want -ne $got) { throw "ciguse: checksum mismatch for $archive, not installing" }

  Expand-Archive -Path (Join-Path $tmp $archive) -DestinationPath $tmp -Force
  New-Item -ItemType Directory -Path $dir -Force | Out-Null
  Copy-Item (Join-Path $tmp 'ciguse.exe') (Join-Path $dir 'ciguse.exe') -Force
} finally {
  Remove-Item -Recurse -Force $tmp
}

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $dir) {
  [Environment]::SetEnvironmentVariable('Path', "$userPath;$dir".TrimStart(';'), 'User')
  Write-Host "Added $dir to your user PATH. Open a new terminal to use it."
}
Write-Host "ciguse installed to $(Join-Path $dir 'ciguse.exe') ($(& (Join-Path $dir 'ciguse.exe') version))"
