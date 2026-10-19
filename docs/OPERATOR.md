# wyk operator reference

Everything about running, configuring and scripting wyk that the
[README](../README.md) only summarises. The README is the landing page;
this is the manual. Nothing here is new: every section was moved from
the README verbatim when it was restructured (would-you-kindly-8m5f), so
links into the old README sections now point here.

Contents: [Install details](#install-details) ·
[Run](#run) · [Managing the registry](#managing-the-registry) ·
[Non-TTY one-shot](#non-tty-one-shot) ·
[Handing a task to a human](#handing-a-task-to-a-human-agent-side) ·
[Troubleshooting](#troubleshooting-wyk-doctor) · [Stats](#stats) ·
[Looking up the convention](#looking-up-the-convention-agents-start-here) ·
[Handoff hook](#mirroring-handoffs-into-another-task-manager-handoff-hook) ·
[Agent inbox](#picking-up-bounced-back-work-agent-inbox) ·
[Auto-closing on commit](#auto-closing-issues-on-commit-wyk-init) ·
[Keys](#keys) · [Customizing colors](#customizing-colors) ·
[Other subcommands](#other-subcommands) ·
[A day in the life](#a-day-in-the-life) · [Screenshots](#screenshots) ·
[Status](#status)

## Install details

The install commands themselves are in the
[README](../README.md#install). What follows is the fine print.


Check what version you're running:

```bash
wyk --version
# Tagged install (go install ...@vX.Y.Z): wyk vX.Y.Z
# Homebrew / prebuilt binary (goreleaser): wyk vX.Y.Z (commit <sha>)
# Pseudoversion (go install ...@latest):  wyk vX.Y.(Z+1)-0.YYYYMMDD-<sha>
# From-checkout build (go build):         wyk (devel) (commit <sha>)
```

The `(commit …)` suffix and `-dirty` marker appear for builds produced
from a git working tree — that includes the goreleaser-built Homebrew and
prebuilt binaries (which also get their tag stamped in via `-ldflags`).
Go's module-proxy builds (`go install`) don't carry VCS stamps, so they
show just the version.

## Platform support

macOS and Linux are first-class. Windows is supported via **WSL** — the
native Windows console isn't a target, because clipboard yank and the `e`
editor-suspend use a `/dev/tty` path that doesn't exist there (the keys
surface an error rather than misbehaving). Clipboard copy (`y`) uses
**OSC 52**, so it reaches your local clipboard even over SSH; your terminal
must support OSC 52, and in tmux you need `set -g allow-passthrough on`.
Multi-word `$EDITOR` values (`code -w`, `emacsclient -nw`) are supported;
a path containing spaces is not.


## Run

After running `wyk init` in each repo you want to track, just:

```bash
wyk
```

— and the TUI shows issues from every registered repo, with a
`Repo` and `Branch` column on the left of the table. Writes (`a`,
`H`, `n`) route to the correct repo automatically. The registry
lives at `~/.config/wyk/repos.json` (or `$XDG_CONFIG_HOME/wyk/...`)
and is plain editable JSON.

For a single repo (without going through the registry):

```bash
wyk -C /path/to/repo
```

Launch into a specific preset (so a shell alias can land on the human
view, the agent inbox, etc.):

```bash
wyk -preset human    # all/ready/human/mine/blocked/review
```

The `review` preset filters to issues labelled `roborev` — the work
[roborev](https://www.roborev.io)'s beads hook files from review
failures — giving the roborev → beads → wyk loop a dedicated triage
view. Review-sourced rows also carry a `◆` marker in the title and a
`N review` count rides the status bar in every preset, so they're
distinguishable at a glance without leaving your current view. (The
marker is provenance, kept separate from the Owner column's ownership
badge — a review issue is still agent-owned work to fix.)


## Managing the registry

```bash
wyk registry add [path]       # register a workspace (default: cwd)
wyk registry list             # human-readable list (-json for structured)
wyk registry remove <name>    # drop one entry by display name
wyk registry prune            # drop entries whose path or .git is gone
```

`prune` asks for `[y/N]` confirmation before writing; pass `-y` to skip
the prompt in scripts. The registry file lives at
`~/.config/wyk/repos.json` and stays editable by hand too.

**Registration is not optional bookkeeping.** Every multi-repo view —
`wyk inbox`, `wyk dashboard`, `wyk stats`, the TUI — reads the registry,
so an unregistered workspace is omitted from all of them with no
warning. An agent can follow the handoff convention exactly and the work
still reaches nobody. wyk therefore registers a workspace for you the
first time it writes to it (`wyk create`, `wyk handoff`) or reads it
via `-C`, printing a one-line notice when it does; `wyk doctor` FAILs
when the repo you're standing in is an unregistered bd workspace, and
`wyk doctor -fix` registers it. `wyk registry add` is the explicit form
for scripts and headless setups.

If the registry is empty or has only one entry, `wyk` (no args)
falls back to running against the current directory — the v0.1.0
single-repo behaviour.


## Non-TTY one-shot

For scripts and CI:

```bash
wyk --probe
# 3 issue(s) flagged for human:
#   would-you-kindly-2oa      P1  Rotate the staging database password
#   would-you-kindly-1ej      P2  Approve the v0.3.0 release on GitHub
#   would-you-kindly-117      P3  Decide retention policy for ephemeral wisp beads
```

Exits 0 on success, 2 if bd is missing or there's no workspace, 1 on
other errors. In multi-repo mode, a repo that fails to respond (while
others succeed) is named on **stderr** (`N repo(s) failed …`) so a
short or empty list isn't mistaken for "no work" when a repo is down;
stdout stays clean for piping. Same partial-failure honesty as `wyk
inbox` / `wyk stats`.


## Handing a task to a human (agent side)

For an agent that's just decided "this needs a human" (no bd issue
yet), file and hand off in one shot:

```bash
cat runbook.md | wyk handoff -create "Rotate staging DB password" -priority 1
# created would-you-kindly-63o — "Rotate staging DB password"
# handed would-you-kindly-63o to human (327-byte runbook)
```

For an existing issue:

```bash
cat runbook.md | wyk handoff wyk-42
# handed wyk-42 to human (327-byte runbook)
```

`wyk handoff` tags the issue with the `human` label and replaces its
description with the runbook from stdin (or `--file <path>`). With
`-create`, it runs `bd create` first (with `src:agent`) and uses the
new ID for the handoff. The runbook must say what kind of human task
it is: a **task** carries `## Steps` (the directions the human
follows), a **question** carries `## Question` (what the human is
asked to decide, with options and a recommendation). A runbook with
neither heading is refused; `wyk handoff -template [-question]`
prints the matching skeleton. Same
contract as the TUI's `H` key — see
[`docs/CONTRACT.md`](CONTRACT.md). Go programs can call
[`pkg/handoff.BounceToHuman`](../pkg/handoff/handoff.go) directly.


## Troubleshooting (`wyk doctor`)

```bash
wyk doctor
#   [PASS] bd binary on PATH
#          /Users/you/.local/bin/bd — bd version 1.0.4
#   [PASS] wyk binary on PATH
#          /Users/you/.local/bin/wyk
#   [PASS] wyk registry
#          ~/.config/wyk/repos.json — 2 repo(s) registered
#   [PASS] repo would-you-kindly: .beads/ present
#   [PASS] repo would-you-kindly: post-commit hook (chained)
#   [WARN] repo would-you-kindly: handoff-contract hygiene
#          1 human task(s) with an empty runbook [wyk-37] — the description IS the runbook; fill it via `wyk handoff <id>`
#   ...
#   doctor: OK
```

Checks the common friction points: bd and wyk on `PATH`, registry
parseable, each registered repo has `.beads/` and `.git/`, post-
commit hook is either wyk's (plain or chained) or flagged as foreign,
chained hook's `.pre-wyk` preservation file is intact.

It also reconciles each repo's open queue against the handoff contract
— surfacing human tasks with an empty runbook, human tasks missing a
`src:` provenance label, and agent tasks with no assignee (orphans) —
all computed from the issues it already fetched, so it costs no extra
bd calls.

Exit 0 on PASS or WARN-only, exit 1 if any FAIL.


## Stats

```bash
wyk stats          # human-readable counts + timing across all registered repos
wyk stats -json    # structured output for scripting
```

Aggregate snapshot: issue counts by status, currently human-flagged
(split by `src:agent` vs `src:human`), agent inbox count, closures in
the last 7/30 days, and median/p95 time-to-close for human-flagged
issues. Useful as a heartbeat for the handoff loop.


## Looking up the convention (agents start here)

```bash
wyk conventions          # human-readable agent-ready tip
wyk conventions -json    # structured for programmatic ingestion
```

If you're writing code that interacts with bd in a wyk-tracked repo,
this is the first thing to run. It documents the label set (`human`,
`src:agent`, and `agent-handoff` for another agent's work), the inbox
query, the preferred handoff command, and a concrete filing example.
`wyk init` also writes the same
convention into bd's `remember` store, so `bd prime` surfaces it on
every agent session start without an extra command.


## Mirroring handoffs into another task manager (handoff hook)

The human's side of the loop usually already lives somewhere — a to-do
app, a calendar, a channel. One config key makes wyk tell it:

```bash
wyk config set hooks.handoff.command 'node ~/Projects/basicdo/scripts/wyk-handoff-hook.mjs'
```

From then on every handoff (and bounce-back, and close of a
human-labelled issue) pipes a JSON description of the issue — runbook,
priority, due date, repo, who filed it — to that command. The command
may answer with a reference and a link; wyk stores them on the issue
(`external_ref` and a note) and sends the reference back on later
events so the mirror is never duplicated. Agent-owned issues never
reach the hook. `wyk handoff -dry-run` says whether it would fire, a
hook failure exits 3 with the replay command, and `wyk hook dispatch
<event> <id>` re-sends any event. Contract, payload, and how to write
an adapter: [`docs/HOOKS.md`](HOOKS.md). The first adapter is
BasicDo's.


## Picking up bounced-back work (agent inbox)

The other direction of the handoff loop: when a human presses `H` to
remove the `human` label, the issue lands in the agent's inbox.

```bash
wyk inbox          # human-readable list across every registered repo
wyk inbox -json    # structured output for an LLM to ingest
# > 1 issue(s) in inbox:
# >   would-you-kindly-037   P4  Configure production OAuth client
```

The canonical query is `label=src:agent AND NOT label=human AND NOT
label=agent-handoff AND status!=closed AND status!=blocked AND
status!=deferred AND status!=hooked` — things you (the agent) filed
that a human has touched but left open, minus another agent's work
(`agent-handoff`, and `hooked` — bd's attached-to-an-agent's-hook
status) and issues whose blocker is tracked elsewhere (`blocked` /
`deferred` reappear when their status returns to open). Use this at
the start of a session to find what you need to act on next.

### Several agents in one repo (`wyk next`, `wyk claim`)

When more than one agent works the same repo at once — each on its own
git branch — they need to see what is already checked out. A claim is a
**lease**: owned by the agent's identity (never a session), and expiring
unless renewed, so a dead session can't hold work forever.

```bash
wyk next -claim                      # pick the best unclaimed issue and check it out
# > claimed would-you-kindly-1k2j as claude-88ef57f5 until 2026-10-08T16:00:00-04:00 on feat/x
wyk claim -renew                     # keep everything I hold alive
wyk claim -release would-you-kindly-1k2j
```

Each Claude Code session claims as `claude-<session id>`, so concurrent
agents are told apart with no setup. An agent with a defined role can
name itself instead with `WYK_AGENT_IDENTITY=reviewer`; outside any
session, claims use bd's actor (git `user.name`).

`wyk next` ranks the issues you already hold, then your inbox, then
ready work and claims other agents abandoned. It never offers an issue
another identity holds a live lease on. `wyk claim <id>` on such an
issue exits 3 and names the holder. The lease lives on the issue as bd
metadata (`wyk.lease.owner`, `.until`, `.branch`) next to bd's own
assignee and `in_progress` status. Expiry is computed on read, with no
daemon. The default TTL is 2h (`wyk config set claim_ttl 90m`, or
`$WYK_CLAIM_TTL`), and the Stop hook below renews it on every turn. An
expired lease is claimable: taking it over leaves a note naming the
previous holder. In the TUI a live lease shows as `@<owner>` in the
Owner column and a lapsed one as `EXPIRED`. Full rules:
[`docs/CONTRACT.md`](CONTRACT.md) (wyk-contract/v4).

### Proactive nudge (opt-in Stop hook)

`wyk inbox` is pull: the agent has to think to run it. To make the
round-trip *push* — so a human's bounce-back reaches a working agent
without anyone re-prompting it — register `wyk hook agent-nudge` as a
Claude Code **Stop** hook. One command does it (idempotent;
`-uninstall` to remove, `-project` to scope to this repo instead):

```bash
wyk hook install-nudge   # adds the Stop hook to ~/.claude/settings.json
```

That writes the standard hook entry — equivalent to adding this by hand:

```json
{
  "hooks": {
    "Stop": [
      { "hooks": [ { "type": "command", "command": "wyk hook agent-nudge" } ] }
    ]
  }
}
```

When the agent finishes a turn, the hook checks the inbox and — if
items have appeared that it hasn't already surfaced **this session** —
blocks the stop and tells the agent to work them. Two guards keep it
from nagging: per-session dedup (each inbox item is surfaced at most
once per session, so a standing inbox doesn't re-fire every turn — it
nudges on *change*) and Claude's `stop_hook_active` flag (it never
blocks while already continuing from a Stop hook, so it can't loop).
It fails open: any bd hiccup just allows the stop. Dedup state lives in
`$XDG_STATE_HOME/wyk/agent-nudge/<session>.json`.

The same hook is the lease heartbeat: on every turn it renews the
leases the agent's identity holds, so a working session keeps its
claims and an abandoned one loses them one TTL later.

### Claude Code skills

`wyk` ships an installable family of
agent skills (`wyk`, `wyk-handoff`, `wyk-project-review`) embedded in the
binary — install them into `~/.claude/skills` with `wyk skills install`
so any Claude session loads them on demand. See
[`docs/SKILLS.md`](SKILLS.md) for the full list, the install-state
model, and how they stay in sync with the CLI.


## Auto-closing issues on commit (`wyk init`)

```bash
wyk init
# wyk init: installed post-commit hook at .git/hooks/post-commit
```

`wyk init` is the one-stop bootstrap for a repo. It runs `bd init` if
there's no bd workspace yet, installs the post-commit auto-close hook
into git's **active** hooks dir (following `core.hooksPath` so it lands
where git actually looks), registers the repo so the multi-repo TUI
finds it, and seeds the **agent enrichment**: a wyk conventions block in
the repo's `CLAUDE.md` (so agents know to file plans as bd issues via
`wyk create` and hand human-only work off with `wyk handoff`), plus a
**`bd-create-guard` PreToolUse hook** in `.claude/settings.json` that
redirects an agent's raw `bd create` to `wyk create` — so the session
actually gets recorded instead of relying on the agent reading the docs.
Every step is idempotent — re-running is safe. Add `-skills` to also
install the agent skills; `-skip-claude-md` (covers both enrichment
pieces) / `-skip-register` / `-skip-bd-init` / `-skip-hook` opt out of
individual steps.

**Registration is never gated on the hook.** If wyk can't install its
post-commit hook — another tool already owns it, or `core.hooksPath` is
stale — it warns, registers the repo anyway, and exits 0. That ordering
is load-bearing: registration is what makes a repo visible to `wyk
inbox`, the dashboard and the TUI, so a repo that fails to register is
one where `wyk handoff` reports success and no human can ever see the
result.

> **What it touches, and what the guard is.** `wyk init` writes *outside*
> `.beads/` by default: a `post-commit` git hook (chained after any
> existing hook — never clobbered; see `-chain`/`-force`), a conventions
> block in `CLAUDE.md`, and the `bd-create-guard` in `.claude/settings.json`.
> Preview all of it with `wyk init -dry-run`, or skip the agent-config
> edits with `-skip-claude-md`.
>
> The **`bd-create-guard` is a convention nudge, not a security control.**
> It steers an agent's `bd create` to `wyk create` so the Session column
> gets populated, but it is best-effort and trivially bypassable
> (`WYK_ALLOW_BD_CREATE=1`, a shell variable, a renamed binary, a different
> tool). Don't rely on it to sandbox what an agent can do to your bd data.

After `wyk init`, every commit whose message contains a
`Closes:`, `Fixes:`, or `Resolves:` trailer (case-insensitive) auto-
closes the referenced bd issue. Hierarchical IDs work too:

```
Closes: would-you-kindly-ma5.4
Fixes #bd-42
Resolves: my-project-abc
```

**One ID per line.** A trailer listing multiple IDs (`Closes: bd-1,
bd-2`) is rejected wholesale — use two separate `Closes:` lines.
This is deliberate; it avoids closing extras from prose like
`Closes: bd-1 (we'll handle bd-2 next week)`.

If the active `post-commit` hook already exists from another tool
(e.g. `roborev`, `husky`, `pre-commit`), a bare `wyk init` leaves it
alone, warns that auto-close won't run, and finishes the rest of the
bootstrap. To get the auto-close as well:

- `wyk init -chain` (recommended) — preserves the existing hook
  at `post-commit.pre-wyk` and writes a wrapper that runs both:
  the original first, then wyk's auto-close. Non-destructive.
- `wyk init -force` — overwrites the existing hook entirely.
  Destructive — only use if you don't need the other tool's hook.
- `wyk init -skip-hook` — don't touch hooks at all, and drop the
  warning. Register and enrich only.
- `wyk init -dry-run` — preview what any of these would do without
  writing anything.

Note that `bd init` sets `core.hooksPath` to `.beads/hooks`, so in a
beads repo this is where *any* other commit-hook tool's hook lives too —
"beads repo that also uses another hook tool" is the common case, not an
exotic one. `wyk init -fix-foreign-hooks` chains wyk after every foreign
hook across the registry in one go.

To uninstall a chained install: `rm .git/hooks/post-commit` and
(optionally) `mv .git/hooks/post-commit.pre-wyk .git/hooks/post-commit`
to restore the original.


## Keys

### Reading

| Key       | Action                                            |
| --------- | ------------------------------------------------- |
| `j` / `k` | Move down / up                                    |
| `g` / `G` | Top / bottom of the list                          |
| `]` / `[` | Next / previous human-flagged issue (wraps)       |
| `enter`   | Open the selected issue (read its instructions); in the split layout, focus the detail pane |
| `esc`     | Back to the list                                  |
| `p`       | Show / hide the detail pane (auto-on at 140×36+; persisted) |
| `/`       | Open the filter input (fuzzy title; substring on repo, branch, ID, body) |
| `@name`   | Expand a saved fuzzy filter (manage via `:filter`)|
| `h`       | Toggle the human-flagged view (press again to return)   |
| `tab`     | Cycle preset filters (all → ready → human → mine → blocked → review) |
| `C`       | Toggle "show closed" across all presets           |
| `1`–`4` / `0` | Cap the view at P0 / ≤P1 / ≤P2 / ≤P3; `0` clears the cap |
| `m`       | Toggle mouse capture (off = native click-drag text selection) |
| `s` / `S` | Cycle sort key / reverse the active direction     |
| `o`       | Column-visibility overlay (persists to `ui.json`) |
| `r`       | Refresh from bd now                               |
| `?`       | Open the help overlay                             |
| `q`       | Quit                                              |

### Writing

| Key   | Action                                                      |
| ----- | ----------------------------------------------------------- |
| `a`   | Close the cursor issue (asks `[y/N]` to confirm)            |
| `H`   | Toggle the `human` label on the cursor issue                |
| `n`   | Append a note to the cursor issue (multi-line textarea)     |
| `N`   | File a new issue in the cursor's repo (QuickAdd; refuses without an assignee) |
| `e`   | Edit the cursor issue's description in `$EDITOR`            |
| `L`   | Toggle an arbitrary label on the cursor issue               |
| `O`   | Change the cursor issue's assignee                          |
| `d`   | Defer the cursor issue (`bd update --defer`)                |
| `T`   | Cycle the cursor issue's type (task → bug → feat → …)       |
| `+`/`-` | Bump the cursor issue's priority                          |
| `v`   | Multi-select; bulk close / flag / defer the marked rows     |
| `u`   | Undo the last close (reopens via `bd reopen`; one-deep)     |
| `.`   | Repeat the last write action against the cursor row         |

### Clipboard & command palette

| Key   | Action                                                      |
| ----- | ----------------------------------------------------------- |
| `y`   | Yank the cursor row's ID                                    |
| `Y`   | Yank `ID — title`                                           |
| `*`   | Yank every visible row's ID, newline-separated              |
| `M`   | Yank the cursor row as a markdown task line (`- [ ] ID — title`) |
| `_`   | Yank every visible row as a markdown task list              |
| `:`   | Command palette — `:assign`, `:priority`, `:label`, `:filter list`/`remove`, `:bd <args>` |

After any write, the list refetches and a status banner appears
above the help bar (e.g. `closed wyk-42`, or `close wyk-42 failed: …`).

The detail view (`enter` on a row) shows the issue's full
description and any accumulated notes (added via `n` or `bd note`).
Notes lazy-load via a `bd show` call on entry, so the section
appears a beat after the rest of the detail view. In the detail
view, `c` copies the instructions (the description body) to the
clipboard.

On a terminal of at least 140×36 the detail is a **pane** next to the
list instead of a separate screen (the layout roborev's TUI uses): it
follows the cursor as you move — the row paints immediately, the
notes and dependency edges arrive after a short debounce so holding
`j` doesn't fan out a `bd show` per row. `enter` focuses the pane (all
the detail keys work there), `esc` returns to the list, and `p` hides
or shows the pane; the choice persists in `state.json`. While the list
has focus the wheel scrolls whichever pane the pointer is over;
focusing the pane releases mouse capture so you can select text.

The list also refreshes itself every 20 seconds. On platforms with
fsnotify support, external `bd` writes (a `git pull`, another `wyk`
instance, the post-commit hook auto-closing) trigger an immediate
refresh instead of waiting for the next tick.


## Customizing colors

Drop a `~/.config/wyk/theme.json` (or `$XDG_CONFIG_HOME/wyk/theme.json`)
to override any subset of the built-in lipgloss styles:

```json
{
  "human_badge_bg": "#ff66cc",
  "agent_badge_bg": "78",
  "claimed_badge_bg": "37",
  "status_open":    "39"
}
```

Empty or missing keys fall through to the defaults. Colors accept ANSI
256 codes (`"212"`) or hex literals (`"#ff66cc"`). Disable styling
entirely with `NO_COLOR=1` (or `WYK_NO_COLOR=1`), or pass `--no-color`
on the command line for the same effect.


## Other subcommands

```bash
wyk create   <bd create args...>                                        # file an issue + stamp the Claude session
wyk activity [-since 24h] [-priority N] [-repo name] [-status open|closed|all] [-json]
wyk export   [-since 24h] [-compact] [-repo name]                        # JSON dump
wyk import   [-file path] [-dry-run] [-repo name]                        # restore from a dump
wyk dashboard [-json] [-days N] [-repo name]                             # per-repo rollup
wyk depgraph [-repo name] [-json]                                        # dependency tree
wyk skills   <list|install|uninstall|print>                             # agent skills for Claude Code
wyk config   <get|set|list> [key] [value]                                # ~/.config/wyk/config.json
wyk bugreport [-json]                                                    # redacted environment dump for a bug report
wyk conventions [-json]                                                  # the label contract, agent-readable
wyk hook     <post-commit|agent-nudge|bd-create-guard|install-nudge>     # git / Claude Code hook entry points
wyk update   [-y] [-channel any|stable] [-dry-run]                       # self-update via go install
wyk completion <bash|zsh|fish>                                           # shell completion
wyk help [--markdown]                                                    # keymap reference
wyk version [--check]                                                    # 0 current / 1 newer / 2 net err
```

The full CLI reference is generated at
[`docs/generated/cli.md`](generated/cli.md) and the TUI keymap at
[`docs/generated/keymap.md`](generated/keymap.md).

`wyk inbox -priority N -repo <name>` and `wyk handoff -note <text>`
extend the existing inbox/handoff commands. Every subcommand that
emits structured data accepts `-json`; the JSON shapes are stable
across patch releases.


## A day in the life

The product's whole point is the round-trip between an agent and a
human. Here's what that looks like end-to-end across two sessions.

**Morning, in your editor — an agent is doing work.** It hits
something it can't do alone (rotate a secret, click "Publish" on a
release, decide which legal entity signs a contract). It files a bd
issue and hands it off in one shot:

```bash
$ echo "1. ...  2. ...  3. Close this issue when done." \
    | wyk handoff -create "Rotate the staging DB password" -priority 1
created would-you-kindly-2oa — "Rotate the staging DB password"
handed would-you-kindly-2oa to human (87-byte runbook)
```

The agent moves on to other work. The bd issue now carries `human`,
`src:agent`, and the runbook as its description.

**Afternoon, at your terminal.** You run `wyk` (or it's already
open). Press `h` to jump to the human view:

```
Owner   Repo               Branch  ID                     Type  Status  Priority  Updated  Session   Title
HUMAN   would-you-kindly   main    would-you-kindly-2oa   task  open    P1        3h ago   5a5b8a48  Rotate the staging DB password
HUMAN   acme-pipeline      feat/x  acme-pipeline-mc42     bug   open    P0        1h ago             Latest broken
```

On a terminal of 140×36 or larger the list takes the left pane and the
selected issue's runbook renders in a right pane that follows the
cursor (`p` hides it) — so reading a handoff is a `j`, not a `⏎`/`esc`
round-trip.

(The `HUMAN` badge is rendered plain regardless of who filed the issue —
`src:agent` vs `src:human` is still in the row's labels, but the badge
itself stays uniform so the eye reads it as one thing.)

**The columns, left to right.** Every column is shown below; the `o`
overlay toggles any of them off (persisted to `ui.json`), and on a
narrow terminal the lower-value ones auto-hide to keep rows intact.
`Owner`, `ID`, `Priority`, and `Title` are always shown.

| Column     | Definition                                                                                                              |
| ---------- | ----------------------------------------------------------------------------------------------------------------------- |
| `Owner`    | Whose move it is. `HUMAN` — a human must act. `AGENT` — agent-owned; the default for any issue without a `human` label. `HUMAN-BLOCK` — an agent issue blocked by a human-flagged dependency. `AGENT-HANDOFF` — another agent is working it, so this agent must not interfere (a human orchestrates the coordination). `@<owner>` — checked out right now by that agent identity (`wyk claim`). `EXPIRED` — its holder stopped renewing, so it's claimable. Never blank. |
| `Repo`     | The registered bd workspace the issue lives in. Shown when the view spans more than one repo.                            |
| `Branch`   | That repo's current git branch. Shown in multi-repo mode.                                                                |
| `ID`       | The full bd issue ID (e.g. `would-you-kindly-2oa`) — the same string bd and your agents use, so a quoted ID matches a row on sight. The column sizes itself to the longest ID in view. Yank it with `y`. |
| `Type`     | The issue type, abbreviated to four characters: `task`, `bug`, `feat`(ure), `chor`(e), `epic`, `deci`(sion), `spik`(e), `stor`(y), `mile`(stone). |
| `Status`   | The bd lifecycle state: `open`, `wip` (in&#95;progress), `hooked`, `blocked`, `deferred`, `pinned`, `closed`. Closed rows are dimmed; the frozen states (`deferred`, `pinned`) render de-emphasized. |
| `Priority` | `P0` (most urgent) through `P4` (backlog). Bump with `+` / `-`; opt into colour emphasis in `ui.json`.                   |
| `Updated`  | How long since the issue last changed — `now`, `3h ago`, `2d ago`, or a `Jan 2` date once it's older than 30 days.       |
| `Session`  | The Claude session that filed the issue (first 8 chars of the ID), recorded when it's created via `wyk create`. Blank for issues filed any other way. |
| `Title`    | The issue's one-line summary, capped at 50 columns for scannability. Press `enter` to read the full title, description, and notes. |

Press `enter` to read the runbook, `a` to close when done, or `H` to
bounce it back to the agent if the next step is theirs again. The
list refreshes every 20 seconds and across every repo you've
registered with `wyk init`.

**Next morning, in your editor.** Your agent starts a session and
checks its inbox:

```bash
$ wyk inbox -json
[
  {"id":"would-you-kindly-2oa","title":"Rotate the staging DB password",
   "labels":["src:agent"], "status":"open", ...}
]
```

These are issues the agent filed (`src:agent`) that no longer carry
`human` — i.e. you handled the runbook step, removed the label, and
now expect the agent to do the next thing. The agent picks them up,
re-applies `human` if another round is needed, or closes when fully
done.

The label flips trace the conversation. The TUI is the human's
window into it; `wyk handoff` and `wyk inbox` are the agent's.


## Screenshots

The GIF at the top of the README is the agent↔human round-trip: an
agent files a runbook with `wyk handoff`, the human jumps to the
**human view** (`h`), reads the runbook in the detail pane, and closes
it (`a`) — the queue clears. Regenerate it (and the still below) with
`bash docs/screenshots/render-demo.sh` (from the repo root); the script seeds a throwaway bd
workspace and isolates every wyk config/cache dir so nothing private
can leak into the capture.

![wyk split layout](screenshots/wyk-tui.png)

The default `all` preset in the split layout. The **Owner** column
carries the "whose move is it" badge — `HUMAN` for handed-off work,
`AGENT` for issues the agent filed, `HUMAN-BLOCK` for agent issues
blocked on a human — and the right pane is the selected issue's
runbook, following the cursor.


## Status

`wyk` is actively developed. See the
[CHANGELOG](../CHANGELOG.md) and the
[GitHub releases](https://github.com/jimbottle/would-you-kindly/releases)
for what's shipped and when. It's a CLI-driven toolkit around the
human-in-the-loop handoff contract: the multi-repo TUI plus the
subcommands listed in [`docs/generated/cli.md`](generated/cli.md),
agent skills installable into Claude Code, a `theme.json` color overlay
with light/dark adaptation, and `NO_COLOR` support.

### Platforms

Developed and used on **macOS** and **Linux**; CI builds and runs the
race-tested suite on both. Windows is not currently tested or supported —
the instant-refresh `fsnotify` path in particular hasn't been exercised
there (the tool degrades to the 10-second polling refresh if the watcher
can't start, so it may still work, but it's unverified).

`go install` builds against the toolchain in `go.mod`; on an older Go it
will download the matching toolchain automatically (Go 1.21+ with the
default `GOTOOLCHAIN=auto`).

### What `wyk` is not

- **Not a replacement for `bd`.** wyk shells out to bd for all storage and
  never touches the database directly; bd remains the source of truth.
- **Not a server.** It's a local, single-user developer tool with no
  network service of its own — the only outbound call is an opt-in GitHub
  release check for `wyk update`.
- **Not a general project-management UI.** The scope is the agent ↔ human
  handoff loop and the views that serve it, not sprints, boards, or
  time-tracking.
