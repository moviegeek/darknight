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
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/moviegeek/darknight/internal/ffprobe"
	"github.com/moviegeek/darknight/internal/matcher"
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
	// Matcher, when non-nil, runs scored TMDB candidate matching for movies
	// without an authoritative id (no NFO tmdb/imdb id). Leave nil to fall
	// back to the legacy enrich-only behaviour.
	Matcher *matcher.Matcher
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

	// A missing/unreadable root is a hard error: pruning on a failed walk
	// would wipe the library. With this guard, an empty `seen` after a
	// successful walk means the library is genuinely empty and pruning is
	// correct.
	if fi, err := os.Stat(lib.RootPath); err != nil || !fi.IsDir() {
		return stats, fmt.Errorf("library root not accessible: %s", lib.RootPath)
	}

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

		files, mainFile, kind, ok := classifyRelease(path)
		if !ok {
			return nil // descend further
		}
		sc.Logger.Debug("found release", "dir", rel, "kind", kindString(kind), "files", len(files), "main", mainFile)

		// File-grained releases: every non-noise video file in the dir is its
		// own movie_files row (multi-version and flat-collection packs), so
		// scanRelease reports the release keys it persisted plus per-FILE
		// outcome counts for the stats.
		keys, nAdded, nUpdated, nUnchanged, err := sc.scanRelease(ctx, lib, path, rel, files, mainFile, kind)
		seen = append(seen, keys...)
		stats.Added += nAdded
		stats.Updated += nUpdated
		stats.Unchanged += nUnchanged
		if err != nil {
			stats.Errors++
			sc.Logger.Warn("scan release", "dir", path, "err", err)
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

	// list the doomed rows before deleting so a "removed=N" stat is traceable
	// to concrete files (and the movies they belong to).
	if stale, err := sc.Store.ListStaleMovieFiles(ctx, lib.ID, seen); err != nil {
		sc.Logger.Warn("list stale files", "err", err)
	} else {
		for _, sf := range stale {
			sc.Logger.Info("prune stale file", "library", lib.Name,
				"dir", sf.DirPath, "file", sf.FileName, "movie", movieLabel(sf.Title, sf.Year))
		}
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

// movieLabel renders "Title (Year)" for scan log lines, tolerating a missing
// year or title.
func movieLabel(title string, year int) string {
	if title == "" {
		return "?"
	}
	if year > 0 {
		return fmt.Sprintf("%s (%d)", title, year)
	}
	return title
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

// skipFileRe matches video-file basenames that are bonus material, not the
// feature: samples, trailers, promos, menus, extras parts.
var skipFileRe = regexp.MustCompile(`(?i)(^|[.\-_])(sample|trailer|promo|menu|featurette|extras?\.\d+)([.\-_]|$)`)

// skipDirNames are directory names that hold bonus material only ("extras",
// "Behind The Scenes"); their files are not releases.
var skipDirNames = map[string]bool{
	"extras": true, "extra": true, "behind the scenes": true, "bonus": true,
}

// releaseKey is the movie_files uniqueness key: dir + file_name joined with a
// NUL byte (file_name is ” for disc releases).
func releaseKey(relDir, fileName string) string {
	return relDir + "\x00" + fileName
}

// classifyRelease inspects dir and returns its video files (largest first,
// noise files skipped), the release kind, and ok=false when dir is not yet a
// leaf (no video / BDMV). For file releases every listed file becomes its own
// movie_files row; mainFile keeps its "largest file" meaning for probe/title
// fallbacks.
func classifyRelease(dir string) (files []string, mainFile string, kind releaseKind, ok bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, "", 0, false
	}
	if skipDirNames[strings.ToLower(filepath.Base(dir))] {
		return nil, "", 0, false
	}
	// Blu-ray disc folder?
	for _, e := range entries {
		if e.IsDir() && strings.EqualFold(e.Name(), "BDMV") {
			return nil, "", kindDisc, true
		}
	}
	type sized struct {
		name string
		size int64
	}
	var vids []sized
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// macOS AppleDouble sidecars ("._Movie.mkv") look like videos but are
		// Finder metadata; skip them like hidden files.
		if strings.HasPrefix(name, "._") || strings.HasPrefix(name, ".") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if !videoExtensions[ext] {
			continue
		}
		if skipFileRe.MatchString(name) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		vids = append(vids, sized{name, info.Size()})
	}
	if len(vids) == 0 {
		return nil, "", 0, false
	}
	sort.Slice(vids, func(i, j int) bool {
		if vids[i].size != vids[j].size {
			return vids[i].size > vids[j].size
		}
		return vids[i].name < vids[j].name
	})
	for _, v := range vids {
		files = append(files, v.name)
	}
	return files, filepath.Join(dir, vids[0].name), kindFile, true
}

// scanRelease parses one release dir and upserts a movie_file for EVERY video
// file in it (multi-version releases and flat collection packs). Returns the
// persisted release keys (dir + "\x00" + file) plus per-FILE outcome counts;
// when a file is unchanged, its ffprobe and stream rewrite are skipped.
func (sc *Scanner) scanRelease(
	ctx context.Context,
	lib *model.Library,
	absDir, relDir string, files []string, mainFile string, kind releaseKind,
) (keys []string, added, updated, unchanged int, err error) {
	dirName := filepath.Base(absDir)
	dirMeta := parser.ParseTitle(dirName)

	// gather side files (.nfo, external subtitles)
	nfoPath, externalSubs := scanSideFiles(absDir)

	if kind == kindDisc {
		k, a, u, un, err := sc.scanDisc(ctx, lib, absDir, relDir, dirMeta, dirName, nfoPath, externalSubs)
		if k != "" {
			keys = append(keys, k)
		}
		return keys, boolToInt(a), boolToInt(u), boolToInt(un), err
	}

	for _, fname := range files {
		k, a, u, un, err := sc.scanOneFile(ctx, lib, absDir, relDir, fname, mainFile,
			dirMeta, dirName, nfoPath, externalSubs)
		keys = append(keys, k)
		if err != nil {
			sc.Logger.Warn("scan file", "dir", absDir, "file", fname, "err", err)
			continue
		}
		added += boolToInt(a)
		updated += boolToInt(u)
		unchanged += boolToInt(un)
	}
	return keys, added, updated, unchanged, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// scanOneFile handles one video file of a release dir: parse (file name first,
// dir name as fallback context), upsert movie + movie_file, ffprobe unless
// unchanged. externalSubs are shared by every file in the dir.
func (sc *Scanner) scanOneFile(
	ctx context.Context,
	lib *model.Library,
	absDir, relDir, fileName, mainFile string,
	dirMeta parser.FileMeta, dirName, nfoPath string,
	externalSubs []model.Subtitle,
) (string, bool, bool, bool, error) {
	absFile := filepath.Join(absDir, fileName)

	// Parse the file name (extension stripped - the trailing ".mkv" would
	// otherwise glue onto the release group and defeat the group peel); fall
	// back to the dir's parse for anything the file name doesn't carry (a
	// bare "00001.m2ts" style name has no title).
	stem := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	meta := parser.ParseTitle(stem)
	if meta.Title == "" {
		meta.Title = dirMeta.Title
		meta.Year = dirMeta.Year
	}
	rawName := stem
	if meta.Title == "" {
		meta = dirMeta
		rawName = dirName
	}

	fi, err := os.Stat(absFile)
	if err != nil {
		return releaseKey(relDir, fileName), false, false, false, fmt.Errorf("stat main file: %w", err)
	}
	size, mtime := fi.Size(), fi.ModTime().Unix()

	existing, err := sc.Store.FindMovieFileByRelease(ctx, lib.ID, relDir, fileName)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return releaseKey(relDir, fileName), false, false, false, err
	}
	unchanged := existing != nil &&
		existing.FileSize == size &&
		existing.FileModified == mtime &&
		existing.DurationSec != 0 && // never skip probing a never-probed file
		existing.FFProbeVersion == sc.probeVersion() // stale cache -> re-probe
	sc.Logger.Debug("incremental check", "dir", relDir, "file", fileName, "unchanged", unchanged,
		"size", size, "mtime", mtime, "prev_size", prevSize(existing), "prev_mtime", prevMtime(existing))

	mf := buildMovieFile(lib, relDir, fileName, kindFile, size, mtime, meta, rawName, nfoPath)
	if existing != nil {
		mf.ID = existing.ID
		mf.MovieID = existing.MovieID
		// carry over derived fields when skipping. This includes the subtitle
		// aggregate: the subtitles table is only rebuilt when the file changed,
		// so forgetting to carry it over wipes subtitle_languages on every
		// incremental scan and breaks the "no Chinese subtitle" filter (the
		// detail rows survive, the aggregate silently empties).
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
			mf.SubtitleLanguages = existing.SubtitleLanguages
			mf.HasExternalSubtitle = existing.HasExternalSubtitle
		}
	}

	// resolve / upsert the logical movie; enrich with TMDB when (re)scanned
	movieID, err := sc.upsertMovieFromRelease(ctx, mf, meta, nfoPath, unchanged)
	if err != nil {
		sc.Logger.Warn("upsert movie", "dir", absDir, "file", fileName, "err", err)
	}
	mf.MovieID = movieID

	// ffprobe the file (skip when unchanged)
	var audioTracks []model.AudioTrack
	var subtitles []model.Subtitle
	if !unchanged {
		sc.Logger.Debug("ffprobe start", "file", absFile)
		if pr, raw, err := sc.FFProbe(ctx, absFile); err == nil {
			applyProbe(mf, pr)
			audioTracks = probeAudioTracks(pr)
			subtitles = probeSubtitleStreams(pr)
			mf.FFProbeJSON = string(raw)
			mf.FFProbeVersion = sc.probeVersion()
			mf.FFProbeAt = time.Now().Unix()
			sc.Logger.Debug("ffprobe done", "file", absFile,
				"duration", mf.DurationSec, "audio", len(audioTracks), "subs", len(subtitles))
		} else {
			sc.Logger.Warn("ffprobe", "file", absFile, "err", err)
		}
	}
	subtitles = append(subtitles, externalSubs...)
	sortStreams(audioTracks, subtitles)

	if len(subtitles) > 0 {
		mf.SubtitleLanguages = subtitleLanguages(subtitles)
		for _, s := range subtitles {
			if !s.IsEmbedded {
				mf.HasExternalSubtitle = true
				break
			}
		}
	}

	updatedFlag := existing != nil && !unchanged
	if err := sc.Store.UpsertMovieFile(ctx, mf); err != nil {
		return releaseKey(relDir, fileName), false, false, false, fmt.Errorf("upsert movie_file: %w", err)
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
	case updatedFlag:
		outcome = "updated"
	}
	// added / updated are the interesting minority of an incremental scan:
	// surface them at info so a summary stat like "added=1" can be traced to
	// concrete files without wading through the debug trace.
	if outcome != "unchanged" {
		sc.Logger.Info("file "+outcome, "dir", relDir, "file", fileName,
			"movie", movieLabel(meta.Title, meta.Year))
	}
	sc.Logger.Debug("file done", "dir", relDir, "file", fileName, "outcome", outcome, "movie_id", mf.MovieID)
	return releaseKey(relDir, fileName), existing == nil, updatedFlag, unchanged, nil
}

