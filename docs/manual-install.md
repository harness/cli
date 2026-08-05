# Manual Install

The [one-line installers](../install.sh) (`curl -fsSL .../install.sh | sh` on Unix,
`irm .../install.ps1 | iex` on Windows) are the recommended way to install the CLI.
Use the steps below instead if the installer can't reach GitHub in your environment
(e.g. WSL behind a corporate SSL-inspecting proxy), you need a vetted binary from
an internal mirror, or you're baking the install into a script/Dockerfile.

This covers macOS, Linux, and Windows (`amd64` / `arm64`).

## 1. Get the release archive

Releases live at [github.com/harness/cli/releases](https://github.com/harness/cli/releases).
Pick a version (or use `latest`) and find the asset for your platform:

| Platform              | Asset name pattern                               |
| --------------------- | ------------------------------------------------ |
| Linux x86_64          | `harness-bundle_<version>_linux_amd64.tar.gz`    |
| Linux ARM64           | `harness-bundle_<version>_linux_arm64.tar.gz`    |
| macOS Intel           | `harness-bundle_<version>_darwin_amd64.tar.gz`   |
| macOS Apple Silicon   | `harness-bundle_<version>_darwin_arm64.tar.gz`   |
| Windows x86_64        | `harness-bundle_<version>_windows_amd64.zip`     |
| Windows ARM64         | `harness-bundle_<version>_windows_arm64.zip`     |

`harness-bundle_*` contains both `harness` and `harness-har` (`.exe` on Windows).
If you only need the core CLI, use `harness-core_<version>_<os>_<arch>.tar.gz` (Unix)
or `.zip` (Windows) instead — it contains just `harness`.

`harness-har` is a **plugin**: unlike `harness`, it is not used from wherever you happen
to place it. Step 3 registers it with `harness install plugin`, which copies it into
`~/.harness/bin` and records its grammar in `~/.harness/spec` — until that happens, the
`harness har ...` commands do not exist.

Also grab `harness_<version>_checksums.txt` from the same release, for step 2.

If `curl`/`wget` don't work in your environment, download both files with a regular browser
(e.g. on the Windows side, not inside WSL) and copy them over — on WSL, Windows drives are
mounted under `/mnt/c/...`, so a file saved to Windows' Downloads folder is usually at
`/mnt/c/Users/<you>/Downloads/`.

## 2. Verify the checksum

Confirm the archive wasn't corrupted or tampered with in transit:

**Unix (`tar.gz`):**

```sh
grep harness-bundle_<version>_<os>_<arch>.tar.gz harness_<version>_checksums.txt
sha256sum harness-bundle_<version>_<os>_<arch>.tar.gz    # Linux
shasum -a 256 harness-bundle_<version>_<os>_<arch>.tar.gz # macOS
```

**Windows (`zip`):**

```powershell
Select-String harness-bundle_<version>_windows_amd64.zip harness_<version>_checksums.txt
Get-FileHash harness-bundle_<version>_windows_amd64.zip -Algorithm SHA256
```

The hash printed should match the one from the checksums file.

## 3. Extract and install the binaries

**Unix:**

```sh
tar -xzf harness-bundle_<version>_<os>_<arch>.tar.gz -C /tmp/harness-install
mkdir -p ~/.local/bin
mv /tmp/harness-install/harness ~/.local/bin/harness
chmod +x ~/.local/bin/harness

# register the bundled har plugin (skip if using harness-core)
~/.local/bin/harness install plugin /tmp/harness-install/harness-har
```

**Windows (Command Prompt or PowerShell):**

```powershell
Expand-Archive harness-bundle_<version>_windows_amd64.zip -DestinationPath $env:TEMP\harness-install
New-Item -ItemType Directory -Force -Path "$env:LOCALAPPDATA\Programs\harness"
Copy-Item "$env:TEMP\harness-install\harness.exe" "$env:LOCALAPPDATA\Programs\harness\"

# register the bundled har plugin (skip if using harness-core)
& "$env:LOCALAPPDATA\Programs\harness\harness.exe" install plugin "$env:TEMP\harness-install\harness-har.exe"
```

`~/.local/bin` (Unix) and `%LOCALAPPDATA%\Programs\harness` (Windows) match the default
installer locations for the **core** binary, and any directory on your `PATH` works. The
`har` plugin is different: `install plugin` always puts it in `~/.harness/bin`, which does
not need to be on `PATH` — the CLI dispatches to it by absolute path.

## 4. Add to PATH and enable completions

If the install directory isn't already on your `PATH`, add it to your shell config.

**Bash** — add to `~/.bashrc`:

```sh
export PATH="$HOME/.local/bin:$PATH"
source <(harness completion bash)
```

**Zsh** — add to `~/.zshrc`:

```sh
export PATH="$HOME/.local/bin:$PATH"
source <(harness completion zsh)
```

**PowerShell** — add to your profile:

```powershell
$env:Path = "$env:LOCALAPPDATA\Programs\harness;" + $env:Path
harness completion powershell | Out-String | Invoke-Expression
```

Then reload the shell or open a new terminal.

## 5. Verify

```sh
harness version
```

On Windows Command Prompt, use `harness.exe version` if the install directory is not yet on `PATH`.

## Upgrading later

Once installed, `harness install cli` can upgrade in place — see the
[Upgrade section in the README](../README.md#-upgrade). If `harness install cli` also can't
reach GitHub in your environment, repeat steps 1–3 above with the new version.
