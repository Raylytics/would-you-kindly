# would-you-kindly

[![ci](https://github.com/raylytics/would-you-kindly/actions/workflows/ci.yml/badge.svg)](https://github.com/raylytics/would-you-kindly/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/raylytics/would-you-kindly)](https://github.com/raylytics/would-you-kindly/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

A terminal UI over the [beads](https://github.com/gastownhall/beads) issue
tracker, built for one specific moment: when an agent finishes the
mechanical part of a task and needs a human to do the part it cannot.

`wyk` (the binary) lists your bd issues, navigates them with vim keys, and
lets you press one key — `h` — to filter down to exactly the issues an
agent has flagged for your attention.

The name is the point: *"would you kindly"* is a polite imperative — the
agent asking the human to please do the part it can't. `wyk` is the binary;
`bd` (beads) is the tracker underneath.

![wyk handoff round-trip](docs/screenshots/wyk-demo.gif)

## Why

Most issue trackers assume tasks are either assigned to a person or
not. When an agent is doing most of the work, a third state matters:
"the agent has done what it can and is handing this back to a human."
`wyk` keys off a small, explicit convention and gives that state its
own keystroke.

**The contract in one sentence.** A task is for a human when it carries
the `human` label; its description is the runbook the human follows;
`src:agent` / `src:human` records who filed it. The full contract lives
in [`docs/CONTRACT.md`](docs/CONTRACT.md).

**The mental model**, smallest to largest: **beads (`bd`)** is the issue
tracker, storing every issue in a [Dolt](https://www.dolthub.com)
database under `.beads/` — wyk never touches that storage directly, it
shells out to `bd` for every read and write. **wyk** is a *view* over bd
plus a *label convention*; it owns no data of its own, so stop using wyk
and your issues are still plain bd issues, readable by any bd tool.
**The convention** is, at heart, one label: **`human` = your turn.**
Everything else (`src:agent`, `agent-handoff`, the presets, the Owner
column) is detail layered on that single signal. If you remember one
thing: an issue with the `human` label is waiting on *you* — press `h`
to see only those.

## This repo is the demo

wyk is built the way it asks you to work: agents file, claim and close
its own bd issues, and hand the human-only steps back through the same
`human` label the TUI keys off. The evidence is in the history, not the
pitch:

- **More than half of the 440+ commits** carry a `Closes: <bd-id>`
  trailer — the post-commit hook `wyk init` installs auto-closed the
  issue behind each one. `git log --grep '^Closes:'` shows the loop
  running on itself.
- **360+ issues** have gone through the tracker, all but a handful
  closed. Human-flagged issues close in a median of ~12 hours;
  agent-filed ones carry `src:agent` and the Claude session that filed
  them (`wyk stats` reports both). The tracker itself is not in git, but
  [`docs/backlog-snapshot.md`](docs/backlog-snapshot.md) is a scrubbed,
  read-only snapshot of every issue in it: titles, status, priority, the
  contract labels and dates, nothing else.
- The agent side lives in the repo: [`CLAUDE.md`](CLAUDE.md) (the
  conventions agents follow here, including *why* each one exists),
  [`AGENTS.md`](AGENTS.md), the hooks in
  [`.claude/settings.json`](.claude/settings.json), the shipped
  [skills](docs/SKILLS.md), and the label contract in
  [`docs/CONTRACT.md`](docs/CONTRACT.md).
- The review side is [roborev](https://www.roborev.io): every commit is
  reviewed by an agent, findings land as bd issues, and the TUI's
  `review` preset is where a human triages them.

[`docs/WORKFLOW.md`](docs/WORKFLOW.md) walks the whole loop end to end
with real issue IDs from this repo.

## The 60-second demo

The GIF above is the whole round-trip. In a repo that already has a bd
workspace:

```bash
wyk init                                             # hook + registry + agent conventions, idempotent
printf '## Steps\n1. rotate it\n2. paste it into op://…\n3. Close this issue when done.\n' \
  | wyk handoff -create "Rotate the staging DB password" -priority 1
# created would-you-kindly-2oa — "Rotate the staging DB password"
# handed would-you-kindly-2oa to human (78-byte runbook)
wyk                                                  # the TUI; press h
```

The human view shows that one row with a `HUMAN` badge; the runbook is
in the pane on the right (on a terminal of 140×36 or more; `enter`
opens it on a smaller one). Do the thing, press `a` to close it — or `H`
to bounce it back if the next step is the agent's again. The agent's
next session sees the bounce with `wyk inbox`. That's the product.

## Install

You'll need Go 1.26+ (matching `go.mod`) and the **`bd` (beads)** binary
on your `PATH` — wyk shells out to it for all storage. Install bd first
by following the instructions in its repo:
[github.com/gastownhall/beads](https://github.com/gastownhall/beads).
wyk is tested against **bd 1.0.4 or newer** (verified through 1.3.0);
`wyk doctor` warns if your bd is older.

```bash
# Homebrew (macOS) — installs a prebuilt cask, no Go toolchain needed:
brew install raylytics/wyk/wyk

# With Go, latest tagged release:
go install github.com/jimbottle/would-you-kindly/cmd/wyk@latest

# Or tip of main:
go install github.com/jimbottle/would-you-kindly/cmd/wyk@main
```

On Linux, use `go install` above, or grab a prebuilt linux/darwin
(amd64/arm64) binary straight from the
[Releases page](https://github.com/raylytics/would-you-kindly/releases)
(the Homebrew cask is macOS-only). You can also build from a checkout
with `go build -o ./bin/wyk ./cmd/wyk`.

First run, inside a repo:

```bash
bd init      # only if this repo has no .beads workspace yet
wyk init     # install the post-commit hook + register the repo
wyk doctor   # verify bd, wyk, and the hook are all wired up
```

macOS and Linux are first-class; Windows is supported via WSL. What
`wyk --version` prints for each install method, the clipboard and
`$EDITOR` requirements, and the rest of the fine print are in the
[operator reference](docs/OPERATOR.md#install-details).

## The loop

### The agent's side

```bash
wyk conventions                       # the label contract, agent-readable (-json too)
cat runbook.md | wyk handoff -create "<imperative title>"   # file + hand off in one step
cat runbook.md | wyk handoff <id>     # hand off an existing issue
wyk inbox                             # what a human bounced back to me (-json for ingest)
wyk next -claim                       # pick the best unclaimed issue and check it out as a lease
```

`wyk handoff` tags the issue `human`, replaces its description with the
runbook from stdin (or `-file`), and refuses a runbook that is neither a
**task** (`## Steps`) nor a **question** (`## Question`);
`wyk handoff -template [-question]` prints the skeleton. `wyk inbox` is
the other direction: issues the agent filed (`src:agent`) that no longer
carry `human`. When several agents share a repo, a claim is an expiring
**lease** owned by the agent's identity, so a dead session can't hold
work forever. An opt-in Stop hook (`wyk hook install-nudge`) makes the
bounce-back *push* rather than pull, and `wyk skills install` gives
Claude Code the `wyk`, `wyk-handoff` and `wyk-project-review` skills. A
configurable [handoff hook](docs/HOOKS.md) mirrors every human task into
the task manager you already use.

### The human's side

```bash
wyk                    # every registered repo in one list
wyk -preset human      # land on the human view (all/ready/human/mine/blocked/review)
wyk -C /path/to/repo   # one repo, bypassing the registry
```

The **Owner** column says whose move it is and is never blank: `HUMAN`
(you must act), `AGENT` (the default for anything without the `human`
label), `HUMAN-BLOCK` (an agent issue blocked by a human-flagged
dependency), `AGENT-HANDOFF` (another agent's; a human orchestrates),
`@<owner>` (checked out by that agent identity right now) or `EXPIRED`
(its lease lapsed, so it's claimable). Writes route to the right repo
automatically; the list refreshes every 20 seconds and immediately
after an external `bd` write where fsnotify is available.

## Keys

The ones you'll use in the first five minutes; the full keymap is
[`docs/generated/keymap.md`](docs/generated/keymap.md) and `?` in the TUI.

| Key       | Action                                                        |
| --------- | ------------------------------------------------------------- |
| `h`       | Toggle the human-flagged view (press again to return)         |
| `tab`     | Cycle presets (all → ready → human → mine → blocked → review) |
| `j` / `k` | Move down / up; `]` / `[` jump between human-flagged rows     |
| `enter`   | Open the selected issue (or focus the detail pane); `esc` back |
| `p`       | Show / hide the detail pane (auto-on at 140×36+)              |
| `/`       | Filter (fuzzy title; substring on repo, branch, ID, body)     |
| `a`       | Close the cursor issue (asks `[y/N]`); `u` undoes the last close |
| `H`       | Toggle the `human` label — hand off, or bounce back           |
| `n`       | Append a note; `e` edit the description in `$EDITOR`          |
| `v`       | Multi-select; bulk close / flag / defer the marked rows       |
| `y`       | Yank the row's ID; `:` opens the command palette              |
| `q`       | Quit                                                          |

## Where everything else is

- [`docs/OPERATOR.md`](docs/OPERATOR.md) — the operator reference:
  registry management, `--probe` for scripts, `wyk doctor`, `wyk stats`,
  the agent inbox and leases in depth, the Stop-hook nudge, `wyk init`
  and hook chaining, every key, theming, the other subcommands, a
  day-in-the-life walkthrough with the column definitions, and status.
- [`docs/CONTRACT.md`](docs/CONTRACT.md) — the handoff contract: labels,
  the runbook structure, the status lifecycle, leases, versioning.
- [`docs/HOOKS.md`](docs/HOOKS.md) — the handoff hook: payload, events,
  writing an adapter.
- [`docs/WORKFLOW.md`](docs/WORKFLOW.md) — how this repo is built with
  its own loop, with real issue IDs.
- [`docs/SKILLS.md`](docs/SKILLS.md) — the Claude Code skills and how
  they stay in sync with the CLI.
- [`docs/generated/cli.md`](docs/generated/cli.md) — the full CLI
  reference; every subcommand that emits structured data accepts
  `-json`, and the shapes are stable across patch releases.
- [`CHANGELOG.md`](CHANGELOG.md) and the
  [GitHub releases](https://github.com/raylytics/would-you-kindly/releases)
  — what's shipped and when. [`CONTRIBUTING.md`](CONTRIBUTING.md) for
  the local gate and versioning policy; [`SECURITY.md`](SECURITY.md) for
  the security model (local, single-user, no telemetry, untrusted issue
  text is sanitised before rendering) and how to report a vulnerability.

`wyk` is not a replacement for `bd`, not a server, and not a general
project-management UI — the scope is the agent ↔ human handoff loop and
the views that serve it.

## Development

```bash
pre-commit install --hook-type pre-commit --hook-type commit-msg   # once per clone
make check                                                          # the full local gate: mirrors CI
make lint                                                           # golangci-lint at the pinned version
go test -race ./...
```

Tool versions are pinned once: Go in `go.mod`, Go and golangci-lint for local
runs in `.tool-versions`, and the same values drive `.github/workflows/ci.yml`.
Commits follow Conventional Commits and land through squash-merged PRs; the
details are in [CONTRIBUTING.md](CONTRIBUTING.md).

## Releasing

`scripts/release.sh X.Y.Z` on a clean, green `main`: it prepends the release
to `CHANGELOG.md` from the commits with git-cliff, commits
`chore(release): vX.Y.Z`, and pushes a signed tag. The `release` workflow then
runs goreleaser, which attaches the binaries and bumps the Homebrew tap. The
changelog lives in [CHANGELOG.md](CHANGELOG.md).

## License

MIT. © Raylytics LLC. See [LICENSE](LICENSE).