// scanDisc handles a BDMV folder release: one movie_files row with
// file_name=” and size summed over BDMV/STREAM.
func (sc *Scanner) scanDisc(
	ctx context.Context,
	lib *model.Library,
	absDir, relDir string,
	dirMeta parser.FileMeta, dirName, nfoPath string,
	externalSubs []model.Subtitle,
) (string, bool, bool, bool, error) {
	size := discSize(absDir)
	var mtime int64
	if fi, _ := os.Stat(absDir); fi != nil {
		mtime = fi.ModTime().Unix()
	}

	existing, err := sc.Store.FindMovieFileByRelease(ctx, lib.ID, relDir, "")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return releaseKey(relDir, ""), false, false, false, err
	}
	unchanged := existing != nil &&
		existing.FileSize == size &&
		existing.FileModified == mtime

	mf := buildMovieFile(lib, relDir, "", kindDisc, size, mtime, dirMeta, dirName, nfoPath)
	if existing != nil {
		mf.ID = existing.ID
		mf.MovieID = existing.MovieID
		if unchanged {
			// subtitles are only rebuilt when the disc changed; keep the
			// stored aggregate (same rationale as the file path above)
			mf.SubtitleLanguages = existing.SubtitleLanguages
			mf.HasExternalSubtitle = existing.HasExternalSubtitle
		}
	}

	movieID, err := sc.upsertMovieFromRelease(ctx, mf, dirMeta, nfoPath, unchanged)
	if err != nil {
		sc.Logger.Warn("upsert movie", "dir", absDir, "err", err)
	}
	mf.MovieID = movieID

	subtitles := append([]model.Subtitle(nil), externalSubs...)
	sortStreams(nil, subtitles)
	if len(subtitles) > 0 {
		mf.SubtitleLanguages = subtitleLanguages(subtitles)
		for _, s := range subtitles {
			if !s.IsEmbedded {
				mf.HasExternalSubtitle = true
				break
			}
		}
	}
	if err := sc.Store.UpsertMovieFile(ctx, mf); err != nil {
		return releaseKey(relDir, ""), false, false, false, fmt.Errorf("upsert movie_file: %w", err)
	}
	if !unchanged {
		if err := sc.Store.ReplaceSubtitles(ctx, mf.ID, subtitles); err != nil {
			sc.Logger.Warn("replace subs", "err", err)
		}
	}
	// mirror the file path's added/updated info lines for disc releases
	switch {
	case existing == nil:
		sc.Logger.Info("disc added", "dir", relDir, "movie", movieLabel(dirMeta.Title, dirMeta.Year))
	case !unchanged:
		sc.Logger.Info("disc updated", "dir", relDir, "movie", movieLabel(dirMeta.Title, dirMeta.Year))
	}
	sc.Logger.Debug("disc done", "dir", relDir, "movie_id", mf.MovieID)
	return releaseKey(relDir, ""), existing == nil, !unchanged && existing != nil, unchanged, nil
}

