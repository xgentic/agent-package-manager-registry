# Install apm-registry from GitHub releases on Windows.
#
#   irm https://raw.githubusercontent.com/xgentic/agent-package-manager-registry/main/install.ps1 | iex
#
# Environment:
#   $env:VERSION      release tag to install, or "latest" (default: latest)
#   $env:INSTALL_DIR  destination directory (default: %LOCALAPPDATA%\Programs\apm-registry)
#
# Native Windows has no `curl | sh`, so this is the Windows half of install.sh.
# Under Git Bash or WSL, install.sh works and is preferred.

$ErrorActionPreference = 'Stop'

$Repo   = 'xgentic/agent-package-manager-registry'
$Binary = 'apm-registry'
$Version = if ($env:VERSION) { $env:VERSION } else { 'latest' }
$InstallDir = if ($env:INSTALL_DIR) { $env:INSTALL_DIR } else { "$env:LOCALAPPDATA\Programs\apm-registry" }

function Info($m) { Write-Host "==> $m" -ForegroundColor Yellow }
function Ok($m)   { Write-Host "  ok $m" -ForegroundColor Green }
function Fail($m) { Write-Host "error $m" -ForegroundColor Red; exit 1 }

# Only amd64 binaries are published for Windows; on arm64 the x64 emulator runs
# them, so this is a supported path rather than a failure.
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { Info 'no native arm64 build — installing amd64 to run under emulation'; 'amd64' }
    default { Fail "unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}

$asset = "$Binary-windows-$arch.exe"
$base  = if ($Version -eq 'latest') {
    "https://github.com/$Repo/releases/latest/download"
} else {
    "https://github.com/$Repo/releases/download/$Version"
}

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $tmp -Force | Out-Null

try {
    Info "installing $Binary $Version for windows/$arch"

    # Some Windows PowerShell 5.1 hosts still default to TLS 1.0, which GitHub refuses.
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

    $exe = Join-Path $tmp $asset
    try {
        Invoke-WebRequest -Uri "$base/$asset" -OutFile $exe -UseBasicParsing
    } catch {
        Fail "no release asset $asset at $base — check that $Version exists"
    }

    # Same reasoning as install.sh: this catches a truncated download, not a
    # compromised release.
    try {
        $sums = Join-Path $tmp 'SHA256SUMS'
        Invoke-WebRequest -Uri "$base/SHA256SUMS" -OutFile $sums -UseBasicParsing
        $line = Select-String -Path $sums -Pattern "\s$([regex]::Escape($asset))$" | Select-Object -First 1
        if (-not $line) { Fail "SHA256SUMS has no entry for $asset" }
        $expected = ($line.Line -split '\s+')[0]
        $actual = (Get-FileHash -Path $exe -Algorithm SHA256).Hash
        if ($actual -ne $expected.ToUpper()) {
            Fail "checksum mismatch for $asset — refusing to install"
        }
        Ok 'checksum verified'
    } catch [System.Net.WebException] {
        Info 'release publishes no SHA256SUMS — skipping checksum verification'
    }

    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    $target = Join-Path $InstallDir "$Binary.exe"
    Move-Item -Path $exe -Destination $target -Force

    Ok "installed $(& $target version)"
    Write-Host "     $target"

    # Persist to the user PATH, and fix the current session so the next line works.
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($userPath -notlike "*$InstallDir*") {
        [Environment]::SetEnvironmentVariable('Path', "$userPath;$InstallDir", 'User')
        $env:Path = "$env:Path;$InstallDir"
        Info "added $InstallDir to your user PATH — restart other terminals to pick it up"
    }

    Write-Host "`nNext:`n    $Binary repo create local --public`n    $Binary serve"
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
