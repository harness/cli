# Manual Install

The [one-line installer](../install.sh) (`curl -fsSL .../install.sh | sh`) is the recommended
way to install the CLI. Use the steps below instead if `curl` can't reach GitHub in your
environment (e.g. WSL behind a corporate SSL-inspecting proxy), you need a vetted binary from
an internal mirror, or you're baking the install into a script/Dockerfile.

This covers macOS and Linux (`amd64` / `arm64`). There's no Windows release yet.

## 1. Get the release archive

Releases live at [github.com/harness/cli/releases](https://github.com/harness/cli/releases).
Pick a version (or use `latest`) and find the asset for your platform:

| Platform            | Asset name pattern                              |
| -------------------- | ------------------------------------------------ |
| Linux x86_64          | `harness-bundle_<version>_linux_amd64.tar.gz`   |
| Linux ARM64           | `harness-bundle_<version>_linux_arm64.tar.gz`   |
| macOS Intel           | `harness-bundle_<version>_darwin_amd64.tar.gz`  |
| macOS Apple Silicon   | `harness-bundle_<version>_darwin_arm64.tar.gz`  |

`harness-bundle_*` contains both `harness` and `harness-har`. If you only need the core CLI,
use `harness-core_<version>_<os>_<arch>.tar.gz` instead — it contains just `harness`.

Also grab `harness_<version>_checksums.txt` from the same release, for step 2.

If `curl`/`wget` don't work in your environment, download both files with a regular browser
(e.g. on the Windows side, not inside WSL) and copy them over — on WSL, Windows drives are
mounted under `/mnt/c/...`, so a file saved to Windows' Downloads folder is usually at
`/mnt/c/Users/<you>/Downloads/`.

## 2. Verify the checksum

Confirm the archive wasn't corrupted or tampered with in transit:

```sh
grep harness-bundle_<version>_<os>_<arch>.tar.gz harness_<version>_checksums.txt
sha256sum harness-bundle_<version>_<os>_<arch>.tar.gz    # Linux
shasum -a 256 harness-bundle_<version>_<os>_<arch>.tar.gz # macOS
```

The hash printed by `sha256sum`/`shasum` should match the one from the `grep` line above.

## 3. Extract and install the binaries

```sh
tar -xzf harness-bundle_<version>_<os>_<arch>.tar.gz -C /tmp/harness-install
mkdir -p ~/.local/bin
mv /tmp/harness-install/harness ~/.local/bin/harness
mv /tmp/harness-install/harness-har ~/.local/bin/harness-har   # skip if using harness-core
chmod +x ~/.local/bin/harness ~/.local/bin/harness-har
```

`~/.local/bin` matches the default installer location, but any directory on your `PATH`
works — `/usr/local/bin` is a common system-wide alternative.

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

Then reload the shell (`source ~/.bashrc` or `source ~/.zshrc`) or open a new terminal.

## 5. Verify

```sh
harness version
```

## Upgrading later

Once installed, `harness install cli` can upgrade in place — see the
[Upgrade section in the README](../README.md#-upgrade). If `harness install cli` also can't
reach GitHub in your environment, repeat steps 1–3 above with the new version.
