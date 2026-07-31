// Package scanner walks a media library on disk and persists structured
// metadata to the store. Each scan is incremental: a release whose main file
// mtime+size is unchanged is skipped (no ffprobe call).
//
// Directory model:
//
//	root_path/
//	  Movie.Title.YEAR....-Group/      <- one release
//	    Movie.Title.YEAR....-Group.mkv
//	    Movie.Title.YEAR....-Group.nfo
//	    Movie.Title.YEAR....-Group.chi.srt
//	  AnotherBluray/
//	    BDMV/STREAM/00000.m2ts          <- disc-folder release
//	    CERTIFICATE/
//
// A "release" is a directory that directly contains a video file or a BDMV
// folder. Subdirectories that contain only more directories are descended
// into (this handles boxsets / anthology folders that group several films).
package scanner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/moviegeek/darknight/internal/ffprobe"
	"github.com/moviegeek/darknight/internal/metadata"
	"github.com/moviegeek/darknight/internal/model"
	"github.com/moviegeek/darknight/internal/nfo"
	"github.com/moviegeek/darknight/internal/parser"
	"github.com/moviegeek/darknight/internal/store"
)

// videoExtensions are file extensions treated as the main video of a release.
var videoExtensions = map[string]bool{
	".mkv": true, ".mp4": true, ".m2ts": true, ".ts": true, ".avi": true,
	".mov": true, ".wmv": true, ".iso": true, ".m4v": true,
}

// subtitleExtensions and their language/format detection.
var subtitleExtensions = map[string]string{
	".srt": "srt", ".ass": "ass", ".ssa": "ssa", ".sub": "sub", ".idx": "vobsub",
}

// Scanner coordinates a scan of one library.
type Scanner struct {
	Store   *store.Store
	Logger  *slog.Logger
	FFProbe func(ctx context.Context, path string) (*ffprobe.Result, []byte, error)
	// ProbeVersion, when non-zero, overrides ffprobe.ProbeVersion for the
	// stale-cache check. Tests use it to simulate a version bump without
	// touching the package constant. Zero defaults to ffprobe.ProbeVersion.
	ProbeVersion int
	// Enricher, when non-nil and enabled, fills TMDB metadata for each movie
	// discovered during a scan. Safe to leave nil for offline-only mode.
	Enricher *metadata.Enricher
}

// probeVersion returns the effective probe version for the stale-cache check.
func (sc *Scanner) probeVersion() int {
	if sc.ProbeVersion != 0 {
		return sc.ProbeVersion
	}
	return ffprobe.ProbeVersion
}

// New returns a Scanner using the default ffprobe binary.
func New(s *store.Store, log *slog.Logger) *Scanner {
	if log == nil {
		log = slog.Default()
	}
	return &Scanner{Store: s, Logger: log, FFProbe: ffprobe.Probe}
}

// Stats summarises a single library scan.
type Stats struct {
	Added     int
	Updated   int
	Unchanged int
	Removed   int
	Errors    int
}

// ScanLibrary walks lib.RootPath, classifies each release, and upserts it.
// It returns aggregate stats. Errors on individual releases are counted but do
// not abort the scan; the whole scan only aborts on a fatal walk error.
func (sc *Scanner) ScanLibrary(ctx context.Context, lib *model.Library) (Stats, error) {
	var stats Stats
	seen := make([]string, 0, 256)
	sc.Logger.Info("scanning library", "library", lib.Name, "root", lib.RootPath)

	err := filepath.WalkDir(lib.RootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// a missing root is fatal; a missing subdir is just logged.
			if path == lib.RootPath {
				return err
			}
			sc.Logger.Warn("walk error", "path", path, "err", err)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		}
		if d == nil || !d.IsDir() {
			return nil
		}
		// skip hidden dirs (.@__thumb on Synology, .DS_Store folders, ...)
		if path != lib.RootPath && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}

		rel, err := filepath.Rel(lib.RootPath, path)
		if err != nil {
			rel = path
		}
		// only descend into the root and one level of grouping dirs (boxsets).
		// a release is a leaf dir containing files or a BDMV folder.
		depth := strings.Count(rel, string(filepath.Separator))
		if path != lib.RootPath && depth > 1 {
			// too deep: stop descending, the parent already classified it.
			return filepath.SkipDir
		}

		release, kind, ok := classifyRelease(path)
		if !ok {
			return nil // descend further
		}
		sc.Logger.Debug("found release", "dir", rel, "kind", kindString(kind), "main", release)
		seen = append(seen, rel)

		added, updated, unchanged, err := sc.scanRelease(ctx, lib, path, rel, release, kind, false)
		switch {
		case err != nil:
			stats.Errors++
			sc.Logger.Warn("scan release", "dir", path, "err", err)
		case added:
			stats.Added++
		case updated:
			stats.Updated++
		case unchanged:
			stats.Unchanged++
		}
		if kind == kindDisc {
			// a disc release is itself a leaf; do not descend into BDMV/STREAM.
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil && !errors.Is(err, filepath.SkipDir) {
		return stats, err
	}

	removed, err := sc.Store.RemoveStaleMovieFiles(ctx, lib.ID, seen)
	if err != nil {
		sc.Logger.Warn("prune stale", "err", err)
	} else {
		stats.Removed = int(removed)
		sc.Logger.Debug("prune stale", "library", lib.Name, "removed", removed)
	}
	if err := sc.Store.TouchLibraryScan(ctx, lib.ID, time.Now().Unix()); err != nil {
		sc.Logger.Warn("touch library", "err", err)
	}
	// WAL checkpoint so the scan results are immediately visible in the main
	// database file, even if the process is still running.
	if err := sc.Store.Checkpoint(ctx); err != nil {
		sc.Logger.Warn("checkpoint", "err", err)
	}
	return stats, nil
}

