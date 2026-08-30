package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/moviegeek/darknight/internal/ffprobe"
	"github.com/moviegeek/darknight/internal/model"
	"github.com/moviegeek/darknight/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpen_RunsMigrationsIdempotently(t *testing.T) {
	s := newTestStore(t)
	// re-open the same db; migrations should be a no-op and not error.
	ctx := context.Background()
	if err := s.DB.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestLibraryCRUD(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	lib, err := s.CreateLibrary(ctx, "Films", "/movies", 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if lib.ID == 0 {
		t.Fatal("expected non-zero id")
	}

	got, err := s.GetLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Films" || got.RootPath != "/movies" {
		t.Fatalf("unexpected library: %+v", got)
	}

	libs, err := s.ListLibraries(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(libs) != 1 {
		t.Fatalf("expected 1 library, got %d", len(libs))
	}

	if err := s.DeleteLibrary(ctx, lib.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetLibrary(ctx, lib.ID); err != store.ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestUpsertMovie_Idempotent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	m1 := &model.Movie{Title: "1917", Year: 2019, TMDBID: 530485, IMDBID: "tt7366338"}
	if err := s.UpsertMovie(ctx, m1); err != nil {
		t.Fatalf("insert: %v", err)
	}
	id1 := m1.ID

	// re-upsert by tmdb_id — should update, not duplicate.
	m2 := &model.Movie{Title: "1917", Year: 2019, TMDBID: 530485, VoteAverage: 8.4}
	if err := s.UpsertMovie(ctx, m2); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if m2.ID != id1 {
		t.Fatalf("expected same id %d, got %d", id1, m2.ID)
	}

	// re-upsert by (title, year) when tmdb_id absent — should match the same row.
	m3 := &model.Movie{Title: "1917", Year: 2019}
	if err := s.UpsertMovie(ctx, m3); err != nil {
		t.Fatalf("upsert by title/year: %v", err)
	}
	if m3.ID != id1 {
		t.Fatalf("expected title/year match to id %d, got %d", id1, m3.ID)
	}

	got, err := s.GetMovie(ctx, id1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.VoteAverage != 8.4 {
		t.Fatalf("expected vote_average 8.4 to persist, got %v", got.VoteAverage)
	}
}

// TestUpsertMovieSeed_MatchesEnrichedRow reproduces the duplicate-movie bug:
// the enricher overwrote title with the TMDB original title and kept the
// parsed English name only in title_en. A re-scan reparsing the English name
// must match the enriched row by title_en instead of inserting a duplicate.
func TestUpsertMovieSeed_MatchesEnrichedRow(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// enriched row, as the TMDB enricher would leave it
	enriched := &model.Movie{
		Title:         "十三人の刺客",
		OriginalTitle: "十三人の刺客",
		TitleEn:       "13 Assassins",
		Year:          2010, TMDBID: 58857, IMDBID: "tt1436045",
		PosterPath: "/yUsVsiVXUwClk4vSCx80vrdO6MV.jpg", Synopsis: "samurai",
	}
	if err := s.UpsertMovie(ctx, enriched); err != nil {
		t.Fatalf("insert enriched: %v", err)
	}
	enrichedID := enriched.ID

	// scanner re-parses the English name from the dir; no ids.
	seed := &model.Movie{Title: "13 Assassins", Year: 2010}
	if err := s.UpsertMovieSeed(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if seed.ID != enrichedID {
		t.Fatalf("seed should match enriched row %d, got %d", enrichedID, seed.ID)
	}

	// enriched display fields must be untouched by the seed update.
	got, err := s.GetMovie(ctx, enrichedID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "十三人の刺客" || got.TitleEn != "13 Assassins" ||
		got.PosterPath != "/yUsVsiVXUwClk4vSCx80vrdO6MV.jpg" || got.Synopsis != "samurai" {
		t.Fatalf("enriched fields clobbered by seed: %+v", got)
	}

	// no duplicate row should have been created.
	var n int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM movies WHERE year = 2010`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 row for year 2010, got %d", n)
	}
}

// TestUpsertMovieSeed_MatchesByOriginalTitle covers the symmetric case: the
// parsed title is the English name, but the stored row carries it in
// original_title (not title_en).
func TestUpsertMovieSeed_MatchesByOriginalTitle(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	enriched := &model.Movie{
		Title:         "大撒把",
		OriginalTitle: "After Separation",
		TitleEn:       "After Separation",
		Year:          1992, TMDBID: 296147,
	}
	if err := s.UpsertMovie(ctx, enriched); err != nil {
		t.Fatalf("insert: %v", err)
	}

	seed := &model.Movie{Title: "After Separation", Year: 1992}
	if err := s.UpsertMovieSeed(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if seed.ID != enriched.ID {
		t.Fatalf("seed should match by original_title, got %d want %d", seed.ID, enriched.ID)
	}
}

// TestUpsertMovieSeed_UpdatesUnenrichedRow verifies the seed path still
// updates a matched row that has no tmdb_id (first-time seed before enrich,
// or offline mode), so re-scans pick up corrected parse fields.
func TestUpsertMovieSeed_UpdatesUnenrichedRow(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	first := &model.Movie{Title: "Casino", Year: 1995}
	if err := s.UpsertMovie(ctx, first); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// re-scan with a richer parse (runtime via .nfo); row has no tmdb_id yet.
	seed := &model.Movie{Title: "Casino", Year: 1995, Runtime: 178, IMDBID: "tt0112641"}
	if err := s.UpsertMovieSeed(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if seed.ID != first.ID {
		t.Fatalf("seed should match existing row, got %d want %d", seed.ID, first.ID)
	}
	got, err := s.GetMovie(ctx, first.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Runtime != 178 || got.IMDBID != "tt0112641" {
		t.Fatalf("unenriched row not updated: %+v", got)
	}
}

func TestMovieFileUpsertAndTracks(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	lib, err := s.CreateLibrary(ctx, "Films", "/movies", 0)
	if err != nil {
		t.Fatal(err)
	}
	movie := &model.Movie{Title: "Casino", Year: 1995}
	if err := s.UpsertMovie(ctx, movie); err != nil {
		t.Fatal(err)
	}

	mf := &model.MovieFile{
		MovieID: movie.ID, LibraryID: lib.ID,
		DirPath: "/movies/Casino", FileName: "Casino.mkv",
		FileSize: 30_000_000_000, FileModified: time.Now().Unix(),
		Source: "UHD BluRay", Resolution: "2160p", VideoCodec: "x265",
		AudioCodec: "DTS-HD MA", AudioChannels: "7.1", HDR: "HDR10", BitDepth: 10,
		RawName: "Casino.1995.UHD.BluRay.2160p.10bit.HDR.DTS-X.7.1.x265-beAst",
		ReleaseGroup: "beAst", DurationSec: 10620, Width: 3840, Height: 2160,
	}
	if err := s.UpsertMovieFile(ctx, mf); err != nil {
		t.Fatalf("insert movie_file: %v", err)
	}
	id1 := mf.ID

	// upsert again by (library, dir) — should update, not duplicate.
	mf2 := *mf
	mf2.FileSize = 31_000_000_000
	if err := s.UpsertMovieFile(ctx, &mf2); err != nil {
		t.Fatalf("upsert movie_file: %v", err)
	}
	if mf2.ID != id1 {
		t.Fatalf("expected same movie_file id %d, got %d", id1, mf2.ID)
	}

	// audio tracks + subtitles round-trip
	tracks := []model.AudioTrack{
		{Language: "eng", Codec: "DTS-HD MA", Channels: 8, IsDefault: true, IsLossless: true, Order: 0},
		{Language: "chi", Codec: "AC-3", Channels: 6, Order: 1},
	}
	if err := s.ReplaceAudioTracks(ctx, id1, tracks); err != nil {
		t.Fatalf("replace audio: %v", err)
	}
	gotTracks, err := s.ListAudioTracks(ctx, id1)
	if err != nil {
		t.Fatalf("list audio: %v", err)
	}
	if len(gotTracks) != 2 || !gotTracks[0].IsLossless || gotTracks[1].Language != "chi" {
		t.Fatalf("unexpected tracks: %+v", gotTracks)
	}

	subs := []model.Subtitle{
		{FilePath: "/movies/Casino/Casino.chi.srt", Language: "chi", Format: "srt", Order: 0},
		{Language: "eng", Format: "pgs", IsEmbedded: true, Order: 1},
	}
	if err := s.ReplaceSubtitles(ctx, id1, subs); err != nil {
		t.Fatalf("replace subs: %v", err)
	}
	gotSubs, err := s.ListSubtitles(ctx, id1)
	if err != nil {
		t.Fatalf("list subs: %v", err)
	}
	if len(gotSubs) != 2 || gotSubs[1].IsEmbedded != true {
		t.Fatalf("unexpected subs: %+v", gotSubs)
	}

	// list movies with a resolution filter should find this movie.
	ms, err := s.ListMovies(ctx, store.ListMoviesOpts{
		Filter: store.MovieFilter{Resolution: "2160p"},
	})
	if err != nil {
		t.Fatalf("list movies: %v", err)
	}
	if len(ms) != 1 || ms[0].Title != "Casino" {
		t.Fatalf("expected 1 Casino, got %+v", ms)
	}

	// DolbyVision flag filter (no placeholder) should also work and miss here.
	msDV, err := s.ListMovies(ctx, store.ListMoviesOpts{
		Filter: store.MovieFilter{DolbyVision: true},
	})
	if err != nil {
		t.Fatalf("list movies DV: %v", err)
	}
	if len(msDV) != 0 {
		t.Fatalf("expected 0 DV movies, got %d", len(msDV))
	}
	// HDR filter should hit the one Casino file we inserted (hdr=HDR10).
	msHDR, err := s.ListMovies(ctx, store.ListMoviesOpts{
		Filter: store.MovieFilter{HDR: "HDR10"},
	})
	if err != nil {
		t.Fatalf("list movies HDR: %v", err)
	}
	if len(msHDR) != 1 {
		t.Fatalf("expected 1 HDR10 movie, got %d", len(msHDR))
	}
	n, _ := s.CountMovies(ctx, store.MovieFilter{HDR: "HDR10"})
	if n != 1 {
		t.Fatalf("count HDR10 = %d, want 1", n)
	}

	// stale-file pruning: keep a different release key => old movie_file deleted,
	// and the empty-keep guard prunes nothing even when the library has rows.
	pruned, err := s.RemoveStaleMovieFiles(ctx, lib.ID, []string{"/movies/Casino\x00Other.mkv"})
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("expected 1 pruned, got %d", pruned)
	}
	files, err := s.ListMovieFiles(ctx, movie.ID)
	if err != nil {
		t.Fatalf("list files: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected 0 files after prune, got %d", len(files))
	}
}

// TestEmptyListsAreNotNull guards against a regression where a nil slice
// serialises to JSON `null` and crashes the frontend (which reads .length).
// Every list helper must return an empty, non-nil slice when there are no rows.
func TestEmptyListsAreNotNull(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	libs, err := s.ListLibraries(ctx)
	if err != nil || libs == nil {
		t.Fatalf("ListLibraries: nil=%v err=%v", libs == nil, err)
	}
	movies, err := s.ListMovies(ctx, store.ListMoviesOpts{})
	if err != nil || movies == nil {
		t.Fatalf("ListMovies: nil=%v err=%v", movies == nil, err)
	}
	files, err := s.ListMovieFiles(ctx, 1)
	if err != nil || files == nil {
		t.Fatalf("ListMovieFiles: nil=%v err=%v", files == nil, err)
	}
	tracks, err := s.ListAudioTracks(ctx, 1)
	if err != nil || tracks == nil {
		t.Fatalf("ListAudioTracks: nil=%v err=%v", tracks == nil, err)
	}
	subs, err := s.ListSubtitles(ctx, 1)
	if err != nil || subs == nil {
		t.Fatalf("ListSubtitles: nil=%v err=%v", subs == nil, err)
	}
}

// TestMovieFile_FFProbeCacheRoundTrip verifies the verbatim ffprobe JSON and its
// version/timestamp survive an upsert + read back through both the
// FindMovieFileByRelease and ListMovieFiles paths.
func TestMovieFile_FFProbeCacheRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	lib, err := s.CreateLibrary(ctx, "Films", "/movies", 0)
	if err != nil {
		t.Fatal(err)
	}
	movie := &model.Movie{Title: "Heat", Year: 1995}
	if err := s.UpsertMovie(ctx, movie); err != nil {
		t.Fatal(err)
	}

	mf := &model.MovieFile{
		MovieID: movie.ID, LibraryID: lib.ID,
		DirPath: "/movies/Heat", FileName: "Heat.mkv", RawName: "Heat",
		Resolution: "2160p", Source: "UHD BluRay", VideoCodec: "x265",
		FFProbeJSON: `{"streams":[]}`, FFProbeVersion: 2, FFProbeAt: 999,
	}
	if err := s.UpsertMovieFile(ctx, mf); err != nil {
		t.Fatalf("insert movie_file: %v", err)
	}

	got, err := s.FindMovieFileByRelease(ctx, lib.ID, "/movies/Heat", "Heat.mkv")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.FFProbeJSON != `{"streams":[]}` || got.FFProbeVersion != 2 || got.FFProbeAt != 999 {
		t.Fatalf("ffprobe cache not round-tripped via FindMovieFileByRelease: %+v", got)
	}

	files, err := s.ListMovieFiles(ctx, movie.ID)
	if err != nil {
		t.Fatalf("list files: %v", err)
	}
	if len(files) != 1 ||
		files[0].FFProbeJSON != `{"streams":[]}` ||
		files[0].FFProbeVersion != 2 ||
		files[0].FFProbeAt != 999 {
		t.Fatalf("ffprobe cache not round-tripped via ListMovieFiles: %+v", files)
	}
}

// TestMovieFile_ReprobeList exercises the backfill helpers: a file with a
// missing (0) or stale (mismatched) ProbeVersion is returned for re-probing;
// once UpdateFFProbeCache writes the current version it drops out.
func TestMovieFile_ReprobeList(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	lib, err := s.CreateLibrary(ctx, "Films", "/movies", 0)
	if err != nil {
		t.Fatal(err)
	}
	movie := &model.Movie{Title: "M", Year: 2000}
	if err := s.UpsertMovie(ctx, movie); err != nil {
		t.Fatal(err)
	}

	mk := func(dir string, version int) *model.MovieFile {
		return &model.MovieFile{
			MovieID: movie.ID, LibraryID: lib.ID,
			DirPath: dir, FileName: "x.mkv", RawName: dir,
			Resolution: "1080p", FFProbeVersion: version,
		}
	}
	if err := s.UpsertMovieFile(ctx, mk("/movies/missing", 0)); err != nil {
		t.Fatal(err)
	}
	stale := mk("/movies/stale", 999)
	if err := s.UpsertMovieFile(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertMovieFile(ctx, mk("/movies/current", ffprobe.ProbeVersion)); err != nil {
		t.Fatal(err)
	}

	cur := ffprobe.ProbeVersion
	need, err := s.ListMovieFilesForReprobe(ctx, cur)
	if err != nil {
		t.Fatalf("list for reprobe: %v", err)
	}
	if len(need) != 2 {
		t.Fatalf("expected 2 files needing reprobe (missing + stale), got %d", len(need))
	}

	if err := s.UpdateFFProbeCache(ctx, stale.ID, `{"fresh":true}`, cur, 123); err != nil {
		t.Fatalf("update cache: %v", err)
	}
	need2, err := s.ListMovieFilesForReprobe(ctx, cur)
	if err != nil {
		t.Fatalf("list for reprobe 2: %v", err)
	}
	if len(need2) != 1 {
		t.Fatalf("expected 1 file still needing reprobe after backfill, got %d", len(need2))
	}
}

// TestUpsertMovie_BilingualTitles verifies the bilingual title columns persist
// and that a re-upsert with empty variants preserves them (COALESCE(NULLIF...)).
func TestUpsertMovie_BilingualTitles(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	m := &model.Movie{
		Title: "The Matrix", Year: 1999, TMDBID: 603,
		OriginalTitle: "The Matrix", OriginalLanguage: "en",
		TitleEn: "The Matrix", TitleZh: "黑客帝国", Country: "US",
	}
	if err := s.UpsertMovie(ctx, m); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := s.GetMovie(ctx, m.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.OriginalTitle != "The Matrix" || got.OriginalLanguage != "en" ||
		got.TitleEn != "The Matrix" || got.TitleZh != "黑客帝国" || got.Country != "US" {
		t.Fatalf("bilingual fields not persisted: %+v", got)
	}

	// re-upsert by tmdb_id with empty variants: COALESCE must preserve, not blank.
	m2 := &model.Movie{Title: "The Matrix", Year: 1999, TMDBID: 603}
	if err := s.UpsertMovie(ctx, m2); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got2, err := s.GetMovie(ctx, m.ID)
	if err != nil {
		t.Fatalf("get2: %v", err)
	}
	if got2.TitleZh != "黑客帝国" || got2.OriginalLanguage != "en" ||
		got2.Country != "US" || got2.OriginalTitle != "The Matrix" {
		t.Fatalf("COALESCE did not preserve bilingual fields on re-upsert: %+v", got2)
	}
}

// TestListMovies_SearchAcrossTitleVariants verifies the q filter matches any of
// the stored title variants (title / original_title / title_en / title_zh).
func TestListMovies_SearchAcrossTitleVariants(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	movies := []*model.Movie{
		{Title: "The Matrix", Year: 1999, TMDBID: 603,
			OriginalTitle: "The Matrix", OriginalLanguage: "en",
			TitleEn: "The Matrix", TitleZh: "黑客帝国", Country: "US"},
		{Title: "Inception", Year: 2010, TMDBID: 27205,
			OriginalTitle: "Inception", OriginalLanguage: "en",
			TitleEn: "Inception", TitleZh: "盗梦空间", Country: "US"},
	}
	// attach a file to each row: the default list hides file-less movies, and
	// this test exercises search, not the data-health buckets.
	for _, m := range movies {
		seedMovieWithFile(t, s, m, true)
	}

	find := func(q string) int {
		t.Helper()
		ms, err := s.ListMovies(ctx, store.ListMoviesOpts{Filter: store.MovieFilter{Query: q}})
		if err != nil {
			t.Fatalf("list q=%q: %v", q, err)
		}
		return len(ms)
	}
	if n := find("黑客"); n != 1 { // matches title_zh only
		t.Fatalf("search title_zh '黑客' -> %d, want 1", n)
	}
	if n := find("Matrix"); n != 1 { // matches title / title_en
		t.Fatalf("search 'Matrix' -> %d, want 1", n)
	}
	if n := find("盗梦"); n != 1 { // matches the other movie's title_zh
		t.Fatalf("search title_zh '盗梦' -> %d, want 1", n)
	}
	if n := find("zzz"); n != 0 {
		t.Fatalf("search 'zzz' -> %d, want 0", n)
	}
}
