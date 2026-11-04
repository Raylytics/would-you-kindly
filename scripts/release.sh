#!/usr/bin/env bash
# Raylytics VC Standard, scripts/release.sh (STANDARD.md §7), with this
# repo's adaptation for a `main` that only accepts pull requests
# (would-you-kindly-g4j8; to be folded back into the standard).
# Usage: scripts/release.sh X.Y.Z   (run from the repo root, on main)
#        RELEASE_DRY_RUN=1 scripts/release.sh X.Y.Z   prints the CHANGELOG
#        section it would commit and touches nothing.
# 1. Refuses unless on main, clean, in sync with origin, gh is authenticated,
#    and the last ci run on main is green. 2. Bumps the stack's
#    source-of-truth version file (and the React Native derived copies).
# 3. Prepends the release to CHANGELOG.md with git-cliff and folds any
#    hand-written "## [Unreleased]" notes under it (cliff --prepend would
#    otherwise leave them orphaned below the new header). 4. Commits
#    chore(release): vX.Y.Z on a chore/release-vX.Y.Z branch, opens the PR,
#    arms squash auto-merge, waits for CI to merge it, then creates the signed
#    annotated tag at the merge commit and pushes it. release.yml then creates
#    the GitHub Release. The main ruleset has no bypass actors, so a direct
#    `git push origin main` from this script is rejected — hence the PR.
# Stacks: python (pyproject.toml), extension (manifest.json), react-native
# (package.json + android/ + ios/), js (package.json), android
# (app/build.gradle(.kts)), go (tag only; goreleaser builds).
set -euo pipefail

VERSION="${1:?usage: $0 X.Y.Z}"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$ ]] || { echo "not a SemVer version: $VERSION" >&2; exit 2; }
TAG="v$VERSION"

die() { echo "release.sh: $*" >&2; exit 1; }

# ---- 1. preconditions -------------------------------------------------------
[ "$(git rev-parse --abbrev-ref HEAD)" = main ] || die "not on main"
[ -z "$(git status --porcelain)" ] || die "working tree not clean"
git fetch -q origin main
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || die "main is not in sync with origin/main"
git rev-parse -q --verify "refs/tags/$TAG" >/dev/null && die "tag $TAG already exists"
git ls-remote --exit-code --tags origin "refs/tags/$TAG" >/dev/null 2>&1 && die "tag $TAG already exists on origin"
BRANCH="chore/release-$TAG"
git rev-parse -q --verify "refs/heads/$BRANCH" >/dev/null && die "branch $BRANCH already exists"
command -v gh >/dev/null || die "gh is not on PATH"
gh auth status >/dev/null 2>&1 || die "gh is not authenticated"
command -v git-cliff >/dev/null || die "git-cliff is not on PATH"
if [ -f .github/workflows/ci.yml ]; then
  conclusion="$(gh run list --branch main --workflow ci.yml --limit 1 --json conclusion --jq '.[0].conclusion' 2>/dev/null || echo unknown)"
  [ "$conclusion" = success ] || die "last ci run on main is '$conclusion', not success"
fi
if [ -f CHANGELOG.md ] && grep -q "^## \[$VERSION\]" CHANGELOG.md; then die "CHANGELOG.md already has an entry for $VERSION"; fi

# ---- 2. bump ----------------------------------------------------------------
detect_stack() {
  if [ -f go.mod ]; then echo go
  elif [ -f pyproject.toml ]; then echo python
  elif [ -f manifest.json ] && [ -f package.json ]; then echo extension
  elif [ -f manifest.json ]; then echo extension
  elif [ -f package.json ] && { [ -d android ] || [ -d ios ]; }; then echo react-native
  elif [ -f package.json ]; then echo js
  elif [ -f app/build.gradle.kts ] || [ -f app/build.gradle ]; then echo android
  else echo none; fi
}

sedi() { if sed --version >/dev/null 2>&1; then sed -i "$@"; else sed -i '' "$@"; fi; }

current_json_version() { python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["version"])' "$1"; }
set_json_version() {
  python3 - "$1" "$2" <<'PY'
import json, sys, re
path, version = sys.argv[1], sys.argv[2]
raw = open(path).read()
new, n = re.subn(r'("version"\s*:\s*")[^"]*(")', r'\g<1>' + version + r'\2', raw, count=1)
if n != 1: sys.exit(f"{path}: no \"version\" field")
open(path, "w").write(new)
PY
}

gradle_file() { if [ -f app/build.gradle.kts ]; then echo app/build.gradle.kts; else echo app/build.gradle; fi; }
gradle_version_name() { sed -nE 's/^[[:space:]]*versionName[[:space:]]*=?[[:space:]]*"([^"]+)".*/\1/p' "$1" | head -1; }
gradle_version_code() { sed -nE 's/^[[:space:]]*versionCode[[:space:]]*=?[[:space:]]*([0-9]+).*/\1/p' "$1" | head -1; }
bump_gradle() { # file version
  local f="$1" v="$2" code
  code="$(gradle_version_code "$f")"; [ -n "$code" ] || die "$f: versionCode not found"
  sedi -E "s/^([[:space:]]*versionName[[:space:]]*=?[[:space:]]*\")[^\"]+(\".*)/\1$v\2/" "$f"
  sedi -E "s/^([[:space:]]*versionCode[[:space:]]*=?[[:space:]]*)[0-9]+/\1$((code + 1))/" "$f"
  echo "$f: versionName $v, versionCode $((code + 1))"
}

