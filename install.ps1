#Requires -Version 5.1
param(
    [string]$InstallDir,
    [string]$Version,
    [switch]$Core,
    [switch]$NonInteractive,
    [switch]$NoVerify
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$Repo = "harness/cli"

if (-not $InstallDir) { $InstallDir = $env:HARNESS_INSTALL_DIR }
$UserOverride = [bool]$InstallDir
if (-not $InstallDir) {
    $localAppData = $env:LOCALAPPDATA
    if (-not $localAppData) { $localAppData = Join-Path $env:USERPROFILE "AppData\Local" }
    $InstallDir = Join-Path $localAppData "Programs\harness"
}

$Interactive = -not ($NonInteractive -or $env:HARNESS_NONINTERACTIVE)
$SkipVerify = [bool]($NoVerify -or $env:HARNESS_NO_VERIFY)
$CoreOnly = [bool]($Core -or $env:HARNESS_CORE_ONLY)

# Testing hook: point at a local directory or private mirror holding the release
# assets instead of GitHub Releases. Must contain the zip and checksums file.
$AssetBase = $env:HARNESS_INSTALL_BASE_URL

function Write-Info($Message) { Write-Host "  - $Message" -ForegroundColor Blue }
function Write-Ok($Message) { Write-Host "  + $Message" -ForegroundColor Green }
function Write-Note($Message) { Write-Host "  ! $Message" -ForegroundColor Yellow }
function Fail($Message) { Write-Host "  x $Message" -ForegroundColor Red; throw $Message }

function Test-Interactive {
    if (-not $Interactive) { return $false }
    return [Environment]::UserInteractive -and -not [Console]::IsInputRedirected
}

function Confirm-Yes($Prompt) {
    $answer = Read-Host "  ? $Prompt [Y/n]"
    return -not ($answer -match '^[nN]')
}

function Get-Platform {
    $raw = $env:PROCESSOR_ARCHITECTURE
    if ($env:PROCESSOR_ARCHITEW6432) { $raw = $env:PROCESSOR_ARCHITEW6432 }
    switch ($raw) {
        "AMD64" { return "windows_amd64" }
        "ARM64" { return "windows_arm64" }
        default { Fail "Unsupported architecture: $raw" }
    }
}

function Get-LatestVersion {
    $headers = @{ "User-Agent" = "harness-cli-installer" }
    $response = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers $headers
    return $response.tag_name
}

function Get-Sha256($Path) {
    return (Get-FileHash -Algorithm SHA256 -Path $Path).Hash.ToLower()
}

function Get-Asset {
    param(
        [string]$Base,
        [string]$Name,
        [string]$Dest
    )
    $isUrl = $Base -match '^[a-zA-Z][a-zA-Z0-9+.-]*://'
    if (-not $isUrl -and (Test-Path -LiteralPath $Base -PathType Container -ErrorAction SilentlyContinue)) {
        $src = Join-Path $Base $Name
        if (-not (Test-Path -LiteralPath $src)) { throw "$Name not found in $Base" }
        Copy-Item -LiteralPath $src -Destination $Dest -Force
        return
    }
    Invoke-WebRequest -Uri "$Base/$Name" -OutFile $Dest -UseBasicParsing
}

function Install-HarnessBinaries {
    param(
        [string]$Version,
        [string]$Platform,
        [string]$Dest
    )

    $ver = $Version.TrimStart("v")
    if ($CoreOnly) {
        $pkgName = "harness-core_${ver}_${Platform}"
    } else {
        $pkgName = "harness-bundle_${ver}_${Platform}"
    }

    $archiveName = "$pkgName.zip"
    $checksumName = "harness_${ver}_checksums.txt"
    $base = $AssetBase
    if (-not $base) { $base = "https://github.com/$Repo/releases/download/$Version" }
    $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("harness-install-" + [guid]::NewGuid().ToString())
    New-Item -ItemType Directory -Path $tmp | Out-Null

    try {
        Write-Info "Downloading $archiveName ..."
        $archivePath = Join-Path $tmp $archiveName
        try {
            Get-Asset -Base $base -Name $archiveName -Dest $archivePath
        } catch {
            if ($AssetBase) { Fail "Could not fetch $archiveName from $base" }
            Fail "Could not download $archiveName - this release may not include Windows assets yet"
        }

        if ($SkipVerify) {
            Write-Note "Skipping checksum verification (-NoVerify)"
        } else {
            Write-Info "Verifying checksum..."
            $checksumPath = Join-Path $tmp "checksums.txt"
            Get-Asset -Base $base -Name $checksumName -Dest $checksumPath
            $match = Select-String -Path $checksumPath -Pattern ([regex]::Escape($archiveName)) | Select-Object -First 1
            if (-not $match) { Fail "Checksum entry not found for $archiveName" }
            $expected = $match.Line.Split()[0].ToLower()
            $actual = Get-Sha256 $archivePath
            if ($actual -ne $expected) { Fail "Checksum mismatch - download may be corrupted" }
        }

        $extractDir = Join-Path $tmp "extract"
        Expand-Archive -Path $archivePath -DestinationPath $extractDir -Force
        New-Item -ItemType Directory -Path $Dest -Force | Out-Null

        $coreSrc = Join-Path $extractDir "harness.exe"
        if (-not (Test-Path $coreSrc)) { Fail "Binary harness.exe not found in archive" }
        $coreTarget = Join-Path $Dest "harness.exe"
        Copy-Item -Path $coreSrc -Destination $coreTarget -Force
        Write-Ok "Installed harness.exe $Version to $coreTarget"

        # A failure here still leaves a working core, so note it instead of aborting.
        if (-not $CoreOnly) {
            Write-Info "Installing har module..."
            & $coreTarget install module har 2>$null | Out-Null
            if ($LASTEXITCODE -eq 0) {
                Write-Ok "Installed har module to ~\.harness\bin"
            } else {
                Write-Note "Could not install the har module - run 'harness install module har' to retry"
            }
        }
    } finally {
        Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
    }
}

function Add-InstallDirToUserPath {
    param([string]$Dir)

    $current = [Environment]::GetEnvironmentVariable("Path", "User")
    $parts = @()
    if ($current) { $parts = @($current -split ';' | Where-Object { $_ -ne "" }) }
    if ($parts -contains $Dir) { return $false }

    $updated = @($Dir) + @($parts | Where-Object { $_ -ne $Dir })
    [Environment]::SetEnvironmentVariable("Path", ($updated -join ';'), "User")
    Send-SettingChange
    return $true
}

# Broadcast WM_SETTINGCHANGE so new shells pick up the PATH change without a reboot.
function Send-SettingChange {
    if (-not ("Harness.NativeMethods" -as [type])) {
        Add-Type -Namespace Harness -Name NativeMethods -MemberDefinition @"
[System.Runtime.InteropServices.DllImport("user32.dll", SetLastError = true, CharSet = System.Runtime.InteropServices.CharSet.Auto)]
public static extern System.IntPtr SendMessageTimeout(
    System.IntPtr hWnd, int Msg, System.IntPtr wParam, string lParam,
    int fuFlags, int uTimeout, out System.IntPtr lpdwResult);
"@ -ErrorAction SilentlyContinue
    }
    try {
        $result = [System.IntPtr]::Zero
        [void][Harness.NativeMethods]::SendMessageTimeout(
            [System.IntPtr]0xffff, 0x001A, [System.IntPtr]::Zero, "Environment", 2, 5000, [ref]$result)
    } catch {
        Write-Note "Could not broadcast PATH change - open a new terminal to pick it up"
    }
}

function Get-HarnessProfileBlock {
    @"
# <HarnessCLI>
`$env:Path = "$InstallDir;" + `$env:Path
harness completion powershell | Out-String | Invoke-Expression
# </HarnessCLI>
"@
}

function Test-ProfilePatched {
    param([string]$ProfilePath)
    if (-not (Test-Path $ProfilePath)) { return $false }
    return [bool](Select-String -Path $ProfilePath -Pattern "<HarnessCLI>" -Quiet)
}

function Invoke-Installer {
    Write-Host ""
    Write-Host "  Harness CLI installer"
    Write-Host ""

    $platform = Get-Platform
    $version = $Version
    if (-not $version) { $version = $env:HARNESS_VERSION }
    if (-not $version) { $version = Get-LatestVersion }
    if (-not $version) { Fail "Could not determine latest version" }

    Install-HarnessBinaries -Version $version -Platform $platform -Dest $InstallDir

    $harnessExe = Join-Path $InstallDir "harness.exe"
    if (Test-Path $harnessExe) {
        $env:HARNESS_INSTALL_TYPE = "script"
        & $harnessExe --post-install 2>$null | Out-Null
    }

    $patchedProfile = $false
    if ((Test-Interactive) -and -not $UserOverride -and $PROFILE) {
        $profilePath = $PROFILE
        $profileName = Split-Path -Leaf $profilePath
        if (Test-ProfilePatched $profilePath) {
            Write-Info "Shell config already set up in $profileName, skipping"
        } else {
            Write-Host ""
            Write-Info "Would you like us to update $profileName ?"
            Write-Info "  - Add $InstallDir to PATH"
            Write-Info "  - Add PowerShell completions"
            Write-Host ""
            if (Confirm-Yes "Update $profileName") {
                $profileDir = Split-Path -Parent $profilePath
                if (-not (Test-Path $profileDir)) {
                    New-Item -ItemType Directory -Path $profileDir -Force | Out-Null
                }
                Add-Content -Path $profilePath -Value "`n$(Get-HarnessProfileBlock)`n"
                $patchedProfile = $true
                Write-Ok "Updated $profileName"
            } else {
                Write-Host ""
                Write-Info "To set up manually, add this to ${profileName}:"
                Write-Host (Get-HarnessProfileBlock)
            }
        }
    }

    if (Add-InstallDirToUserPath -Dir $InstallDir) {
        Write-Ok "Added $InstallDir to user PATH"
    }

    Write-Host ""
    $env:Path = "$InstallDir;" + $env:Path
    Write-Ok "Done!"
    Write-Info "Verify right now:  & '$harnessExe' version"
    if ($patchedProfile) {
        Write-Info "New shells (or '. `$PROFILE') pick up 'harness' on PATH with completions."
    } else {
        Write-Info "Open a new terminal to pick up 'harness' on PATH."
    }
}

try {
    Invoke-Installer
} catch {
    # Fail already printed a readable message; suppress the raw PowerShell error record.
    if ($MyInvocation.MyCommand.Path) { exit 1 }
}
