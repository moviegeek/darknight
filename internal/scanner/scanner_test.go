package scanner_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/moviegeek/darknight/internal/ffprobe"
	"github.com/moviegeek/darknight/internal/model"
	"github.com/moviegeek/darknight/internal/scanner"
	"github.com/moviegeek/darknight/internal/store"
	"log/slog"
)

// fakeProbeRaw is the verbatim ffprobe JSON fakeProbe returns, so tests can
// assert the scanner caches it unchanged.
var fakeProbeRaw = []byte(`{"format":{"filename":"x","format_name":"matroska,webm","duration":"7380.500000","bit_rate":"28500000","size":"26300000000"},"streams":[{"index":0,"codec_type":"video","codec_name":"hevc","width":3840,"height":2160,"avg_frame_rate":"24000/1001","side_data_list":[{"x":{"side_data_type":"DOVI configuration record"}}]},{"index":1,"codec_type":"audio","codec_name":"dts","channels":8,"disposition":{"default":1},"tags":{"language":"eng","title":"DTS-HD MA 7.1"}},{"index":2,"codec_type":"audio","codec_name":"ac3","channels":6,"tags":{"language":"chi"}},{"index":3,"codec_type":"subtitle","codec_name":"ass","tags":{"language":"chi"},"disposition":{"default":1}}]}`)

// fakeProbe returns canned ffprobe output so tests don't depend on real media
// files or a system ffmpeg.
func fakeProbe(_ context.Context, _ string) (*ffprobe.Result, []byte, error) {
	return &ffprobe.Result{
		Format: ffprobe.Format{
			FormatName: "matroska,webm",
			Duration:   "7380.500000",
			BitRate:    "28500000",
			Size:       "26300000000",
		},
		Streams: []ffprobe.Stream{
			{
				CodecType: "video", CodecName: "hevc",
				Width: 3840, Height: 2160,
				AvgFrameRate: "24000/1001",
				SideDataList: []map[string]json.RawMessage{
					{"x": json.RawMessage(`"DOVI configuration record"`)},
				},
			},
			{CodecType: "audio", CodecName: "dts", Channels: 8, Disposition: ffprobe.Disposition{Default: 1},
				Tags: ffprobe.Tags{"language": "eng", "title": "DTS-HD MA 7.1"}},
			{CodecType: "audio", CodecName: "ac3", Channels: 6,
				Tags: ffprobe.Tags{"language": "chi"}},
			{CodecType: "subtitle", CodecName: "ass",
				Tags: ffprobe.Tags{"language": "chi"}, Disposition: ffprobe.Disposition{Default: 1}},
		},
	}, fakeProbeRaw, nil
}

