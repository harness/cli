if [[ ! -f "$(pwd)/go.mod" ]] || ! grep -q '^module github.com/harness/cli$' "$(pwd)/go.mod"; then
  echo "local-setup.zsh must be sourced from the root of the harness CLI repo" >&2
  return 1
fi

if [[ ":$PATH:" != *":$(pwd)/bin:"* ]]; then
  export PATH="$(pwd)/bin:$PATH"
fi
export HARNESS_CLI_HOME="$(pwd)/devhome"
source <(harness completion zsh)