bump_python() {
  grep -Eq '^version *= *"' pyproject.toml || die "pyproject.toml: project.version not found"
  sedi -E "s/^(version *= *\")[^\"]+(\")/\1$VERSION\2/" pyproject.toml
  echo "pyproject.toml: version $VERSION"
}
bump_extension() {
  set_json_version manifest.json "$VERSION"; echo "manifest.json: version $VERSION"
  [ -f package.json ] && { set_json_version package.json "$VERSION"; echo "package.json: version $VERSION"; }
}
bump_js() { set_json_version package.json "$VERSION"; echo "package.json: version $VERSION"; [ -f package-lock.json ] && npm install --package-lock-only --ignore-scripts >/dev/null && echo "package-lock.json: synced"; }
bump_react_native() {
  local pkg gf
  pkg="$(current_json_version package.json)"
  if [ -d android ]; then
    gf="android/app/build.gradle"; [ -f "$gf" ] || gf="android/app/build.gradle.kts"
    [ "$(gradle_version_name "$gf")" = "$pkg" ] || die "drift: $gf versionName $(gradle_version_name "$gf") != package.json $pkg"
  fi
  if [ -d ios ]; then
    local mv; mv="$(grep -ho 'MARKETING_VERSION = [^;]*' ios/*.xcodeproj/project.pbxproj | head -1 | awk '{print $3}')"
    [ "$mv" = "$pkg" ] || die "drift: ios MARKETING_VERSION $mv != package.json $pkg"
  fi
  bump_js
  [ -d android ] && bump_gradle "$gf" "$VERSION"
  if [ -d ios ]; then
    sedi -E "s/(MARKETING_VERSION = )[^;]*;/\1$VERSION;/" ios/*.xcodeproj/project.pbxproj
    local cur; cur="$(grep -ho 'CURRENT_PROJECT_VERSION = [0-9]*' ios/*.xcodeproj/project.pbxproj | head -1 | awk '{print $3}')"
    sedi -E "s/(CURRENT_PROJECT_VERSION = )[0-9]*;/\1$((cur + 1));/" ios/*.xcodeproj/project.pbxproj
    echo "ios: MARKETING_VERSION $VERSION, CURRENT_PROJECT_VERSION $((cur + 1))"
  fi
}
bump_android() {
  local gf; gf="$(gradle_file)"
  if [ -f VERSION ] && [ -x scripts/sync-version.sh ]; then
    # Repos with a second app (just-my-weather's iOS target) keep one semver in
    # VERSION and copy it into versionName and MARKETING_VERSION with their own
    # sync script; versionCode is still bumped here (§7: +1 per upload).
    [ "$(cat VERSION)" = "$(gradle_version_name "$gf")" ] || die "drift: VERSION $(cat VERSION) != $gf versionName $(gradle_version_name "$gf")"
    printf '%s\n' "$VERSION" > VERSION; scripts/sync-version.sh; echo "VERSION: $VERSION (synced by scripts/sync-version.sh)"
    local code; code="$(gradle_version_code "$gf")"
    sedi -E "s/^([[:space:]]*versionCode[[:space:]]*=?[[:space:]]*)[0-9]+/\1$((code + 1))/" "$gf"; echo "$gf: versionCode $((code + 1))"
  else
    bump_gradle "$gf" "$VERSION"
    [ -f VERSION ] && { printf '%s\n' "$VERSION" > VERSION; echo "VERSION: $VERSION"; }
  fi
}
bump_go() { echo "go: the tag is the version; nothing to bump"; }

STACK="$(detect_stack)"
echo "== release $TAG ($STACK)"
case "$STACK" in
  python) bump_python ;; extension) bump_extension ;; react-native) bump_react_native ;;
  js) bump_js ;; android) bump_android ;; go) bump_go ;;
  *) die "no version source-of-truth file found (§7)" ;;
esac

# ---- 3. changelog -----------------------------------------------------------
# `git cliff --prepend` writes its header and the new section above the OLD
# file, duplicating the header and stranding any hand-written
# "## [Unreleased]" notes below the new release. scripts/changelog-fold.py
# (tested by `make release-fold-check`) repairs that: duplicate header
# dropped, Unreleased prose and groups merged into the new section, link
# references kept at the bottom.
fold_changelog() { # file version
  python3 "$(dirname "$0")/changelog-fold.py" "$1" "$2"
}

changelog_section() { # file version -> prints the new section
  awk -v hdr="## [$2]" 'index($0, hdr) == 1 { grab = 1 } grab && /^## \[/ && index($0, hdr) != 1 { exit } grab' "$1"
}

# An empty section means no Conventional Commit of a changelog-worthy type
# (feat/fix/perf/refactor/revert, see cliff.toml) since the last tag: there is
# nothing to release, and tagging anyway would publish release notes that say
# so. Returns non-zero (never exits) so the caller can restore the tree first.
# The section is captured before grep looks at it: with pipefail, a `grep -q`
# that stops at the first match would SIGPIPE awk on a long section and turn
# a real release into a false "nothing to release".
has_release_content() { # file version
  local sec
  sec="$(changelog_section "$1" "$2")"
  grep '^### ' >/dev/null <<<"$sec"
}
NO_CONTENT_MSG="no changelog entries since the last tag — nothing to release (only docs/ci/chore/test commits, or none)"

if [ "${RELEASE_DRY_RUN:-0}" = 1 ]; then
  tmp="$(mktemp -t CHANGELOG.XXXXXX)"
  # Step 2 may have bumped version files (non-go stacks); a dry run promises
  # to touch nothing, so restore the tree on every exit path from here on.
  trap 'rm -f "$tmp"; git checkout -q -- . 2>/dev/null || true' EXIT
  if [ -f CHANGELOG.md ]; then
    cp CHANGELOG.md "$tmp"
    git cliff --tag "$TAG" --unreleased --prepend "$tmp" >/dev/null || die "git-cliff failed"
    fold_changelog "$tmp" "$VERSION"
  else
    git cliff --tag "$TAG" > "$tmp" || die "git-cliff failed"
  fi
  echo "== dry run: CHANGELOG.md section that would be committed"
  changelog_section "$tmp" "$VERSION"
  has_release_content "$tmp" "$VERSION" || die "$NO_CONTENT_MSG"
  echo "== dry run: would commit chore(release): $TAG on $BRANCH, open its PR with squash auto-merge, then tag the merge commit $TAG and push the tag"
  exit 0
fi

if [ -f CHANGELOG.md ]; then
  git cliff --tag "$TAG" --unreleased --prepend CHANGELOG.md || die "git-cliff failed"
  fold_changelog CHANGELOG.md "$VERSION"
else
  git cliff --tag "$TAG" > CHANGELOG.md || die "git-cliff failed"
fi
# Restore the bump and the changelog before refusing, or the next run stops
# at "working tree not clean".
has_release_content CHANGELOG.md "$VERSION" || { git checkout -q -- .; die "$NO_CONTENT_MSG"; }
echo "CHANGELOG.md: $TAG entry written"

# ---- 4. release PR, then tag the merge commit --------------------------------
# The changelog was generated from THIS commit. If anything else lands on
# main before the release PR merges, the squash commit (and the tag) would
# contain changes the vX.Y.Z section never mentions — and the next release's
# `git cliff --unreleased` starts after the tag, so they would be missing for
# good. The merge commit's parent must therefore be the base recorded here.
base="$(git rev-parse HEAD)"
git checkout -q -b "$BRANCH"
git add -A
git commit -q -m "chore(release): $TAG" -m "Source: VC Standard §7"
git push -q -u origin "$BRANCH"
pr_url="$(gh pr create --base main --head "$BRANCH" --title "chore(release): $TAG" \
  --body "Release commit for $TAG: the CHANGELOG.md section generated by git-cliff. Squash auto-merge is armed; once CI is green and it lands, scripts/release.sh pushes the signed $TAG tag at the merge commit and release.yml cuts the GitHub Release.")"
echo "== $pr_url opened; arming squash auto-merge"
gh pr merge --squash --auto --delete-branch --subject "chore(release): $TAG" "$pr_url"
echo "== waiting for CI to merge $pr_url (ctrl-c leaves the PR open; merge it and tag the merge commit by hand)"
state=""
for _ in $(seq 1 120); do
  state="$(gh pr view "$pr_url" --json state --jq .state)"
  [ "$state" = MERGED ] && break
  [ "$state" = CLOSED ] && die "$pr_url was closed without merging"
  sleep 15
done
[ "$state" = MERGED ] || die "$pr_url has not merged after 30 minutes; once it does: git tag -s -a $TAG -m $TAG <merge sha> && git push origin $TAG"
merged="$(gh pr view "$pr_url" --json mergeCommit --jq .mergeCommit.oid)"
git checkout -q main
git pull -q --ff-only origin main
if [ "$(git rev-parse "$merged^")" != "$base" ]; then
  die "main moved between generating the changelog (${base:0:7}) and merging the release PR (${merged:0:7} is on top of $(git rev-parse --short "$merged^")). NOT tagging: the $VERSION section would omit those commits. Regenerate: revert the release commit with a PR (or bump to the next patch) and run $0 again."
fi
[ "$(git rev-parse HEAD)" = "$merged" ] || echo "note: main moved past the release commit (${merged:0:7}); tagging that commit, not HEAD"
git tag -s -a "$TAG" -m "$TAG" "$merged"
git push origin "$TAG"
echo "== $TAG pushed at ${merged:0:7}; release.yml creates the GitHub Release"
