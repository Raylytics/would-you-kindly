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
# fold_changelog: `git cliff --prepend` writes its header and the new section
# above the OLD file, so the old header is duplicated and a hand-written
# "## [Unreleased]" section (with its "### Group" bullets) ends up below the
# new "## [X.Y.Z]" header. Drop the duplicate header, merge the Unreleased
# groups into the new section (same group -> appended; new group -> added),
# and keep the link references at the bottom of the file where Keep a
# Changelog puts them.
fold_changelog() { # file version
  python3 - "$1" "$2" <<'PY'
import re, sys
path, ver = sys.argv[1], sys.argv[2]
s = open(path).read()
hdr = re.compile(r"^## \[", re.M)
new_sec = re.search(r"^## \[%s\][^\n]*\n" % re.escape(ver), s, re.M)
if not new_sec:
    sys.exit("fold_changelog: no '## [%s]' section" % ver)
nxt = hdr.search(s, new_sec.end())
if not nxt:
    sys.exit(0)  # first release: nothing below the new section
section, rest = s[new_sec.end():nxt.start()], s[nxt.start():]
# The generated section ends with its own link line; whatever follows it up to
# the next "## [" is the old file's header text — drop it.
link = re.search(r"^\[%s\]: [^\n]+\n" % re.escape(ver), section, re.M)
linkline = ""
if link:
    linkline, section = link.group(0), section[:link.start()]
m_unr = re.match(r"## \[Unreleased\][^\n]*\n", rest)
if m_unr:
    end = hdr.search(rest, m_unr.end())
    body = rest[m_unr.end():end.start() if end else len(rest)]
    rest = rest[end.start():] if end else ""
    for g in re.split(r"^### ", body, flags=re.M)[1:]:
        name, _, items = g.partition("\n")
        name, items = name.strip(), items.strip("\n")
        if not items:
            continue
        gm = re.search(r"^### %s[ \t]*\n" % re.escape(name), section, re.M)
        if gm:
            gnext = re.search(r"^### ", section[gm.end():], re.M)
            at = gm.end() + gnext.start() if gnext else len(section)
            section = section[:at].rstrip("\n") + "\n" + items + "\n\n" + section[at:]
        else:
            section = section.rstrip("\n") + "\n\n### " + name + "\n\n" + items + "\n\n"
out = s[:new_sec.end()] + section.rstrip("\n") + "\n\n" + rest
if linkline:
    unr = re.search(r"^\[Unreleased\]: (\S+)/compare/\S+\.\.\.HEAD[ \t]*\n", out, re.M)
    if unr:
        out = out[:unr.start()] + "[Unreleased]: %s/compare/v%s...HEAD\n" % (unr.group(1), ver) + linkline + out[unr.end():]
    else:
        out = out.rstrip("\n") + "\n" + linkline
open(path, "w").write(out)
PY
}

changelog_section() { # file version -> prints the new section
  awk -v hdr="## [$2]" 'index($0, hdr) == 1 { grab = 1 } grab && /^## \[/ && index($0, hdr) != 1 { exit } grab' "$1"
}

if [ "${RELEASE_DRY_RUN:-0}" = 1 ]; then
  tmp="$(mktemp -t CHANGELOG.XXXXXX)"
  trap 'rm -f "$tmp"' EXIT
  if [ -f CHANGELOG.md ]; then
    cp CHANGELOG.md "$tmp"
    git cliff --tag "$TAG" --unreleased --prepend "$tmp" >/dev/null 2>&1
    fold_changelog "$tmp" "$VERSION"
  else
    git cliff --tag "$TAG" > "$tmp" 2>/dev/null
  fi
  echo "== dry run: CHANGELOG.md section that would be committed"
  changelog_section "$tmp" "$VERSION"
  echo "== dry run: would commit chore(release): $TAG on $BRANCH, open its PR with squash auto-merge, then tag the merge commit $TAG and push the tag"
  git checkout -q -- . 2>/dev/null || true
  exit 0
fi

if [ -f CHANGELOG.md ]; then
  git cliff --tag "$TAG" --unreleased --prepend CHANGELOG.md
  fold_changelog CHANGELOG.md "$VERSION"
else
  git cliff --tag "$TAG" > CHANGELOG.md
fi
echo "CHANGELOG.md: $TAG entry written"

# ---- 4. release PR, then tag the merge commit --------------------------------
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
[ "$(git rev-parse HEAD)" = "$merged" ] || echo "note: main moved past the release commit (${merged:0:7}); tagging that commit, not HEAD"
git tag -s -a "$TAG" -m "$TAG" "$merged"
git push origin "$TAG"
echo "== $TAG pushed at ${merged:0:7}; release.yml creates the GitHub Release"