// RescanMovie re-scans every on-disk release backing a movie, forcing a full
// re-probe regardless of whether the main file's size/mtime changed. It is
// the "refresh" UI action's disk-side counterpart to Enricher.EnrichMovie:
// picks up video-file replacements and subtitle-only edits (new/removed
// .srt, embedded subtitle changes) without waiting for the next full
// library scan.
func (sc *Scanner) RescanMovie(ctx context.Context, movieID int64) (Stats, error) {
	var stats Stats
	files, err := sc.Store.ListMovieFiles(ctx, movieID)
	if err != nil {
		return stats, err
	}
	libs := make(map[int64]*model.Library)
	for _, mf := range files {
		lib, ok := libs[mf.LibraryID]
		if !ok {
			lib, err = sc.Store.GetLibrary(ctx, mf.LibraryID)
			if err != nil {
				stats.Errors++
				sc.Logger.Warn("rescan movie: get library", "movie_file", mf.ID, "err", err)
				continue
			}
			libs[mf.LibraryID] = lib
		}
		absDir := filepath.Join(lib.RootPath, mf.DirPath)
		kind := kindFile
		mainFile := ""
		if mf.IsDisc {
			kind = kindDisc
		} else {
			mainFile = filepath.Join(absDir, mf.FileName)
		}
		added, updated, unchanged, err := sc.scanRelease(ctx, lib, absDir, mf.DirPath, mainFile, kind, true)
		switch {
		case err != nil:
			stats.Errors++
			sc.Logger.Warn("rescan release", "dir", absDir, "err", err)
		case added:
			stats.Added++
		case updated:
			stats.Updated++
		case unchanged:
			stats.Unchanged++
		}
	}
	if err := sc.Store.Checkpoint(ctx); err != nil {
		sc.Logger.Warn("checkpoint", "err", err)
	}
	return stats, nil
}

// releaseKind tells apart a single-file release from a Blu-ray disc folder.
type releaseKind int

const (
	kindFile releaseKind = iota
	kindDisc
)

// kindString returns a human label for a release kind, for log lines.
func kindString(k releaseKind) string {
	if k == kindDisc {
		return "disc"
	}
	return "file"
}

// prevSize / prevMtime return the stored size / mtime of an existing release,
// or 0 when there is no prior record. Used for incremental-check debug logs.
func prevSize(mf *model.MovieFile) int64 {
	if mf == nil {
		return 0
	}
	return mf.FileSize
}

func prevMtime(mf *model.MovieFile) int64 {
	if mf == nil {
		return 0
	}
	return mf.FileModified
}

// classifyRelease inspects dir and returns the chosen main file path, the
// release kind, and ok=false if dir is not yet a leaf (no video / BDMV).
func classifyRelease(dir string) (mainFile string, kind releaseKind, ok bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0, false
	}
	// Blu-ray disc folder?
	for _, e := range entries {
		if e.IsDir() && strings.EqualFold(e.Name(), "BDMV") {
			return "", kindDisc, true
		}
	}
	// pick the largest video file in the dir
	var best string
	var bestSize int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if !videoExtensions[ext] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Size() > bestSize {
			bestSize = info.Size()
			best = e.Name()
		}
	}
	if best == "" {
		return "", 0, false
	}
	return filepath.Join(dir, best), kindFile, true
}

