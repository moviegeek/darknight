package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/moviegeek/darknight/internal/model"
)

// After an on-disk rename the DB must point at the NEW directory and the NEW
// basenames - for the movie_file row, its nfo_path, and every external
// subtitle. Regression: only the basenames were rewritten, leaving the old
// directory segment in the absolute nfo/subtitle paths.
func TestRenameMovieFileRelease_RewritesDirAndBasenames(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	lib, err := s.CreateLibrary(ctx, "Films", "/mnt/Media/Movie", 0)
	if err != nil {
		t.Fatal(err)
	}
	m := &model.Movie{Title: "Green Snake", TitleEn: "Green Snake", Year: 1993, TMDBID: 39915}
	if err := s.UpsertMovie(ctx, m); err != nil {
		t.Fatal(err)
	}

	oldDir := "Grean.Snake.1993.BluRay.1080p.x264-HDChina"
	oldStem := oldDir
	absOld := filepath.Join("/mnt/Media/Movie", oldDir)
	mf := &model.MovieFile{
		MovieID: m.ID, LibraryID: lib.ID,
		DirPath: oldDir, FileName: oldStem + ".mkv", RawName: oldStem,
		NFOPath: filepath.Join(absOld, oldStem+".nfo"),
	}
	if err := s.UpsertMovieFile(ctx, mf); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceSubtitles(ctx, mf.ID, []model.Subtitle{
		{FilePath: filepath.Join(absOld, oldStem+".chi.srt"), Language: "chi", Format: "srt"},
		{FilePath: filepath.Join(absOld, oldStem+".eng.srt"), Language: "eng", Format: "srt"},
	}); err != nil {
		t.Fatal(err)
	}

	newDir := "Green.Snake.1993.BluRay.1080p.x264-HDChina"
	newStem := newDir
	absNew := filepath.Join("/mnt/Media/Movie", newDir)
	if err := s.RenameMovieFileRelease(ctx, mf.ID, newDir, newStem+".mkv",
		filepath.Join(absNew, newStem+".nfo"), absNew); err != nil {
		t.Fatal(err)
	}

	got, err := s.FindMovieFileByRelease(ctx, lib.ID, newDir, newStem+".mkv")
	if err != nil {
		t.Fatalf("row not found under the new release key: %v", err)
	}
	if got.DirPath != newDir || got.FileName != newStem+".mkv" {
		t.Errorf("release key not updated: dir=%q file=%q", got.DirPath, got.FileName)
	}
	wantNFO := filepath.Join(absNew, newStem+".nfo")
	if got.NFOPath != wantNFO {
		t.Errorf("nfo_path\n  got  %q\n  want %q", got.NFOPath, wantNFO)
	}

	subs, err := s.ListSubtitles(ctx, mf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 2 {
		t.Fatalf("expected 2 subtitles, got %d", len(subs))
	}
	for _, sub := range subs {
		if dir := filepath.Dir(sub.FilePath); dir != absNew {
			t.Errorf("subtitle still in the old dir: %q", sub.FilePath)
		}
		base := filepath.Base(sub.FilePath)
		if base != newStem+"."+sub.Language+".srt" {
			t.Errorf("subtitle basename not rebuilt: %q (lang %q)", base, sub.Language)
		}
	}
}
