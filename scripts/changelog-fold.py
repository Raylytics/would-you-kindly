#!/usr/bin/env python3
"""Fold a git-cliff --prepend result into one well-formed CHANGELOG.md.

`git cliff --tag vX.Y.Z --unreleased --prepend CHANGELOG.md` writes its own
header and the new "## [X.Y.Z]" section ABOVE the old file, so the result has
the old header duplicated and any hand-written "## [Unreleased]" section
(with its "### Group" bullets) stranded below the new release header. This
script, called by scripts/release.sh right after cliff:

  1. drops the duplicated old header (the text between the generated
     section's link line and the first old "## [" heading);
  2. merges the hand-written Unreleased notes into the new section — prose
     before the first group goes to the top of the section, each "### Group"
     is appended to the same group or added as a new one;
  3. keeps link references at the bottom of the file where Keep a Changelog
     puts them, rewriting "[Unreleased]: .../compare/vOLD...HEAD" to the new
     version and inserting the new "[X.Y.Z]:" line under it.

Usage: changelog-fold.py CHANGELOG.md X.Y.Z
Exit 2 with a message when the file has no "## [X.Y.Z]" section.
"""
import re
import sys

HDR = re.compile(r"^## \[", re.M)


def fold(text: str, ver: str) -> str:
    new_sec = re.search(r"^## \[%s\][^\n]*\n" % re.escape(ver), text, re.M)
    if not new_sec:
        raise ValueError("no '## [%s]' section" % ver)
    nxt = HDR.search(text, new_sec.end())
    if not nxt:
        return text  # first release: nothing below the new section
    section, rest = text[new_sec.end():nxt.start()], text[nxt.start():]
    # The generated section ends with its own link line; whatever follows it
    # up to the next "## [" is the old file's header text — drop it.
    linkline = ""
    link = re.search(r"^\[%s\]: [^\n]+\n" % re.escape(ver), section, re.M)
    if link:
        linkline, section = link.group(0), section[:link.start()]
    m_unr = re.match(r"## \[Unreleased\][^\n]*\n", rest)
    if m_unr:
        end = HDR.search(rest, m_unr.end())
        body = rest[m_unr.end():end.start() if end else len(rest)]
        rest = rest[end.start():] if end else ""
        parts = re.split(r"^### ", body, flags=re.M)
        prose = parts[0].strip("\n")
        if prose:
            section = "\n" + prose + "\n" + section
        for g in parts[1:]:
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
    out = text[:new_sec.end()] + section.rstrip("\n") + "\n\n" + rest
    if linkline:
        unr = re.search(r"^\[Unreleased\]: (\S+)/compare/\S+\.\.\.HEAD[ \t]*\n", out, re.M)
        if unr:
            out = (out[:unr.start()]
                   + "[Unreleased]: %s/compare/v%s...HEAD\n" % (unr.group(1), ver)
                   + linkline + out[unr.end():])
        else:
            out = out.rstrip("\n") + "\n" + linkline
    return out


def main(argv):
    if len(argv) != 3:
        sys.exit("usage: changelog-fold.py CHANGELOG.md X.Y.Z")
    path, ver = argv[1], argv[2]
    with open(path, encoding="utf-8") as f:
        text = f.read()
    try:
        out = fold(text, ver)
    except ValueError as e:
        sys.exit("changelog-fold: %s" % e)
    with open(path, "w", encoding="utf-8") as f:
        f.write(out)


if __name__ == "__main__":
    main(sys.argv)
