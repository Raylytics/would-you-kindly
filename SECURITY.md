# Security Policy

## Supported versions

`wyk` is pre-1.0 and ships fixes on the latest release only. Always run a
recent version (`wyk update`); older tags are not patched.

## Reporting a vulnerability

Please report security issues **privately**, not through public issues.

- Preferred: open a [private security advisory](https://github.com/jimbottle/would-you-kindly/security/advisories/new)
  on this repository (GitHub → Security → Report a vulnerability).
- Include a description, reproduction steps, affected version
  (`wyk --version`), and the impact you observed.

You can expect an acknowledgement within a few days. Once a fix is
released, we're happy to credit you in the changelog unless you'd rather
stay anonymous.

## Scope notes

`wyk` is a local developer tool: it shells out to `bd`, `git`, and `go`,
and reads/writes files under your home and repo directories. It has no
network-service surface of its own (the only outbound call is an
opt-in GitHub release check for `wyk update`). Reports about local-only
behavior that requires an attacker to already control your shell,
`$PATH`, or repo contents are lower priority, but still welcome.

## Security model

`wyk` is a local, single-user tool. It has no daemon and no network
service; the only outbound call is an opt-in GitHub release check for
`wyk update`. There is **no telemetry** — wyk never reports usage,
repo names, paths, or issue content anywhere.

**What it executes.** wyk runs two things on your behalf:

- **The `bd` binary on your `PATH`** — every read and write goes through
  bd (with `--dolt-auto-commit=on` on writes); wyk never touches the Dolt
  database directly. You trust the `bd` you've installed.
- **A git `post-commit` hook** that `wyk init` writes into the repos you
  initialize. The hook calls `wyk hook post-commit` to auto-close issues
  named in `Closes: <id>` commit trailers. wyk preserves a pre-existing
  hook with `-chain` (e.g. to sit after roborev's) and only clobbers one
  with explicit `-force`; `-uninstall` restores the saved `post-commit.pre-wyk`.

**What it trusts.** Issue text (titles, descriptions, runbooks, labels)
from any bd workspace you register is **untrusted input** — it can contain
arbitrary bytes. Before rendering it in the TUI, wyk strips terminal
control and escape sequences (`internal/sanitize`, applied inline and
per-block), so a hostile issue can't smuggle ANSI escapes to move your
cursor, rewrite the screen, or hijack the terminal. Register only
workspaces whose contents — and whose `post-commit` hooks, which run when
*you* commit there — you're willing to trust.

**Local state.** wyk reads and writes the registry at
`~/.config/wyk/repos.json` (XDG-aware), an optional `theme.json` color
overlay, and each initialized repo's `.git/hooks/post-commit`. Nothing
leaves your machine.

