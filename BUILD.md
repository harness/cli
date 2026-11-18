# Building from Source

Requires [Task](https://taskfile.dev) and Go 1.26+.

```sh
brew install go-task
task build
```

Run `source local-setup.zsh` which adds `$(pwd)/bin` to your `PATH` and sources the harness completions into your shell.

## Release grammar subset

The `harness-core` release build uses gotreesitter's `grammar_subset` tags for
Java, Python, JavaScript, TypeScript, and TSX only. The dependency is MIT
licensed; see [NOTICE](NOTICE) for the release notice. CI builds the
cross-platform GoReleaser snapshot artifacts and verifies the embedded tags.