// buildMovieFile fills the parsed fields of a MovieFile from FileMeta.
// fileName is the release's own video file; mainFile arg was dropped in favour
// of the explicit fileName ("" for discs).
func buildMovieFile(lib *model.Library, relDir, fileName string, kind releaseKind,
	size, mtime int64, meta parser.FileMeta, rawName, nfoPath string) *model.MovieFile {
	mf := &model.MovieFile{
		LibraryID: lib.ID, DirPath: relDir, FileName: fileName,
		FileSize: size, FileModified: mtime,
		RawName: rawName, NFOPath: nfoPath,
	}
	if kind == kindDisc {
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
	if kind == kindDisc {
		// A BDMV folder is an original Blu-ray disc, not a rip - override
		// whatever source the filename parser inferred (usually BluRay) so
		// disc releases are distinguishable in filters and the UI.
		mf.Source = string(parser.BlurayDisk)
	}
	return mf
}

// upsertMovieFromRelease creates or finds the logical movie for this release.
//
// Matching pipeline (only when the file changed and TMDB is available):
//  1. NFO ids (tmdb/imdb) are authoritative - enrich directly.
//  2. Otherwise the matcher runs: cleaned SearchVariants -> scored TMDB
//     candidates -> auto-accept (>=85) enriches immediately; pending (60-84)
//     stores the best candidates on the row for manual review; below that the
//     row is left unmatched with the failure reason recorded.
//  3. A manual match_status ('manual') is never re-matched automatically.
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
	if err := sc.Store.UpsertMovieSeed(ctx, m); err != nil {
		return 0, err
	}
	sc.Logger.Debug("upsert movie", "title", m.Title, "year", m.Year, "movie_id", m.ID)

	// Whether to run matching is a property of the MOVIE ROW, not of the file's
	// bytes. `unchanged` only means "this file did not change, skip ffprobe":
	// gating the matcher on it left rows stuck forever at tmdb_id=NULL with
	// match_attempts=0 (observed: 169 rows after files were re-seeded onto new
	// movie rows). A row that already has a tmdb_id, or that a human confirmed,
	// needs nothing; anything else is (re)matched even when the file is
	// untouched.
	if m.TMDBID != 0 || m.MatchStatus == model.MatchStatusManual {
		// Re-enrich only when the file actually changed; the cached TMDB row
		// stands otherwise.
		if !unchanged && sc.Enricher != nil && sc.Enricher.Enabled() &&
			m.TMDBID != 0 && m.MatchStatus != model.MatchStatusManual {
			if _, err := sc.Enricher.EnrichMovie(ctx, m); err != nil {
				sc.Logger.Warn("tmdb enrich", "movie", m.Title, "err", err)
			}
		}
		return m.ID, nil
	}

	// NFO imdb_id without tmdb_id: let the enricher's /find chain resolve it,
	// falling back to the matcher when it finds nothing.
	if m.IMDBID != "" && sc.Enricher != nil && sc.Enricher.Enabled() {
		if refreshed, err := sc.Enricher.EnrichMovie(ctx, m); err != nil {
			sc.Logger.Warn("tmdb enrich", "movie", m.Title, "err", err)
		} else if refreshed {
			if err := sc.Store.SetMovieMatch(ctx, m.ID, m.TMDBID, model.MatchStatusMatched, 100, ""); err != nil {
				sc.Logger.Warn("set match", "movie_id", m.ID, "err", err)
			}
			return m.ID, nil
		}
	}

	// scored matcher path
	if sc.Matcher != nil {
		variants := parser.SearchVariants(m.Title)
		res, err := sc.Matcher.Match(ctx, variants, m.Year)
		if err != nil {
			sc.Logger.Warn("match", "movie", m.Title, "err", err)
			_ = sc.Store.SetMovieMatch(ctx, m.ID, 0, model.MatchStatusUnmatched, 0, "matcher error: "+err.Error())
			return m.ID, nil
		}
		switch res.Decision {
		case matcher.DecisionAccept:
			// ApplyMatch handles the case where this tmdb_id already belongs to
			// another row (the enriched row this release was orphaned from):
			// the files move there and this seed shell is dropped.
			if sc.Enricher != nil {
				finalID, merged, err := sc.Enricher.ApplyMatch(ctx, m, res.Best.TMDBID, int(res.Best.Score))
				if err != nil {
					sc.Logger.Warn("apply match", "movie", m.Title, "err", err)
					return m.ID, nil
				}
				if merged {
					return finalID, nil
				}
				sc.Logger.Info("match accepted", "movie", m.Title,
					"tmdb_id", res.Best.TMDBID, "score", res.Best.Score)
				return finalID, nil
			}
			if err := sc.Store.SetMovieMatch(ctx, m.ID, res.Best.TMDBID, model.MatchStatusMatched, int(res.Best.Score), ""); err != nil {
				sc.Logger.Warn("set match", "movie_id", m.ID, "err", err)
			}
			sc.Logger.Info("match accepted", "movie", m.Title, "tmdb_id", res.Best.TMDBID, "score", res.Best.Score)
		case matcher.DecisionPending:
			if err := sc.Store.SetMovieMatch(ctx, m.ID, 0, model.MatchStatusPending, int(res.Best.Score), res.Reason); err != nil {
				sc.Logger.Warn("set match", "movie_id", m.ID, "err", err)
			}
			sc.Store.SetMatchCandidates(ctx, m.ID, res.Candidates)
			sc.Logger.Info("match pending", "movie", m.Title, "best", res.Best.Title, "score", res.Best.Score)
		default:
			if err := sc.Store.SetMovieMatch(ctx, m.ID, 0, model.MatchStatusUnmatched, 0, res.Reason); err != nil {
				sc.Logger.Warn("set match", "movie_id", m.ID, "err", err)
			}
			sc.Logger.Info("match failed", "movie", m.Title, "reason", res.Reason)
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
		if strings.HasPrefix(name, "._") || strings.HasPrefix(name, ".") {
			continue // hidden / macOS AppleDouble metadata
		}
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