// scanRelease parses one release dir and upserts its movie_file. Returns
// (added, updated, unchanged, err). When unchanged, ffprobe and the
// audio/subtitle rewrite are skipped entirely. force bypasses the
// size/mtime incremental check so the release is always fully re-probed and
// its audio/subtitle rows always rewritten - used by the manual "rescan
// movie" action so subtitle-only changes (no video file change) are picked
// up without waiting for the next full library scan.
func (sc *Scanner) scanRelease(
	ctx context.Context,
	lib *model.Library,
	absDir, relDir, mainFile string, kind releaseKind, force bool,
) (bool, bool, bool, error) {
	dirName := filepath.Base(absDir)
	meta := parser.ParseTitle(dirName)

	// gather side files (.nfo, external subtitles)
	nfoPath, externalSubs := scanSideFiles(absDir)

	// determine size + mtime of the main artefact
	var size int64
	var mtime int64
	if kind == kindFile {
		fi, err := os.Stat(mainFile)
		if err != nil {
			return false, false, false, fmt.Errorf("stat main file: %w", err)
		}
		size = fi.Size()
		mtime = fi.ModTime().Unix()
	} else {
		// disc: sum STREAM/*.m2ts sizes, mtime = dir mtime
		size = discSize(absDir)
		fi, _ := os.Stat(absDir)
		if fi != nil {
			mtime = fi.ModTime().Unix()
		}
	}

	// incremental check: skip ffprobe if (size, mtime) unchanged
	existing, err := sc.Store.FindMovieFileByRelease(ctx, lib.ID, relDir)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return false, false, false, err
	}
	unchanged := !force && existing != nil &&
		existing.FileSize == size &&
		existing.FileModified == mtime &&
		existing.DurationSec != 0 && // never skip probing a never-probed file
		existing.FFProbeVersion == sc.probeVersion() // stale cache -> re-probe
	sc.Logger.Debug("incremental check", "dir", relDir, "unchanged", unchanged,
		"size", size, "mtime", mtime, "prev_size", prevSize(existing), "prev_mtime", prevMtime(existing))

	mf := buildMovieFile(lib, relDir, mainFile, kind, size, mtime, meta, dirName, nfoPath)
	if existing != nil {
		mf.ID = existing.ID
		mf.MovieID = existing.MovieID
		// carry over ffprobe-derived fields when skipping
		if unchanged {
			mf.DurationSec = existing.DurationSec
			mf.VideoBitrate = existing.VideoBitrate
			mf.FrameRate = existing.FrameRate
			mf.Width = existing.Width
			mf.Height = existing.Height
			mf.Container = existing.Container
			mf.FFProbeJSON = existing.FFProbeJSON
			mf.FFProbeVersion = existing.FFProbeVersion
			mf.FFProbeAt = existing.FFProbeAt
		}
	}

	// resolve / upsert the logical movie; enrich with TMDB when (re)scanned
	movieID, err := sc.upsertMovieFromRelease(ctx, mf, meta, nfoPath, unchanged)
	if err != nil {
		sc.Logger.Warn("upsert movie", "dir", absDir, "err", err)
	}
	mf.MovieID = movieID

	// ffprobe the main file (skip when unchanged)
	var audioTracks []model.AudioTrack
	var subtitles []model.Subtitle
	if !unchanged && kind == kindFile {
		sc.Logger.Debug("ffprobe start", "file", mainFile)
		if pr, raw, err := sc.FFProbe(ctx, mainFile); err == nil {
			applyProbe(mf, pr)
			audioTracks = probeAudioTracks(pr)
			subtitles = probeSubtitleStreams(pr)
			mf.FFProbeJSON = string(raw)
			mf.FFProbeVersion = sc.probeVersion()
			mf.FFProbeAt = time.Now().Unix()
			sc.Logger.Debug("ffprobe done", "file", mainFile,
				"duration", mf.DurationSec, "audio", len(audioTracks), "subs", len(subtitles))
		} else {
			sc.Logger.Warn("ffprobe", "file", mainFile, "err", err)
		}
	}
	subtitles = append(subtitles, externalSubs...)
	sortStreams(audioTracks, subtitles)

	// also compute subtitle aggregation for the movie_file row
	if len(subtitles) > 0 {
		mf.SubtitleLanguages = subtitleLanguages(subtitles)
		for _, s := range subtitles {
			if !s.IsEmbedded {
				mf.HasExternalSubtitle = true
				break
			}
		}
	}

	updated := existing != nil && !unchanged
	if err := sc.Store.UpsertMovieFile(ctx, mf); err != nil {
		return false, false, false, fmt.Errorf("upsert movie_file: %w", err)
	}
	if !unchanged {
		if err := sc.Store.ReplaceAudioTracks(ctx, mf.ID, audioTracks); err != nil {
			sc.Logger.Warn("replace audio", "err", err)
		}
		if err := sc.Store.ReplaceSubtitles(ctx, mf.ID, subtitles); err != nil {
			sc.Logger.Warn("replace subs", "err", err)
		}
	}
	outcome := "added"
	switch {
	case unchanged:
		outcome = "unchanged"
	case updated:
		outcome = "updated"
	}
	sc.Logger.Debug("release done", "dir", relDir, "outcome", outcome, "movie_id", mf.MovieID)
	switch {
	case unchanged:
		return false, false, true, nil
	case updated:
		return false, true, false, nil
	default:
		return true, false, false, nil
	}
}

