// Package workspace inspects a bd workspace's on-disk layout for the
// one misconfiguration bd itself hides: a `.beads/` whose embedded Dolt
// directory holds a real database while `.beads/metadata.json` — the
// file that names it — is missing or points elsewhere. bd then prints
// a one-line warning, falls back to the default database name
// ("beads"), creates it empty, and `bd list` answers "No issues found"
// as if the tracker were simply empty (would-you-kindly-dcby).
//
// That is exactly what happened to every clone that pulled past the
// commit which stopped tracking `.beads/`: git removed metadata.json
// and config.yaml from the working tree, and bd carried on against an
// empty database without failing. This package exists so both `wyk
// doctor` and the TUI's multi-repo fetch can name the condition
// instead of reporting zero rows for the repo.
//
// It reads two things only: the JSON in metadata.json and the
// directory names under .beads/embeddeddolt. It never opens Dolt's
// storage.
package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultDatabase is the database name bd falls back to when
// metadata.json is absent.
const DefaultDatabase = "beads"

// DBState is what Inspect found about one workspace.
type DBState struct {
	// Configured is metadata.json's dolt_database, or "" when the file
	// is missing, unreadable, or has no such field.
	Configured string
	// MetadataPresent is whether .beads/metadata.json exists at all.
	MetadataPresent bool
	// MetadataErr is non-nil when the file exists but could not be
	// read or parsed.
	MetadataErr error
	// Databases lists the directories under .beads/embeddeddolt in
	// sorted order, Dolt's own ".lock" excluded. Empty when there is
	// no embedded Dolt directory (JSONL-only or remote-Dolt
	// workspaces).
	Databases []string
}

// Inspect reads repoPath/.beads. The error is non-nil only when .beads
// itself is missing or unreadable; a missing metadata.json or
// embeddeddolt directory is reported through the returned state.
func Inspect(repoPath string) (DBState, error) {
	var s DBState
	beadsDir := filepath.Join(repoPath, ".beads")
	if _, err := os.Stat(beadsDir); err != nil {
		return s, err
	}
	switch raw, err := os.ReadFile(filepath.Join(beadsDir, "metadata.json")); {
	case err == nil:
		s.MetadataPresent = true
		var md struct {
			DoltDatabase string `json:"dolt_database"`
		}
		if jerr := json.Unmarshal(raw, &md); jerr != nil {
			s.MetadataErr = jerr
		} else {
			s.Configured = md.DoltDatabase
		}
	case errors.Is(err, os.ErrNotExist):
		// absent: the fallback case this package exists for
	default:
		s.MetadataPresent = true
		s.MetadataErr = err
	}
	entries, err := os.ReadDir(filepath.Join(beadsDir, "embeddeddolt"))
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			s.Databases = append(s.Databases, e.Name())
		}
		sort.Strings(s.Databases)
	}
	return s, nil
}

// Mismatch is the condition under which bd is serving the wrong (empty)
// database for a workspace that has a real one on disk.
type Mismatch struct {
	// Configured is the database metadata.json names; "" when the
	// file is missing or unparseable.
	Configured string
	// Candidates are the non-default databases on disk that the
	// workspace's data is actually in.
	Candidates []string
	// MetadataErr carries a parse/read error when that is why
	// Configured is empty.
	MetadataErr error
}

// Error renders the condition for a fetch banner or a doctor row.
func (m *Mismatch) Error() string {
	what := ".beads/metadata.json is missing"
	switch {
	case m.MetadataErr != nil:
		what = ".beads/metadata.json is unreadable (" + m.MetadataErr.Error() + ")"
	case m.Configured != "":
		what = fmt.Sprintf(".beads/metadata.json names database %q, which is not on disk", m.Configured)
	}
	return fmt.Sprintf("bd is reading an empty default database: %s; the issues live in .beads/embeddeddolt/%s",
		what, strings.Join(m.Candidates, " or "))
}

// Mismatch returns the fallback condition when the state shows one, or
// nil when bd would open the database the data is in. A workspace with
// no embedded databases, or only the default one, is never a mismatch:
// bd's fallback lands on the right (or only) database there.
func (s DBState) Mismatch() *Mismatch {
	var candidates []string
	for _, db := range s.Databases {
		if db != DefaultDatabase {
			candidates = append(candidates, db)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	if s.Configured != "" {
		for _, db := range s.Databases {
			if db == s.Configured {
				return nil
			}
		}
	}
	return &Mismatch{Configured: s.Configured, Candidates: candidates, MetadataErr: s.MetadataErr}
}

// Check is Inspect followed by Mismatch. It returns nil when the
// workspace is healthy OR cannot be inspected (no .beads) — the latter
// is bd's to report, not this package's.
func Check(repoPath string) *Mismatch {
	s, err := Inspect(repoPath)
	if err != nil {
		return nil
	}
	return s.Mismatch()
}

// MinimalMetadata is the smallest metadata.json that makes bd open the
// named embedded database again. bd accepts it without a project_id
// (verified against bd 1.3.x); restoring the original from git history
// is still preferable when it exists.
func MinimalMetadata(database string) string {
	b, _ := json.Marshal(map[string]string{
		"database":      "dolt",
		"backend":       "dolt",
		"dolt_mode":     "embedded",
		"dolt_database": database,
	})
	return string(b)
}
