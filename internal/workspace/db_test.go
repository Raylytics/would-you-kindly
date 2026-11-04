package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mkWorkspace lays out <dir>/.beads with the given embedded databases
// and, when metadata != "", that metadata.json body.
func mkWorkspace(t *testing.T, metadata string, dbs ...string) string {
	t.Helper()
	dir := t.TempDir()
	beads := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(filepath.Join(beads, "embeddeddolt", ".lock"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, db := range dbs {
		if err := os.Mkdir(filepath.Join(beads, "embeddeddolt", db), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if metadata != "" {
		if err := os.WriteFile(filepath.Join(beads, "metadata.json"), []byte(metadata), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestHealthyWorkspaceIsNoMismatch(t *testing.T) {
	dir := mkWorkspace(t, MinimalMetadata("my_repo"), "my_repo")
	if mm := Check(dir); mm != nil {
		t.Fatalf("healthy workspace reported %v", mm)
	}
}

func TestStrayDefaultDBNextToConfiguredOneIsHealthy(t *testing.T) {
	// The leftover empty "beads" db a past fallback created is harmless
	// once metadata.json names the real one again.
	dir := mkWorkspace(t, MinimalMetadata("my_repo"), "beads", "my_repo")
	if mm := Check(dir); mm != nil {
		t.Fatalf("recovered workspace reported %v", mm)
	}
}

func TestMissingMetadataWithRealDBIsMismatch(t *testing.T) {
	dir := mkWorkspace(t, "", "beads", "my_repo")
	mm := Check(dir)
	if mm == nil {
		t.Fatal("expected a mismatch for missing metadata.json")
	}
	if got := strings.Join(mm.Candidates, ","); got != "my_repo" {
		t.Errorf("candidates = %q, want my_repo", got)
	}
	msg := mm.Error()
	for _, want := range []string{"metadata.json is missing", "embeddeddolt/my_repo"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
}

func TestMetadataNamingAbsentDBIsMismatch(t *testing.T) {
	dir := mkWorkspace(t, MinimalMetadata("other"), "my_repo")
	mm := Check(dir)
	if mm == nil || mm.Configured != "other" {
		t.Fatalf("expected mismatch naming \"other\", got %v", mm)
	}
	if !strings.Contains(mm.Error(), `names database "other"`) {
		t.Errorf("message %q should quote the configured name", mm.Error())
	}
}

func TestUnparseableMetadataIsMismatch(t *testing.T) {
	dir := mkWorkspace(t, "{not json", "my_repo")
	mm := Check(dir)
	if mm == nil || mm.MetadataErr == nil {
		t.Fatalf("expected a mismatch carrying the parse error, got %v", mm)
	}
	if !strings.Contains(mm.Error(), "unreadable") {
		t.Errorf("message %q should say unreadable", mm.Error())
	}
}

func TestOnlyDefaultDBIsNotAMismatch(t *testing.T) {
	// No metadata but nothing except the default db: bd's fallback is
	// the right database. Nothing to report.
	if mm := Check(mkWorkspace(t, "", "beads")); mm != nil {
		t.Fatalf("default-only workspace reported %v", mm)
	}
}

func TestNoEmbeddedDoltIsNotAMismatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if mm := Check(dir); mm != nil {
		t.Fatalf("jsonl-only workspace reported %v", mm)
	}
}

func TestNoBeadsDirIsNotAMismatch(t *testing.T) {
	if mm := Check(t.TempDir()); mm != nil {
		t.Fatalf("missing .beads should be bd's to report, got %v", mm)
	}
	if _, err := Inspect(t.TempDir()); err == nil {
		t.Error("Inspect should error without .beads")
	}
}

func TestInspectListsDatabasesSortedWithoutLock(t *testing.T) {
	dir := mkWorkspace(t, "", "zeta", "alpha")
	s, err := Inspect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.Databases, ","); got != "alpha,zeta" {
		t.Errorf("databases = %q, want alpha,zeta", got)
	}
	if s.MetadataPresent {
		t.Error("MetadataPresent should be false")
	}
}

func TestInspectIgnoresNonIdentifierDirectories(t *testing.T) {
	// Only names bd itself would create count as databases; anything
	// else (Dolt's .lock, a stray folder with shell metacharacters) is
	// neither reported nor echoed into a suggested command.
	dir := mkWorkspace(t, "", "ok_db", "bad name$(x)", ".hidden")
	s, err := Inspect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.Databases, ","); got != "ok_db" {
		t.Errorf("databases = %q, want ok_db", got)
	}
}

func TestMismatchMessageStripsControlBytes(t *testing.T) {
	dir := mkWorkspace(t, "{\x1b[31m", "my_repo")
	mm := Check(dir)
	if mm == nil {
		t.Fatal("expected a mismatch")
	}
	if strings.ContainsRune(mm.Error(), 0x1b) {
		t.Errorf("message leaks an escape byte: %q", mm.Error())
	}
}