// buildMovieFile fills the parsed fields of a MovieFile from FileMeta.
func buildMovieFile(lib *model.Library, relDir, mainFile string, kind releaseKind,
	size, mtime int64, meta parser.FileMeta, dirName, nfoPath string) *model.MovieFile {
	mf := &model.MovieFile{
		LibraryID: lib.ID, DirPath: relDir, FileSize: size, FileModified: mtime,
		RawName: dirName, NFOPath: nfoPath,
	}
	if kind == kindFile {
		mf.FileName = filepath.Base(mainFile)
	} else {
		mf.IsDisc = true
	}
	mf.ReleaseGroup = string(meta.ReleaseGroup)
	mf.Edition = meta.Edition
	mf.Source = string(meta.Source)
	mf.Resolution = string(meta.Resolution)
	mf.VideoCodec = string(meta.VideoCodec)
	mf.AudioCodec = string(meta.AudioCodec)
	mf.AudioChannels = meta.AudioChannels
	mf.HDR = string(meta.HDR)
	mf.DolbyVision = meta.HDR == parser.DolbyVision
	mf.BitDepth = meta.BitDepth
	mf.AudioCount = meta.AudioCount
	mf.Language = meta.Language
	mf.IsCollection = meta.IsCollection
	return mf
}

// upsertMovieFromRelease creates or finds the logical movie for this release.
// Priority for the seed row: .nfo ids > (parsed title, year). When unchanged is
// false (added or updated), and an Enricher is wired up, the movie is then
// enriched with TMDB metadata - which overwrites the seed fields with
// authoritative values (poster, overview, genres, credits, ...). When unchanged
// is true the previous TMDB data is left in place.
func (sc *Scanner) upsertMovieFromRelease(ctx context.Context, mf *model.MovieFile, meta parser.FileMeta, nfoPath string, unchanged bool) (int64, error) {
	m := &model.Movie{Title: meta.Title, Year: meta.Year}
	if nfoPath != "" {
		if info, err := nfo.ParseFile(nfoPath); err == nil {
			if info.Title != "" {
				m.Title = info.Title
			}
			if info.Year != 0 {
				m.Year = info.Year
			}
			m.IMDBID = info.IMDBID
			m.TMDBID = info.TMDBID
			m.Country = info.Country
			if info.Runtime != 0 {
				m.Runtime = info.Runtime
			}
			if info.Plot != "" {
				m.Synopsis = info.Plot
			}
		}
	}
	if m.Title == "" {
		return 0, nil
	}
	m.SortTitle = sortTitle(m.Title)
	if err := sc.Store.UpsertMovie(ctx, m); err != nil {
		return 0, err
	}
	sc.Logger.Debug("upsert movie", "title", m.Title, "year", m.Year, "movie_id", m.ID)
	// Enrich only on add / update; the cached TMDB row persists otherwise.
	if !unchanged && sc.Enricher != nil && sc.Enricher.Enabled() {
		if refreshed, err := sc.Enricher.EnrichMovie(ctx, m); err != nil {
			sc.Logger.Warn("tmdb enrich", "movie", m.Title, "err", err)
		} else {
			sc.Logger.Debug("tmdb enrich done", "movie", m.Title, "movie_id", m.ID, "refreshed", refreshed)
		}
	}
	return m.ID, nil
}

// applyProbe fills ffprobe-derived container fields on mf.
func applyProbe(mf *model.MovieFile, pr *ffprobe.Result) {
	mf.DurationSec = pr.Format.DurationSeconds()
	mf.VideoBitrate = pr.Format.BitRateInt()
	mf.Container = pr.Format.Container()
	if v := pr.FirstVideo(); v != nil {
		mf.Width = v.Width
		mf.Height = v.Height
		mf.FrameRate = ffprobe.ParseFraction(v.AvgFrameRate)
		if v.HasDolbyVision() && !mf.DolbyVision {
			mf.DolbyVision = true
			if mf.HDR == "" {
				mf.HDR = "DV"
			}
		}
		if v.HasHDR10Plus() {
			mf.HDR = "HDR10+"
		}
	}
}