func TestScanLibrary_FileRelease(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	root := t.TempDir()
	// build a release dir with an mkv + nfo + external subtitle
	relDir := filepath.Join(root, "Casino.1995.UHD.BluRay.2160p.10bit.HDR.DTS-X.7.1.x265-beAst")
	if err := os.MkdirAll(relDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mkvPath := filepath.Join(relDir, "Casino.1995.UHD.BluRay.2160p.10bit.HDR.DTS-X.7.1.x265-beAst.mkv")
	if err := os.WriteFile(mkvPath, []byte("fake-mkv"), 0o644); err != nil {
		t.Fatal(err)
	}
	nfoBody := `<?xml version="1.0" encoding="UTF-8"?>
<movie>
  <title>Casino</title>
  <year>1995</year>
  <imdbid>tt0112641</imdbid>
  <uniqueid type="tmdb">524</uniqueid>
  <runtime>178</runtime>
  <genre>Crime / Drama</genre>
</movie>`
	if err := os.WriteFile(filepath.Join(relDir, "Casino.1995.UHD.BluRay.2160p.10bit.HDR.DTS-X.7.1.x265-beAst.nfo"),
		[]byte(nfoBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(relDir, "Casino.1995.UHD.BluRay.2160p.10bit.HDR.DTS-X.7.1.x265-beAst.chi.srt"),
		[]byte("1\n00:00:01 --> 00:00:02\nhi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	lib, err := s.CreateLibrary(ctx, "Films", root, 0)
	if err != nil {
		t.Fatal(err)
	}

	sc := &scanner.Scanner{Store: s, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), FFProbe: fakeProbe}
	stats, err := sc.ScanLibrary(ctx, lib)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if stats.Added != 1 || stats.Updated != 0 {
		t.Fatalf("expected 1 added, got %+v", stats)
	}

	// verify the movie + movie_file landed
	movies, err := s.ListMovies(ctx, store.ListMoviesOpts{Filter: store.MovieFilter{Query: "Casino"}})
	if err != nil {
		t.Fatalf("list movies: %v", err)
	}
	if len(movies) != 1 {
		t.Fatalf("expected 1 movie, got %d", len(movies))
	}
	m := movies[0]
	if m.IMDBID != "tt0112641" || m.TMDBID != 524 || m.Runtime != 178 {
		t.Fatalf("unexpected movie: %+v", m)
	}

	files, err := s.ListMovieFiles(ctx, m.ID)
	if err != nil {
		t.Fatalf("list files: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	mf := files[0]
	if mf.Resolution != "2160p" || mf.Source != "UHD BluRay" || mf.VideoCodec != "x265" {
		t.Fatalf("unexpected parsed fields: %+v", mf)
	}
	if mf.Width != 3840 || mf.Height != 2160 || mf.DurationSec != 7380.5 {
		t.Fatalf("unexpected probe fields: %+v", mf)
	}
	if !mf.DolbyVision {
		t.Fatalf("expected dolby_vision from side data, got %+v", mf)
	}
	if mf.FFProbeJSON == "" || mf.FFProbeVersion != ffprobe.ProbeVersion {
		t.Fatalf("expected ffprobe cache populated, got json=%q version=%d", mf.FFProbeJSON, mf.FFProbeVersion)
	}
	if mf.FFProbeJSON != string(fakeProbeRaw) {
		t.Fatalf("ffprobe_json should be verbatim raw output, got %q", mf.FFProbeJSON)
	}

	tracks, err := s.ListAudioTracks(ctx, mf.ID)
	if err != nil {
		t.Fatalf("list audio: %v", err)
	}
	if len(tracks) != 2 || !tracks[0].IsDefault || tracks[1].Language != "chi" {
		t.Fatalf("unexpected tracks: %+v", tracks)
	}
	subs, err := s.ListSubtitles(ctx, mf.ID)
	if err != nil {
		t.Fatalf("list subs: %v", err)
	}
	// 1 embedded + 1 external = 2
	if len(subs) != 2 {
		t.Fatalf("expected 2 subs, got %d: %+v", len(subs), subs)
	}

	// second scan: unchanged (same size+mtime) => no probe, counted unchanged
	stats2, err := sc.ScanLibrary(ctx, lib)
	if err != nil {
		t.Fatalf("scan2: %v", err)
	}
	if stats2.Added != 0 || stats2.Updated != 0 || stats2.Unchanged != 1 {
		t.Fatalf("expected 1 unchanged on rescan, got %+v", stats2)
	}
	// unchanged rescan must preserve the cached ffprobe JSON verbatim
	files2, err := s.ListMovieFiles(ctx, m.ID)
	if err != nil {
		t.Fatalf("list files after rescan: %v", err)
	}
	if len(files2) != 1 || files2[0].FFProbeJSON != string(fakeProbeRaw) ||
		files2[0].FFProbeVersion != ffprobe.ProbeVersion {
		t.Fatalf("expected ffprobe cache preserved on unchanged rescan, got %+v", files2)
	}

	// delete the release dir; rescan should prune it
	if err := os.RemoveAll(relDir); err != nil {
		t.Fatal(err)
	}
	stats3, err := sc.ScanLibrary(ctx, lib)
	if err != nil {
		t.Fatalf("scan3: %v", err)
	}
	if stats3.Removed != 1 {
		t.Fatalf("expected 1 removed, got %+v", stats3)
	}
}

func TestScanLibrary_DiscRelease(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	root := t.TempDir()
	relDir := filepath.Join(root, "Downfall.2004.1080p.USA.BluRay.AVC.DTS-HD.MA.5.1-DiY@HDHome")
	streamDir := filepath.Join(relDir, "BDMV", "STREAM")
	if err := os.MkdirAll(streamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(relDir, "CERTIFICATE"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(streamDir, "00000.m2ts"),
		[]byte("fake-m2ts-payload-1234567890"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(streamDir, "00001.m2ts"),
		[]byte("fake-m2ts-payload-0987654321"), 0o644); err != nil {
		t.Fatal(err)
	}

	lib, err := s.CreateLibrary(ctx, "Films", root, 0)
	if err != nil {
		t.Fatal(err)
	}
	sc := &scanner.Scanner{Store: s, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), FFProbe: fakeProbe}

	stats, err := sc.ScanLibrary(ctx, lib)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if stats.Added != 1 {
		t.Fatalf("expected 1 added, got %+v", stats)
	}
	movies, err := s.ListMovies(ctx, store.ListMoviesOpts{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(movies) != 1 || movies[0].Title != "Downfall" || movies[0].Year != 2004 {
		t.Fatalf("unexpected movie: %+v", movies)
	}
	files, err := s.ListMovieFiles(ctx, movies[0].ID)
	if err != nil {
		t.Fatalf("files: %v", err)
	}
	if len(files) != 1 || !files[0].IsDisc {
		t.Fatalf("expected 1 disc file, got %+v", files)
	}
	if files[0].VideoCodec != "AVC" || files[0].Source != "Bluray Disk" {
		t.Fatalf("unexpected parsed disc fields: %+v", files[0])
	}
}

// TestScanLibrary_MultiDiscBoxset verifies a boxset-style folder that groups
// several Blu-ray disc releases in subdirectories (each with its own BDMV
// folder) is scanned as one movie_file per disc. This is the structure used
// for multi-disc releases, e.g.:
//
//	Interstellar...-HDclub/
//	  Interstellar...Disc.1-HDclub/BDMV/STREAM/00000.m2ts
//	  Interstellar...Disc.2.Bonus-HDclub/BDMV/STREAM/00000.m2ts
func TestScanLibrary_MultiDiscBoxset(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	root := t.TempDir()
	boxset := filepath.Join(root, "Interstellar.2014.IMAX.Edition.Blu-ray.CEE.1080p.AVC.DTS-HD.MA5.1-HDclub")
	for _, disc := range []string{
		"Interstellar.2014.IMAX.Edition.Blu-ray.CEE.1080p.AVC.DTS-HD.MA5.1.Disc.1-HDclub",
		"Interstellar.2014.IMAX.Edition.Blu-ray.CEE.1080p.AVC.DTS-HD.MA5.1.Disc.2.Bonus-HDclub",
	} {
		streamDir := filepath.Join(boxset, disc, "BDMV", "STREAM")
		if err := os.MkdirAll(streamDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(streamDir, "00000.m2ts"),
			[]byte("fake-m2ts-payload-1234567890"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	lib, err := s.CreateLibrary(ctx, "Films", root, 0)
	if err != nil {
		t.Fatal(err)
	}
	sc := &scanner.Scanner{Store: s, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), FFProbe: fakeProbe}

	stats, err := sc.ScanLibrary(ctx, lib)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if stats.Added != 2 {
		t.Fatalf("expected 2 added (one per disc), got %+v", stats)
	}
	movies, err := s.ListMovies(ctx, store.ListMoviesOpts{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(movies) != 1 || movies[0].Title != "Interstellar" || movies[0].Year != 2014 {
		t.Fatalf("expected one Interstellar movie, got %+v", movies)
	}
	files, err := s.ListMovieFiles(ctx, movies[0].ID)
	if err != nil {
		t.Fatalf("files: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 disc files, got %d", len(files))
	}
	for _, f := range files {
		if !f.IsDisc || f.Source != "Bluray Disk" {
			t.Fatalf("expected disc release with Bluray Disk source, got %+v", f)
		}
	}
}

func TestScanLibrary_FFProbeVersionBump(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	root := t.TempDir()
	relDir := filepath.Join(root, "Heat.1995.BluRay.1080p.x264-GROUP")
	if err := os.MkdirAll(relDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mkvPath := filepath.Join(relDir, "Heat.1995.BluRay.1080p.x264-GROUP.mkv")
	if err := os.WriteFile(mkvPath, []byte("fake-mkv"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib, err := s.CreateLibrary(ctx, "Films", root, 0)
	if err != nil {
		t.Fatal(err)
	}
	sc := &scanner.Scanner{Store: s, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), FFProbe: fakeProbe}

	// first scan: probes + caches JSON at the current ProbeVersion
	if _, err := sc.ScanLibrary(ctx, lib); err != nil {
		t.Fatalf("scan1: %v", err)
	}
	movies, err := s.ListMovies(ctx, store.ListMoviesOpts{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(movies) != 1 {
		t.Fatalf("expected 1 movie, got %d", len(movies))
	}
	files, err := s.ListMovieFiles(ctx, movies[0].ID)
	if err != nil {
		t.Fatalf("files: %v", err)
	}
	if files[0].FFProbeJSON == "" || files[0].FFProbeVersion != ffprobe.ProbeVersion {
		t.Fatalf("expected cache populated on first scan: %+v", files[0])
	}

	// second scan: size+mtime unchanged -> unchanged, cache preserved
	stats2, err := sc.ScanLibrary(ctx, lib)
	if err != nil {
		t.Fatalf("scan2: %v", err)
	}
	if stats2.Unchanged != 1 {
		t.Fatalf("expected 1 unchanged, got %+v", stats2)
	}

	// bump the expected probe version: same size+mtime, but the cache is now
	// stale -> the file must be re-probed and counted updated.
	sc.ProbeVersion = ffprobe.ProbeVersion + 1
	stats3, err := sc.ScanLibrary(ctx, lib)
	if err != nil {
		t.Fatalf("scan3: %v", err)
	}
	if stats3.Updated != 1 {
		t.Fatalf("expected 1 updated after version bump, got %+v", stats3)
	}
	files3, err := s.ListMovieFiles(ctx, movies[0].ID)
	if err != nil {
		t.Fatalf("files3: %v", err)
	}
	if files3[0].FFProbeVersion != ffprobe.ProbeVersion+1 || files3[0].FFProbeJSON == "" {
		t.Fatalf("expected cache refreshed at new version, got %+v", files3[0])
	}
}

// newTestStore mirrors the store package's helper, duplicated here to avoid an
// import cycle on the test binary.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scan.db")
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// satisfy unused import of model in case helpers reference it.
var _ = model.Movie{}
