# Project Instructions for AI Agents

This file provides instructions and context for AI coding agents working on this project.

**Task tracking + workflow:** this project uses **bd (beads)** — run `bd prime`
for the full command reference, session-close protocol, and persistent
memories. (The SessionStart/PreCompact hooks in `.claude/settings.json` load it
automatically.) Use `bd` for all task tracking (not TodoWrite/markdown TODOs)
and `bd remember` for persistent notes (not MEMORY.md). The wyk-specific
conventions below are the delta on top of that. If `bd list` here
warns `no beads configuration found` or shows no issues, the working
tree lost `.beads/metadata.json`: run `wyk doctor` and paste its
restore command rather than trusting the empty result.

> ## Turn column: HUMAN / AGENT / WAITING-ON-HUMAN / OTHER-AGENT
> The TUI **Turn column** (formerly "Owner") shows whose move it is, driven by labels (NOT
> bd's `owner`/`assignee` fields, which are out of scope here):
> - `human` label → **HUMAN** (a human needs to act).
> - `agent-handoff` label → **OTHER-AGENT** badge (another agent is working
>   this; THIS agent must not interfere — a human orchestrates the
>   coordination). Excluded from the agent inbox so the "work it"
>   imperative doesn't fire on it.
> - agent task blocked by an open human task → **WAITING-ON-HUMAN**
>   (formerly HUMAN-BLOCK).
> - everything else → **AGENT**. A null owner (no `src:`/`human` label)
>   **defaults to AGENT**, so the column is never blank.
>
> Because a null owner silently becomes AGENT, the one thing that matters
> is: **a task that needs a human MUST be handed off** — `wyk handoff <id>`
> or `wyk handoff -create "…"` (sets the `human` label → HUMAN). Otherwise
> it defaults to AGENT and the human never sees it. Agent-filed work may
> pass `--labels src:agent` to be explicit, but it defaults to AGENT
> anyway. `-a`/`--claim` are bd's assignee/status — they do NOT set the
> badge.
> - This has been missed repeatedly; treat it as load-bearing.

## Build & Test

```bash
make check                    # the full local gate — run this before pushing
```

`make check` mirrors the `test` CI workflow exactly (gofmt, docs-check,
`go vet`, **golangci-lint**, build, `test -race`, **govulncheck**) plus
the plugin-skills drift guard, so a green `make check` means a green
push — provided your golangci-lint matches CI's pinned version (`make
lint` warns if it doesn't). The Makefile exports `GOTOOLCHAIN` from
go.mod, so a newer Go on PATH (Homebrew) no longer breaks lint with
export-data or 'requires newer Go version' errors; set `GOTOOLCHAIN`
yourself only to override it. It exists because
the linter gate (golangci-lint / staticcheck) used to run only in CI — a
red `test` workflow shipped unnoticed for a long time because local runs
skipped it. Don't push on a bare `go test`; run `make check`.

Individual pieces, when you want them à la carte:

```bash
go build -o ./bin/wyk ./cmd/wyk   # produce the CLI binary
go build ./...                # all packages compile
go test ./...                 # full suite (no race)
go test -race ./...           # race detector (load-bearing for MultiBDSource)
go vet ./...
make lint                     # golangci-lint at the CI-pinned version
make vuln                     # govulncheck at the CI-pinned version (go.mod pins the patched toolchain it scans)
```

One CI workflow (`test`) runs per push — the full gate above, on both
ubuntu and macOS (static gates run once, on Linux).

Most bd-shelling code uses a swappable runner field so tests can
inject a stub command without needing a real `bd` binary on PATH —
mimic that pattern (e.g. `beads.Client`'s `runner`, `runScanAndRegister`'s
`probeBDFunc`) when adding new bd-dispatching code.

## Architecture Overview

- **`cmd/wyk`** — the CLI binary and subcommand dispatchers. Each
  subcommand (the `subcommandHandlers` map in `main.go` is the list;
  `docs/generated/cli.md` is the reference) owns its own
  `flag.FlagSet`; `main.go` switches on `os.Args[1]` before parsing
  top-level flags. The TUI starts when no subcommand matches.
- **`internal/beads`** — typed wrapper over the `bd` CLI. `Client` shells
  out via a swappable runner; methods (`Query`, `List`, `Ready`, `Show`,
  `Create`, `Close`, `AddLabel`, `RemoveLabel`, `Note`, `UpdateDescription`)
  parse bd's JSON. Never reads bd's storage directly.
- **`internal/tui`** — the Bubble Tea Model + the `Source` / `Mutator` /
  `Detailer` / `MultiSource` interfaces it consumes. `BDSource` wraps one
  workspace; `MultiBDSource` parallel-fetches across many and unions the
  result. Per-sub errors travel atomically on `fetchedMsg`. `split.go`
  is the roborev-style list + detail-pane layout (auto at 140×36, `p`
  toggles); it reuses the stacked view's chrome builders, so a new
  banner or prompt only needs adding in `viewList`/`chromeExtra` once.
- **`internal/registry`** — JSON-backed registry of bd workspaces at
  `~/.config/wyk/repos.json` (XDG-aware). `Load` / `Save` / `Add` /
  `Remove` / `RemoveByName` / `Has`. Save writes atomically via
  temp-file + rename.
- **`internal/filter`** — preset → bd-query mapping (`all`, `ready`,
  `human`, `mine`, `blocked`, `review`) plus the keymap's preset-cycle
  order.
- **`pkg/handoff`** — the `BounceToHuman` helper that filed bd issues use
  to transition agent → human (adds `human` label, replaces description
  with the runbook). External programs can call this directly.

## Conventions & Patterns

**bd writes must pass `--dolt-auto-commit=on`.** bd defaults to leaving
writes in Dolt's working set; without the flag, `bd dolt push` later
can't see them. Every `Client` write method already does this — when
shelling out from a script/test, do the same.

**The handoff contract.** A task is for a human when it carries the
`human` label; its description IS the runbook the human follows;
`src:agent` / `src:human` labels record who filed it. See
`docs/CONTRACT.md` for the full text. New code that changes who's
holding the work should obey this contract — adding/removing `human`,
preserving the source label.

**Source / Mutator / Detailer.** Three small interfaces in
`internal/tui` segment the TUI's read, write, and lazy-detail needs.
Implementations can opt into any subset; the model runtime
type-asserts. `MultiSource` is a fourth (optional) interface that lets
the model render per-sub fetch failures.

**Commit conventions.** Conventional Commits 1.0 as the Version control
section below spells out (all eleven types, a scope such as `feat(tui):` or
`fix(doctor):`, subject at most 72 characters, enforced by the commit-msg hook).
Issues are closed via `Closes: <id>` trailers (one ID per line — the
auto-close hook deliberately rejects multi-ID lines, see the auto-close section of docs/OPERATOR.md).

**Versioning (pre-1.0).** On `0.y.z`, default to a **patch** bump
(`0.y.Z`) — fixes, additive flags, refactors, docs/CI, small UX tweaks.
Bump the **minor** (`0.Y.0`) only for a new subcommand / substantial
capability, a breaking flag / output / contract change, or something a
user must read the release notes to adopt. When in doubt, patch — the
minor digit tracks milestones, not activity. Full policy in CONTRIBUTING.md.

**Trunc is rune-aware** (`internal/tui.trunc`) — width semantics
throughout the TUI are visual, not byte. Don't reach for byte-level
slicing for column widths.

**Registry path normalisation.** `internal/registry.normalizePath`
resolves symlinks before dedup so the same workspace can't register
twice via macOS's `/var` → `/private/var` shortcut or any user
shortcut symlink.

**Feedback on wyk's experience IS wyk product feedback.** When the
user comments on the TUI, the CLI, the handoff flow, or any wyk
surface ("the headers don't show", "the W column is unclear", "this
should be deferred not held open"), the right first move is to file
a bd issue capturing the improvement, then handle the immediate
symptom. Don't stop at patching the one-off case — every piece of
friction the user reports has either revealed a real product gap or
a missing piece of convention documentation, and both deserve
durable fixes. The implicit rule the user follows: the agent's
behaviour against wyk is data about wyk itself.

**Every task has an owner.** bd auto-sets `owner` (who filed it) but
NOT `assignee` (who's responsible for doing it), and there's no bd
validation to require one — so set the assignee yourself on every
`bd create`. Don't file orphan tasks. Claim it (`wyk claim <id>`, an
expiring lease that sets assignee + `in_progress`, so concurrent agents
see it's taken) if you're starting now, or `bd create … -a <assignee>` to assign it
without starting. A human task is owned through `wyk handoff`. When
you review the backlog, treat a missing assignee as a defect to fix.

**Status lifecycle.** When filing or updating bd issues from this
project, follow the lifecycle documented in `docs/CONTRACT.md` —
default to `open`, reach for `deferred` when the blocker is "the
rest of the project hasn't caught up yet", `blocked` (with
`--add-dependency`) when the blocker is another tracked issue, and
the `human` label + handoff when a human is genuinely required.
Holding-open a task that depends on an unstable subsystem clutters
the ready view and creates false signals about what someone could
start now.

**Act on the agent inbox.** If `wyk inbox` returns items at any
point in a session, the default move is to WORK them — they're
tasks the human has bounced back, meaning whatever unblocker the
agent specified has arrived. Treating inbox items as "things to
handle later" defeats the round-trip. Exception: the user is
mid-conversation about something explicitly urgent, or the
expected unblocker artifact is actually missing (re-flag `human`
with a note explaining what's still needed; don't sit silently).

## Secrets

**Every credential an agent uses lives in 1Password.** API keys, tokens, DSNs, OAuth client secrets, webhook URLs, signing keys: if Claude Code, a cron job, an MCP server, or CI needs it, 1Password is the source of truth, and anything else (a local `.env`, SSM, GitHub Secrets, a server-side env file) is a derived copy seeded from it. This applies to the credentials this repo already uses, not only to new ones.

- **Reference, don't copy.** Tracked files carry env var *names* and `op://<vault>/<item>/<field>` references, never values. Load at run time with `op run --env-file=op.env -- <cmd>` (an `op.env` of `op://` lines, gitignored) or `op read "op://..."` piped straight into the consumer.
- **Name items `<repo>-<service>`** (for example `mc-epn`), one item per service, laid out as "How an item is laid out" below says. Use the 1Password account that owns the work (personal `my.1password.com` for this repo) and pass `--account` when more than one is signed in.
- **A literal `.env` is debt, not a pattern.** It must be gitignored and `chmod 600`, and the inventory below marks it as pending migration. Move the values into 1Password the next time you touch them.
- **Never print a secret.** Prove a key works by calling the API. Don't `cat` env files, echo variables, or paste values into transcripts, commit messages, or this file.
- **Found a literal secret in a tracked file or in history?** Stop and tell the owner. It needs rotating and purging, and that is their call.
- **Adding a credential:** create the 1Password item, add the `op://` line to `op.env.example` (or this repo's equivalent template), add a row below, and confirm the local copy is ignored.

### How an item is laid out

An audit on 2026-11-12 found 19 API Credential items in five shapes: secrets in plain-text fields, in a dropdown field, under ad-hoc names, and across four vaults. One shape from here on:

- **Category: API Credential.** Not Login, not Secure Note.
- **One secret → the built-in `credential` field.** It is the concealed field the template already has, so the app shows the item as holding a secret. Reference: `op://<vault>/<item>/credential`. Map it to the env var name in `op.env`, not in the item (`HOMEBREW_TAP_GITHUB_TOKEN=op://Personal/wyk-homebrew-tap/credential`).
- **Several values for one service → one field per value, labelled exactly as the env var** (`PAYCOR_CLIENT_ID`, `PAYCOR_CLIENT_SECRET`), and leave `credential` empty. Reference: `op://<vault>/<item>/<ENV_VAR>`.
- **Every secret is in a concealed (password-type) field.** Never a text field, a dropdown, `username`, or the notes: those show the value in plain text, and a dropdown-type field does not show as a secret at all (the first `wyk-homebrew-tap` held its token that way and looked empty). Non-secret values (a base URL, a key id, an account name) are plain text fields.
- **A token is one value.** A GitHub personal access token, for instance, has no separate "secret": the token goes in `credential` and nothing else is needed.
- **Record what it is.** `username` = the account that owns the credential; the expiry date in the item and in the table below; in the notes, what it grants and the page it was issued from.
- **Check the shape without reading the value:** `op item get <item> --account my.1password.com --format json | python3 -c 'import json,sys; [print(f.get("label"), f.get("type"), "set" if f.get("value") else "empty") for f in json.load(sys.stdin)["fields"]]'` — the secret's row must say `CONCEALED set`.
- **Seed a derived copy by piping, never by pasting:** `op read "op://<vault>/<item>/credential" --account my.1password.com | gh secret set <NAME> --repo <owner>/<repo>`. Then prove it works by calling the service.
- **Replacing one:** create the new credential, put it in the same field of the same item (1Password keeps the old version), re-seed every derived copy listed in the table, verify, then revoke the old credential at the issuer.

### Credentials this repo uses

| Env var | Used by / purpose | Where it lives today | 1Password item |
|---|---|---|---|
| `HOMEBREW_TAP_GITHUB_TOKEN` | `.github/workflows/release.yml`: goreleaser pushes the updated cask to the Homebrew tap on a tag; the first step of that workflow proves the token can read and push | GitHub Actions secret on this repo (CI only; no local copy), seeded from the item | `op://Personal/wyk-homebrew-tap/credential`: a fine-grained PAT with Contents read and write on `raylytics/homebrew-wyk`. Expires 2027-11-12 |

Not secrets: `GITHUB_TOKEN` in that workflow is the ephemeral `github.token`; the `WYK_*`, `XDG_*`, `BEADS_ACTOR`, and `EDITOR` env vars are plain config.

## Version control

This repo follows the Raylytics Version Control Standard, tier 1, stack go. Source: STANDARD.md in the `raylytics` repo (2026-10-27); cite its section in a commit body when a rule decides something.

- Branch from `main` as `type/short-kebab`. Every change lands as a squash-merged PR with CI green; the PR title is the Conventional Commit subject. Agent work always goes through a PR.
- Commits follow Conventional Commits 1.0: `type(scope): subject`, subject at most 72 characters, imperative, no trailing period, then a body that says why. Types: feat, fix, perf, refactor, style, docs, test, build, ci, chore, revert. Scopes here: tui, cli, doctor, init, hooks, updater, registry, skills, plugin, docs, ci, deps, release, vc. Keep the `Closes: <bd-id>` trailer convention above; it is a footer, not a byline.
- Never add `Co-Authored-By`, `Generated with`, or any AI byline; the commit-msg hook refuses them. Never use `--no-verify`. Never force-push `main`.
- Secrets never enter the tree; `.env.example` lists the variables; gitleaks runs in pre-commit and CI.
- Commands: `pre-commit install --hook-type pre-commit --hook-type commit-msg` once; lint `make lint`; test `go test -race ./...` (`make check` is the full local gate); release `scripts/release.sh X.Y.Z` (no version file: the tag is the version, goreleaser injects it; the script regenerates CHANGELOG.md with git-cliff, lands `chore(release): vX.Y.Z` through an auto-merged PR because `main` takes PRs only, then pushes the signed tag at the merge commit; `RELEASE_DRY_RUN=1` previews the section).
- Do not commit `.claude/settings.local.json`, `QUEUE.md`, `HANDOFF*`, `.beads/`, scratch notes or session logs.