func probeAudioTracks(pr *ffprobe.Result) []model.AudioTrack {
	streams := pr.AudioStreams()
	out := make([]model.AudioTrack, 0, len(streams))
	for i, s := range streams {
		out = append(out, model.AudioTrack{
			Language:  tagLang(s.Tags),
			Codec:     s.CodecName,
			Channels:  s.Channels,
			Title:     s.Tags["title"],
			IsDefault: s.Disposition.Default != 0,
			Order:     i,
		})
	}
	return out
}

func probeSubtitleStreams(pr *ffprobe.Result) []model.Subtitle {
	streams := pr.SubtitleStreams()
	out := make([]model.Subtitle, 0, len(streams))
	for i, s := range streams {
		out = append(out, model.Subtitle{
			Language:   tagLang(s.Tags),
			Format:     s.CodecName,
			IsEmbedded: true,
			IsDefault:  s.Disposition.Default != 0,
			Order:      i,
		})
	}
	return out
}

// scanSideFiles finds the .nfo (preferring one matching the dir name) and any
// external subtitle files in dir, returning the resolved subtitles.
func scanSideFiles(dir string) (nfoPath string, subs []model.Subtitle) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil
	}
	dirName := filepath.Base(dir)
	// order so deterministic: by name
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	subOrder := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		base := strings.TrimSuffix(name, filepath.Ext(name))
		switch ext {
		case ".nfo":
			if nfoPath == "" || strings.HasPrefix(base, dirName) {
				nfoPath = filepath.Join(dir, name)
			}
		}
		if fmt, ok := subtitleExtensions[ext]; ok {
			var size int64
			if info, err := e.Info(); err == nil {
				size = info.Size()
			}
			subs = append(subs, model.Subtitle{
				FilePath: filepath.Join(dir, name),
				Language: langFromName(base),
				Format:   fmt,
				Order:    subOrder,
				FileSize: size,
			})
			subOrder++
		}
	}
	return nfoPath, subs
}

// langFromName extracts a 2-3 letter language tag from a subtitle filename
// like "Movie.chi.srt" or "Movie.2.chi.ass".
func langFromName(base string) string {
	// strip the main release stem: tokens after the last '.' that are letters
	parts := strings.Split(base, ".")
	for i := len(parts) - 1; i >= 0; i-- {
		p := strings.ToLower(parts[i])
		if len(p) >= 2 && len(p) <= 3 && isAlpha(p) {
			return p
		}
		if len(p) == 2 && p == "zh" {
			return "chi"
		}
		// stop at the first non-language token (release name part)
		if !isAlpha(p) {
			break
		}
	}
	return ""
}

func isAlpha(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return len(s) > 0
}

// tagLang pulls a language code from stream tags, preferring "language" then
// "LANGUAGE".
func tagLang(tags ffprobe.Tags) string {
	for _, k := range []string{"language", "LANGUAGE", "lang"} {
		if v := tags[k]; v != "" {
			return strings.ToLower(v)
		}
	}
	return ""
}

// sortTitle lowercases the title and strips leading articles for sorting.
func sortTitle(title string) string {
	t := strings.ToLower(strings.TrimSpace(title))
	for _, art := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(t, art) {
			return t[len(art):]
		}
	}
	return t
}

// discSize sums the size of all .m2ts files under <dir>/BDMV/STREAM.
func discSize(dir string) int64 {
	streamDir := filepath.Join(dir, "BDMV", "STREAM")
	var total int64
	_ = filepath.WalkDir(streamDir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), ".m2ts") {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// subtitleLanguages returns a deterministic, comma-separated list of unique
// subtitle language tags for a movie_file. Empty string when there are none.
func subtitleLanguages(subs []model.Subtitle) string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(subs))
	for _, s := range subs {
		lang := strings.TrimSpace(s.Language)
		if lang == "" || seen[lang] {
			continue
		}
		seen[lang] = true
		out = append(out, lang)
	}
	return strings.Join(out, ",")
}

// sortStreams orders audio tracks and subtitles by their Order field.
func sortStreams(audio []model.AudioTrack, subs []model.Subtitle) {
	sort.Slice(audio, func(i, j int) bool { return audio[i].Order < audio[j].Order })
	sort.Slice(subs, func(i, j int) bool { return subs[i].Order < subs[j].Order })
}
