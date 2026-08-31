package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/moviegeek/darknight/internal/store"
)

// rescanFixture creates a throwaway database holding one library whose root
// contains a single unscanned release, and points the config environment at
// it. Returns the database path.
func rescanFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "darknight.db")

	root := filepath.Join(dir, "media")
	relDir := filepath.Join(root, "Casino.1995.1080p.BluRay.x265-beAst")
	if err := os.MkdirAll(relDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(relDir, "Casino.1995.1080p.BluRay.x265-beAst.mkv"),
		[]byte("fake-mkv"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if _, err := st.CreateLibrary(ctx, "Films", root, 0); err != nil {
		t.Fatalf("create library: %v", err)
	}

	// pin the config so buildRuntime picks up the fixture database and stays
	// offline (no TMDB) regardless of the developer's shell / .env values.
	t.Setenv("DARKNIGHT_DB", dbPath)
	t.Setenv("TMDB_API_KEY", "")
	t.Setenv("DARKNIGHT_LOG_LEVEL", "info")
	return dbPath
}

// tableCount opens dbPath read-only for our purposes and counts rows.
func tableCount(t *testing.T, dbPath, table string) int {
	t.Helper()
	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open %s: %v", dbPath, err)
	}
	defer st.Close()
	var n int
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestRunRescan_DryRunWritesNothing runs a dry rescan over a library with one
// unscanned release and asserts the real database stays empty: the movie,
// its file, and the library's last_scan_at are only ever written to the
// throwaway snapshot.
func TestRunRescan_DryRunWritesNothing(t *testing.T) {
	dbPath := rescanFixture(t)
	preSnapshots, _ := filepath.Glob(filepath.Join(os.TempDir(), "darknight-rescan-dryrun-*"))

	if code := runRescan(context.Background(), []string{"--dry-run", "--library=all"}); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	for _, table := range []string{"movies", "movie_files", "tmdb_cache"} {
		if n := tableCount(t, dbPath, table); n != 0 {
			t.Fatalf("dry-run wrote %d rows into %s of the real database, want 0", n, table)
		}
	}
	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	libs, err := st.ListLibraries(context.Background())
	if err != nil || len(libs) != 1 {
		t.Fatalf("list libraries: %v (%d)", err, len(libs))
	}
	if libs[0].LastScanAt != 0 {
		t.Fatalf("dry-run touched last_scan_at = %d, want 0", libs[0].LastScanAt)
	}

	// the snapshot and its WAL/SHM sidecars are cleaned up afterwards
	postSnapshots, _ := filepath.Glob(filepath.Join(os.TempDir(), "darknight-rescan-dryrun-*"))
	if len(postSnapshots) != len(preSnapshots) {
		t.Fatalf("dry-run left snapshot files behind: %v", postSnapshots)
	}
}

// TestRunRescan_AppliesScan is the counterpart of the dry-run test: the same
// fixture scanned without --dry-run (numeric --library form) must persist the
// movie and its file, proving the dry-run fixture genuinely produces writes.
func TestRunRescan_AppliesScan(t *testing.T) {
	dbPath := rescanFixture(t)

	// library ids start at 1 on a fresh database
	if code := runRescan(context.Background(), []string{"--library=1"}); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	if n := tableCount(t, dbPath, "movies"); n != 1 {
		t.Fatalf("movies rows = %d, want 1", n)
	}
	if n := tableCount(t, dbPath, "movie_files"); n != 1 {
		t.Fatalf("movie_files rows = %d, want 1", n)
	}
	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	libs, err := st.ListLibraries(context.Background())
	if err != nil || len(libs) != 1 {
		t.Fatalf("list libraries: %v (%d)", err, len(libs))
	}
	if libs[0].LastScanAt == 0 {
		t.Fatal("rescan did not touch last_scan_at")
	}
}

func TestRunRescan_UnknownLibrary(t *testing.T) {
	rescanFixture(t)
	if code := runRescan(context.Background(), []string{"--library=999"}); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

func TestParseRescanArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		library string
		dryRun  bool
		wantErr bool
	}{
		{"dry run all", []string{"--dry-run", "--library=all"}, "all", true, false},
		{"dry run id", []string{"--dry-run", "--library=3"}, "3", true, false},
		{"apply id", []string{"--library=1"}, "1", false, false},
		{"space form", []string{"--library", "3"}, "3", false, false},
		{"case-insensitive all", []string{"--library=ALL"}, "all", false, false},
		{"missing library flag", []string{"--dry-run"}, "", false, true},
		{"no args", nil, "", false, true},
		{"non-numeric id", []string{"--library=films"}, "", false, true},
		{"unknown flag", []string{"--library=all", "--force"}, "", false, true},
		{"dangling library flag", []string{"--library"}, "", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o, err := parseRescanArgs(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", o)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if o.library != tc.library || o.dryRun != tc.dryRun {
				t.Fatalf("got library=%q dryRun=%v, want library=%q dryRun=%v",
					o.library, o.dryRun, tc.library, tc.dryRun)
			}
		})
	}
}
