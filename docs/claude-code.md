# Using the Harness CLI with Claude Code

Claude Code can run `harness` for you: list pipelines, check why an execution
failed, look at pull requests, and more. Three things are needed: you log in,
Claude knows the CLI exists, and Claude knows which profile to use.

## 1. Install and log in

Install the CLI (see the [README](../README.md)), then pick one way to log in.

**Option A (recommended): log in before starting Claude Code.**

```sh
harness auth login      # interactive wizard
harness auth status     # confirm account, org, and project
```

Claude Code uses the saved profile. Your token never appears in the chat.

**Option B: log in during a Claude Code session (SSO only).**

`harness auth login` is an interactive wizard, and Claude can't operate it. If
your account is enabled for SSO, run the browser login from inside the session by
typing `!` first, which runs the command in your own shell:

```
! harness auth login --sso
```

If your account isn't SSO-enabled, use Option A. Don't paste a token into the
chat.

**Automation (no profile):** set `HARNESS_API_KEY` and `HARNESS_ACCOUNT` in the
environment before launching Claude Code. See [auth.md](auth.md).

## 2. Tell Claude about the CLI

Claude doesn't know that "my pipelines" means a `harness` command. It may try an
MCP server, read the API docs and write a script, or scrape the website. Fix this
in either of the two ways below.

**Quick: say it in your prompt.**

> Run `harness` to learn the CLI, then list my pipelines.

`harness` prints a short reference covering the command grammar, auth, and how to
discover more (`harness get module <name>`, `harness get noun <noun>`,
`harness <verb> <noun> --help`). Claude explores from there.

**Better: add a standing instruction** so you don't have to repeat it. Put this in
your project's `CLAUDE.md`, or in `~/.claude/CLAUDE.md` for all projects:

```md
For anything involving Harness (pipelines, executions, connectors, PRs, secrets,
etc.), use the `harness` CLI. Run `harness` first and read all of its output, then
explore with `harness get module <name>` and `harness <verb> <noun> --help`.
Don't use the web UI, scrape the website, or write API scripts.
```

**Optional: install the skill.** [claude-skill.md](claude-skill.md) is a Claude
Code skill that does the same job and adds safety rules. Claude loads it
automatically when you mention Harness resources, and it asks you before running
commands that change things. Install it with:

```sh
mkdir -p ~/.claude/skills/harness-cli
cp docs/claude-skill.md ~/.claude/skills/harness-cli/SKILL.md
```

Use either the `CLAUDE.md` snippet or the skill. You don't need both.

## 3. Tell Claude which profile to use

If you have several profiles, or Claude can't find a working login, it tends to
improvise: guessing profile names, hunting for tokens, or trying odd workarounds.
The fix is to tell it which profile to use. Check what you have with
`harness auth profiles`.

**In your prompt:** "Use `--profile foo` on every `harness` command."

**Or set it once before starting Claude Code.** Commands then work without a flag:

```sh
export HARNESS_PROFILE=foo
claude
```

If Claude reports "not logged in", don't let it keep trying. Log in yourself
(Option A or B above), then tell it to continue.

## 4. Try it

- "List my pipelines."
- "Show the last 5 executions of `<pipeline>` and why the latest one failed."
- "Which PRs in `<repo>` are open and authored by me?"

## Tips

- **Check the scope.** Ask Claude to run `harness auth status` first. Commands
  that use the wrong account, org, or project can succeed with the wrong data.
- **Review writes.** `list` and `get` only read. Review `create`, `update`,
  `delete`, and `execute` before approving them.
- **Large result sets.** `list` returns 20 items by default. Ask for "all of them"
  when you need a complete answer.

## Troubleshooting

| Symptom | Fix |
|---|---|
| `not logged in — run 'harness auth login'` | Log in outside the session (Option A), then continue. |
| `--sso` fails or isn't offered | Your account may not be SSO-enabled. Use a token login (Option A). |
| Results look empty or wrong | Run `harness auth status` and check the profile, org, and project. |
| Claude guesses profile names or hunts for tokens | Give it the profile: `--profile foo` or `HARNESS_PROFILE=foo`. |
| Claude uses an MCP server or writes scripts | Add the `CLAUDE.md` snippet or install the skill. |

