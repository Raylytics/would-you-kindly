# wyk Makefile. `make check` is the one local gate that mirrors the
# `test` CI workflow — run it before pushing. The rest are the pieces
# it composes plus tasks Go's default tooling doesn't cover: the
# committed doc snapshots the would-you-kindly.raylytics.io docs agent
# reads, and the plugin-skills sync.

.PHONY: docs-snapshot docs-check plugin-skills plugin-skills-check backlog-snapshot backlog-snapshot-check lint vuln check help

# Scrubbed, read-only snapshot of this repo's bd tracker for GitHub
# visitors (docs/backlog-snapshot.md). Regeneration needs bd, so it is
# local-only; the check lints the committed file for anything the scrub
# must never let through and runs in `make check` + CI.
backlog-snapshot:
	@./scripts/backlog-snapshot.sh

backlog-snapshot-check:
	@./scripts/backlog-snapshot-check.sh

# Tool versions are pinned once, in .tool-versions (mise/asdf format), and
# read here and by .github/workflows/ci.yml, so `make lint` and CI run the
# same golangci-lint against the same Go toolchain. Bump .tool-versions
# (and the `go` line in go.mod for Go) in one commit.
GO_VERSION := $(shell awk '$$1 == "golang" {print $$2}' .tool-versions)
GOLANGCI_VERSION := v$(shell awk '$$1 == "golangci-lint" {print $$2}' .tool-versions)

# govulncheck version CI pins (.github/workflows/ci.yml). Bump here AND in
# ci.yml together. `make check`
# runs it because CI does — a scan that only ran in CI let a stale local
# toolchain (go.mod's bare `go 1.26` satisfied by an old 1.26.x) carry
# called stdlib CVEs through a green local gate (would-you-kindly-351g).
GOVULNCHECK_VERSION := v1.3.0

# Regenerate the markdown snapshots under docs/generated/. Build a
# fresh binary into /tmp so this target works from a dirty tree
# without polluting ./bin or relying on `go run` per call (which
# re-compiles every invocation).
docs-snapshot:
	@mkdir -p docs/generated
	@go build -o /tmp/wyk-docgen ./cmd/wyk
	@/tmp/wyk-docgen help --markdown > docs/generated/keymap.md
	@/tmp/wyk-docgen help --cli-markdown > docs/generated/cli.md
	@rm -f /tmp/wyk-docgen
	@echo "docs-snapshot: docs/generated/{keymap.md,cli.md} regenerated"

# Drift check: regenerate the snapshots and fail if any committed
# file changed OR if a snapshot file is untracked. Use
# `git status --porcelain` rather than `git diff --quiet` because
# the latter only surfaces modifications to tracked files — a
# future docs-snapshot that emits a new file would silently pass
# the diff check while the file sits uncommitted.
docs-check: docs-snapshot
	@status=$$(git status --porcelain -- docs/generated/); \
	if [ -n "$$status" ]; then \
		echo "docs-check: docs/generated/ is stale — run 'make docs-snapshot' and commit the result"; \
		echo "$$status"; \
		git diff -- docs/generated/; \
		exit 1; \
	fi
	@echo "docs-check: docs/generated/ is up to date"

# Sync the Claude Code plugin's bundled skills from the embedded
# source of truth (internal/skills/data). The plugin under plugin/ ships
# real SKILL.md files because a marketplace install can't reach into the
# wyk binary — so they're committed copies. This target regenerates them;
# TestPluginSkillsMatchEmbedded fails if a copy drifts from the embedded
# content.
plugin-skills:
	@rm -rf plugin/skills
	@for d in internal/skills/data/*/; do \
		name=$$(basename $$d); \
		mkdir -p plugin/skills/$$name; \
		cp $$d/SKILL.md plugin/skills/$$name/SKILL.md; \
	done
	@echo "plugin-skills: plugin/skills/ synced from internal/skills/data/"

# Drift check mirroring docs-check: regenerate the bundled skills and
# fail if anything changed or is untracked.
plugin-skills-check: plugin-skills
	@status=$$(git status --porcelain -- plugin/skills/); \
	if [ -n "$$status" ]; then \
		echo "plugin-skills-check: plugin/skills/ is stale — run 'make plugin-skills' and commit"; \
		echo "$$status"; \
		exit 1; \
	fi
	@echo "plugin-skills-check: plugin/skills/ is up to date"

# golangci-lint at the version .tool-versions pins (.golangci.yml: govet,
# staticcheck, errcheck, ineffassign, unused), run under the Go toolchain
# .tool-versions pins. `go run …@version` builds the linter once (cached)
# with exactly that toolchain, so a newer local Go cannot make the linter
# fail to read the stdlib export data, which is what broke `make lint` on a
# Go 1.27 workstation while CI (pinned) stayed green. GOTOOLCHAIN makes
# every nested `go` call use the pinned version too. The pre-commit hook
# runs this same target.
lint:
	@echo "==> golangci-lint $(GOLANGCI_VERSION) (go$(GO_VERSION))"
	@GOTOOLCHAIN=go$(GO_VERSION) go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION) run

# govulncheck at the CI-pinned version. `go run …@version` caches the
# binary after the first run, but every run fetches the vulnerability
# database from vuln.go.dev, so this target — and therefore `make check`,
# which ends with it — needs network access (`-db file://…` would point
# it at a local copy). Reports vulnerabilities the code actually CALLS,
# in dependencies and the Go standard library — which is why go.mod
# pins the patched toolchain: the stdlib govulncheck sees is the one
# that built the binary.
vuln:
	@echo "==> govulncheck"; go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

# The full local gate. Runs the same checks as the `test` CI workflow,
# in the same order, so a green `make check` means a green push — the
# missing link that let a golangci-lint failure ship unnoticed. Also runs
# the plugin-skills drift guard (a local check CI doesn't yet run).
check:
	@echo "==> gofmt"; out=$$(gofmt -l .); if [ -n "$$out" ]; then \
		echo "gofmt: non-conforming files (run \`gofmt -w .\`):"; echo "$$out"; exit 1; fi
	@$(MAKE) --no-print-directory docs-check
	@echo "==> go vet"; go vet ./...
	@$(MAKE) --no-print-directory lint
	@echo "==> go build"; go build ./...
	@$(MAKE) --no-print-directory plugin-skills-check
	@$(MAKE) --no-print-directory backlog-snapshot-check
	@echo "==> go test -race"; go test -race -timeout 5m ./...
	@$(MAKE) --no-print-directory vuln
	@echo "check: all local gates passed (mirrors the test CI workflow)"

help:
	@echo "Targets:"
	@echo "  check                run the full local gate (mirrors the test CI workflow) — use before pushing"
	@echo "  lint                 golangci-lint run (CI-pinned version)"
	@echo "  vuln                 govulncheck ./... (CI-pinned version; run by CI + make check)"
	@echo "  docs-snapshot        regenerate docs/generated/{keymap.md,cli.md}"
	@echo "  docs-check           fail if docs/generated/ is stale (run by CI + make check)"
	@echo "  plugin-skills        sync plugin/skills/ from internal/skills/data/"
	@echo "  plugin-skills-check  fail if plugin/skills/ is stale (run by make check)"
	@echo "  backlog-snapshot     regenerate docs/backlog-snapshot.md from the local bd tracker (scrubbed)"
	@echo "  backlog-snapshot-check  fail if docs/backlog-snapshot.md leaks a scrubbed field (run by CI + make check)"
