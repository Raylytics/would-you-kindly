"""Fixture tests for scripts/changelog-fold.py (run by `make release-fold-check`)."""
import importlib.util
import os
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("changelog_fold", os.path.join(HERE, "changelog-fold.py"))
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
fold = mod.fold

NEW_HEADER = """# Changelog

Generated from Conventional Commits by git-cliff.

"""
GENERATED = """## [0.9.0] - 2026-12-01

### Added

- **cli:** New thing ([abc1234](https://example.invalid/commit/abc1234))

### Fixed

- **tui:** Generated fix line ([def5678](https://example.invalid/commit/def5678))

[0.9.0]: https://example.invalid/compare/v0.8.2...v0.9.0

"""
OLD_HEADER = """# Changelog

All notable changes to this project are documented in this file. The
format is based on Keep a Changelog.

"""
OLD_RELEASE = """## [0.8.2] - 2026-11-04

### Fixed

- old stuff

"""
REFS = """[Unreleased]: https://example.invalid/compare/v0.8.2...HEAD
[0.8.2]: https://example.invalid/compare/v0.8.1...v0.8.2
"""


class FoldTests(unittest.TestCase):
    def test_duplicate_header_dropped_and_groups_merged(self):
        unreleased = """## [Unreleased]

### Fixed

- **Hand-written fix detail** (would-you-kindly-xxxx). Spans
  two lines.

### Security

- Hand-written security note.

"""
        out = fold(NEW_HEADER + GENERATED + OLD_HEADER + unreleased + OLD_RELEASE + REFS, "0.9.0")
        self.assertEqual(out.count("# Changelog\n"), 1)
        self.assertNotIn("## [Unreleased]", out)
        fixed = out[out.index("### Fixed"):out.index("### Security")]
        self.assertIn("Generated fix line", fixed)
        self.assertIn("Hand-written fix detail", fixed)
        self.assertLess(fixed.index("Generated fix line"), fixed.index("Hand-written fix detail"))
        security = out[out.index("### Security"):out.index("## [0.8.2]")]
        self.assertIn("Hand-written security note.", security)
        # link refs stay at the bottom, in order, with Unreleased rebased
        tail = out[out.index("[Unreleased]:"):]
        self.assertEqual(tail.splitlines(), [
            "[Unreleased]: https://example.invalid/compare/v0.9.0...HEAD",
            "[0.9.0]: https://example.invalid/compare/v0.8.2...v0.9.0",
            "[0.8.2]: https://example.invalid/compare/v0.8.1...v0.8.2",
        ])
        self.assertEqual(out.count("[0.9.0]:"), 1)

    def test_prose_before_first_group_is_kept(self):
        unreleased = """## [Unreleased]

This release renames the config file; see docs/MIGRATION.md.

### Fixed

- a hand-written fix

"""
        out = fold(NEW_HEADER + GENERATED + OLD_HEADER + unreleased + OLD_RELEASE + REFS, "0.9.0")
        sec = out[out.index("## [0.9.0]"):out.index("## [0.8.2]")]
        self.assertIn("renames the config file", sec)
        self.assertLess(sec.index("renames the config file"), sec.index("### Added"))
        self.assertIn("- a hand-written fix", sec)

    def test_no_unreleased_section(self):
        out = fold(NEW_HEADER + GENERATED + OLD_HEADER + OLD_RELEASE + REFS, "0.9.0")
        self.assertEqual(out.count("# Changelog\n"), 1)
        self.assertIn("## [0.9.0] - 2026-12-01", out)
        self.assertIn("## [0.8.2] - 2026-11-04", out)
        self.assertIn("[Unreleased]: https://example.invalid/compare/v0.9.0...HEAD\n[0.9.0]:", out)

    def test_missing_unreleased_ref_appends_link_at_end(self):
        refs = "[0.8.2]: https://example.invalid/compare/v0.8.1...v0.8.2\n"
        out = fold(NEW_HEADER + GENERATED + OLD_HEADER + OLD_RELEASE + refs, "0.9.0")
        self.assertTrue(out.endswith("[0.9.0]: https://example.invalid/compare/v0.8.2...v0.9.0\n"))
        self.assertEqual(out.count("[0.9.0]:"), 1)

    def test_first_release_is_untouched(self):
        text = NEW_HEADER + "## [0.1.0] - 2026-01-01\n\n### Added\n\n- first\n\n[0.1.0]: https://example.invalid/releases/tag/v0.1.0\n"
        self.assertEqual(fold(text, "0.1.0"), text)

    def test_missing_section_is_an_error(self):
        with self.assertRaises(ValueError):
            fold(NEW_HEADER + OLD_RELEASE, "0.9.0")

    def test_idempotent_on_already_folded_file(self):
        once = fold(NEW_HEADER + GENERATED + OLD_HEADER + OLD_RELEASE + REFS, "0.9.0")
        self.assertEqual(fold(once, "0.9.0"), once)


if __name__ == "__main__":
    unittest.main()
