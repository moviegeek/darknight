package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/moviegeek/darknight/internal/matcher"
	"github.com/moviegeek/darknight/internal/model"
	"github.com/moviegeek/darknight/internal/parser"
	"github.com/moviegeek/darknight/internal/rename"
	"github.com/moviegeek/darknight/internal/store"
	subtitlepkg "github.com/moviegeek/darknight/internal/subtitle"
)

// ---------- manual matching endpoints ----------
//
// The matcher auto-accepts high-confidence candidates, but borderline rows
// land in match_status='pending' with their candidate list stored on the row.
// These endpoints are the human loop: list what needs attention, search
// candidates live, confirm a pick (manual - never auto-overwritten), detach a
// wrong match, and batch-retry everything unmatched.

// listPendingMatches returns movies awaiting review: pending rows with their
// stored candidates, plus unmatched rows with their failure reason.
func (a *API) listPendingMatches(w http.ResponseWriter, r *http.Request) {
	statuses := r.URL.Query().Get("status") // "", "pending", "unmatched", "both"
	if statuses == "" {
		statuses = "both"
	}
	var movies []model.Movie
	var err error
	switch statuses {
	case "pending":
		movies, err = a.Store.ListMoviesForRematch(r.Context(), 0)
	default:
		movies, err = a.Store.ListMoviesForRematch(r.Context(), 0)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type pendingMovie struct {
		Movie      model.Movie         `json:"movie"`
		Candidates []matcher.Candidate `json:"candidates"`
	}
	out := make([]pendingMovie, 0, len(movies))
	for i := range movies {
		pm := pendingMovie{Movie: movies[i]}
		if movies[i].MatchCandidates != "" {
			var cands []matcher.Candidate
			if err := json.Unmarshal([]byte(movies[i].MatchCandidates), &cands); err == nil {
				pm.Candidates = cands
			}
		}
		out = append(out, pm)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"movies": out})
}

// movieCandidates runs a live TMDB search for manual review: q (required) is
// any title, or movie_id to use the movie's own parsed title. Results are
// scored against the movie so the likely match sorts first.
func (a *API) movieCandidates(w http.ResponseWriter, r *http.Request) {
	if a.Matcher == nil {
		writeError(w, http.StatusServiceUnavailable, "matcher not configured (no TMDB API key)")
		return
	}
	q := r.URL.Query().Get("q")
	movieID, _ := strconv.ParseInt(r.URL.Query().Get("movie_id"), 10, 64)
	year, _ := strconv.Atoi(r.URL.Query().Get("year"))

	title := q
	if title == "" && movieID > 0 {
		m, err := a.Store.GetMovie(r.Context(), movieID)
		if err != nil {
			writeError(w, http.StatusNotFound, "movie not found")
			return
		}
		title = m.Title
		if year == 0 {
			year = m.Year
		}
	}
	if title == "" {
		writeError(w, http.StatusBadRequest, "missing q")
		return
	}

	res, err := a.Matcher.Search(r.Context(), title, year)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"candidates": res.Candidates})
}

// matchMovie confirms a manual pick: sets tmdb_id, marks the row 'manual'
// (exempt from automatic re-matching) and enriches it.
func (a *API) matchMovie(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		TMDBID int64 `json:"tmdb_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TMDBID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid tmdb_id")
		return
	}
	m, err := a.Store.GetMovie(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "movie not found")
		return
	}

	// merge any duplicate logical row that already holds this tmdb_id
	if existing, err := a.Store.FindMovieByTMDB(r.Context(), body.TMDBID); err == nil && existing.ID != m.ID {
		if n, err := a.Store.AttachMovieFiles(r.Context(), m.ID, existing.ID); err == nil {
			a.Logger.Info("manual match merged rows", "from", m.ID, "to", existing.ID, "files", n)
			if err := a.Store.SetMovieMatch(r.Context(), existing.ID, body.TMDBID, model.MatchStatusManual, 100, ""); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if a.Enricher != nil && a.Enricher.Enabled() {
				if _, err := a.Enricher.EnrichMovie(r.Context(), existing); err != nil {
					a.Logger.Warn("enrich after manual match", "err", err)
				}
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{"merged_into": existing.ID, "files_moved": n})
			return
		}
	}

	m.TMDBID = body.TMDBID
	if a.Enricher != nil && a.Enricher.Enabled() {
		if _, err := a.Enricher.EnrichMovie(r.Context(), m); err != nil {
			a.Logger.Warn("enrich after manual match", "err", err)
		}
	}
	if err := a.Store.SetMovieMatch(r.Context(), m.ID, body.TMDBID, model.MatchStatusManual, 100, ""); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.Store.SetMatchCandidates(r.Context(), m.ID, nil)

	// offer an on-disk rename preview: the release name still carries the
	// wrong title. POST /movies/:id/rename executes it after user review.
	previews := a.renamePreviews(r.Context(), m.ID)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"movie_id": m.ID, "tmdb_id": body.TMDBID, "status": model.MatchStatusManual,
		"rename_preview": previews,
	})
}

// renamePreviews computes the rename plan for every file release of a movie
// (discs are skipped - BDMV structures are named by their release dir only).
// Returns one entry per release: the old/new dir names and the file moves.
func (a *API) renamePreviews(ctx context.Context, movieID int64) []renamePreview {
	files, err := a.Store.ListMovieFiles(ctx, movieID)
	if err != nil {
		a.Logger.Warn("rename preview: list files", "movie_id", movieID, "err", err)
		return nil
	}
	m, err := a.Store.GetMovie(ctx, movieID)
	if err != nil {
		return nil
	}
	roots, err := a.Store.ListLibraries(ctx)
	if err != nil {
		return nil
	}
	rootOf := make(map[int64]string, len(roots))
	for _, l := range roots {
		rootOf[l.ID] = l.RootPath
	}
	var out []renamePreview
	for i := range files {
		f := files[i]
		if f.IsDisc {
			continue
		}
		root := rootOf[f.LibraryID]
		if root == "" {
			continue
		}
		dirAbs := filepath.Join(root, f.DirPath)
		siblings := listDirFiles(dirAbs)
		if len(siblings) == 0 {
			continue
		}
		plan := rename.Build(dirAbs, f.FileName, siblings, matchTitleForRename(m), m.Year)
		if plan.DirNew == plan.DirOld && len(plan.Moves) == 0 {
			continue // already canonical
		}
		out = append(out, renamePreview{MovieFileID: f.ID, Plan: plan, DirAbsOld: dirAbs})
	}
	return out
}

// matchTitleForRename picks the title segment for the new release name: the
// English/localized title when there is one (the library's convention is
// romanized names), else the display title.
func matchTitleForRename(m *model.Movie) string {
	if m.TitleEn != "" {
		return m.TitleEn
	}
	return m.Title
}

// listDirFiles returns the regular-file basenames of dir (hidden files
// skipped), or nil when the dir is unreadable.
func listDirFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, e.Name())
	}
	return out
}

// renamePreview is the per-release rename plan handed to the UI.
type renamePreview struct {
	MovieFileID int64       `json:"movie_file_id"`
	DirAbsOld   string      `json:"-"` // server-side only
	Plan        rename.Plan `json:"plan"`
}

// renameMovieRelease executes (or previews, dry_run=1) the on-disk rename for
// one movie_file, then syncs the DB paths. The plan is recomputed server-side
// - the client only sends the movie_file id, never paths.
func (a *API) renameMovieRelease(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	fid, err := strconv.ParseInt(chi.URLParam(r, "fid"), 10, 64)
	if err != nil || fid <= 0 {
		writeError(w, http.StatusBadRequest, "missing file id")
		return
	}
	dryRun := queryBool(r, "dry_run")

	m, err := a.Store.GetMovie(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "movie not found")
		return
	}
	files, err := a.Store.ListMovieFiles(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var mf *model.MovieFile
	for i := range files {
		if files[i].ID == fid {
			mf = &files[i]
			break
		}
	}
	if mf == nil {
		writeError(w, http.StatusNotFound, "file not on this movie")
		return
	}
	if mf.IsDisc {
		writeError(w, http.StatusBadRequest, "disc releases are not renamed")
		return
	}
	libs, err := a.Store.ListLibraries(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	root := ""
	for _, l := range libs {
		if l.ID == mf.LibraryID {
			root = l.RootPath
		}
	}
	if root == "" {
		writeError(w, http.StatusInternalServerError, "library root not found")
		return
	}

	dirAbs := filepath.Join(root, mf.DirPath)
	siblings := listDirFiles(dirAbs)
	if siblings == nil {
		writeError(w, http.StatusConflict, "release dir not readable: "+dirAbs)
		return
	}
	plan := rename.Build(dirAbs, mf.FileName, siblings, matchTitleForRename(m), m.Year)
	if plan.DirNew == plan.DirOld && len(plan.Moves) == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{"changed": false, "plan": plan})
		return
	}
	if dryRun {
		writeJSON(w, http.StatusOK, map[string]interface{}{"changed": true, "dry_run": true, "plan": plan})
		return
	}

	if err := rename.Apply(plan, dirAbs); err != nil {
		a.Logger.Warn("apply rename", "dir", dirAbs, "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// sync DB: new rel dir, new file name, relocated nfo path. Both the nfo
	// and the subtitle paths are absolute and sit inside the release dir, so
	// they need the NEW directory as well as the new basename.
	oldDirRel := mf.DirPath
	newDirRel := filepath.Join(filepath.Dir(oldDirRel), plan.DirNew)
	absDirNew := filepath.Join(filepath.Dir(dirAbs), plan.DirNew)
	newMain := renamedMain(plan, mf.FileName)
	newNFO := ""
	if mf.NFOPath != "" {
		base := filepath.Base(mf.NFOPath)
		ext := filepath.Ext(base)
		stem := strings.TrimSuffix(base, ext)
		newStem := rename.TitleYear(stem, matchTitleForRename(m), m.Year)
		newNFO = filepath.Join(absDirNew, newStem+ext)
	}
	if err := a.Store.RenameMovieFileRelease(r.Context(), mf.ID, newDirRel, newMain, newNFO, absDirNew); err != nil {
		a.Logger.Warn("sync db after rename", "movie_file", mf.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "db sync failed (disk renamed): "+err.Error())
		return
	}
	a.Logger.Info("release renamed", "movie_id", m.ID, "file_id", mf.ID,
		"dir_old", plan.DirOld, "dir_new", plan.DirNew, "files", len(plan.Moves))
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"changed": true, "dry_run": false, "plan": plan,
		"dir_old": plan.DirOld, "dir_new": plan.DirNew,
	})
}

// renamedMain finds the video move for mainFile and returns its new base
// name, or the unchanged name when the plan has no move for it.
func renamedMain(plan rename.Plan, mainFile string) string {
	for _, mv := range plan.Moves {
		if mv.Kind == "video" && filepath.Base(mv.From) == mainFile {
			return filepath.Base(mv.To)
		}
	}
	return mainFile
}

// unmatchMovie clears the tmdb_id and returns the row to unmatched, detaching
// its files' movie_id so a rescan re-seeds them.
func (a *API) unmatchMovie(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.Store.ClearMovieMatch(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "movie not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"movie_id": id, "status": model.MatchStatusUnmatched})
}

// rematchAll re-runs the scored matcher over every pending/unmatched movie
// (skipping manual). dry_run=1 reports the decisions without writing.
func (a *API) rematchAll(w http.ResponseWriter, r *http.Request) {
	if a.Matcher == nil {
		writeError(w, http.StatusServiceUnavailable, "matcher not configured (no TMDB API key)")
		return
	}
	dryRun := queryBool(r, "dry_run")
	movies, err := a.Store.ListMoviesForRematch(r.Context(), 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type row struct {
		MovieID  int64   `json:"movie_id"`
		Title    string  `json:"title"`
		Year     int     `json:"year"`
		Decision string  `json:"decision"`
		Best     string  `json:"best,omitempty"`
		TMDBID   int64   `json:"tmdb_id,omitempty"`
		Score    float64 `json:"score"`
		Reason   string  `json:"reason,omitempty"`
	}
	rows := make([]row, 0, len(movies))
	for i := range movies {
		m := &movies[i]
		res, err := a.Matcher.Match(r.Context(), searchVariantsOf(m), m.Year)
		if err != nil {
			rows = append(rows, row{MovieID: m.ID, Title: m.Title, Year: m.Year,
				Decision: "error", Reason: err.Error()})
			continue
		}
		rr := row{MovieID: m.ID, Title: m.Title, Year: m.Year, Score: bestScore(res)}
		switch res.Decision {
		case matcher.DecisionAccept:
			rr.Decision = "accept"
			rr.Best = res.Best.Title
			rr.TMDBID = res.Best.TMDBID
			if !dryRun && a.Enricher != nil {
				mm := *m
				if _, merged, err := a.Enricher.ApplyMatch(r.Context(), &mm, res.Best.TMDBID, int(res.Best.Score)); err != nil {
					a.Logger.Warn("rematch apply", "movie_id", m.ID, "err", err)
					rr.Reason = err.Error()
				} else if merged {
					rr.Decision = "merged"
				}
			}
		case matcher.DecisionPending:
			rr.Decision = "pending"
			rr.Best = res.Best.Title
			rr.TMDBID = res.Best.TMDBID
			if !dryRun {
				a.Store.SetMovieMatch(r.Context(), m.ID, 0, model.MatchStatusPending, int(res.Best.Score), res.Reason)
				a.Store.SetMatchCandidates(r.Context(), m.ID, res.Candidates)
			}
		default:
			rr.Decision = "unmatched"
			rr.Reason = res.Reason
			if !dryRun {
				a.Store.SetMovieMatch(r.Context(), m.ID, 0, model.MatchStatusUnmatched, 0, res.Reason)
			}
		}
		rows = append(rows, rr)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"dry_run": dryRun, "count": len(rows), "results": rows})
}

func bestScore(res *matcher.Result) float64 {
	if res.Best == nil {
		return 0
	}
	return res.Best.Score
}

// searchVariantsOf derives search variants for a stored movie row (its title
// may already be the enriched original title, which searches fine as-is).
func searchVariantsOf(m *model.Movie) []string {
	return parser.SearchVariants(m.Title)
}

// ---------- subtitle upload ----------

// uploadSubtitles receives a batch of subtitle files for one movie release.
// Multipart: files[] (srt/ass/ssa only) plus langs[] - one ISO 639-2 code or
// display name per file, in order. Files are written next to the video as
// <movie stem>.[verN.]<lang>.<ext> and registered in the subtitles table.
func (a *API) uploadSubtitles(w http.ResponseWriter, r *http.Request) {
	movieID, err := parseInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	fid, err := strconv.ParseInt(chi.URLParam(r, "fid"), 10, 64)
	if err != nil || fid <= 0 {
		writeError(w, http.StatusBadRequest, "missing file id")
		return
	}

	// resolve the movie_file + library root
	m, err := a.Store.GetMovie(r.Context(), movieID)
	if err != nil {
		writeError(w, http.StatusNotFound, "movie not found")
		return
	}
	files, err := a.Store.ListMovieFiles(r.Context(), movieID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var mf *model.MovieFile
	for i := range files {
		if files[i].ID == fid {
			mf = &files[i]
			break
		}
	}
	if mf == nil {
		writeError(w, http.StatusNotFound, "file not on this movie")
		return
	}
	if mf.IsDisc {
		writeError(w, http.StatusBadRequest, "disc releases cannot take uploaded subtitles")
		return
	}
	libs, err := a.Store.ListLibraries(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	root := ""
	for _, l := range libs {
		if l.ID == mf.LibraryID {
			root = l.RootPath
		}
	}
	if root == "" {
		writeError(w, http.StatusInternalServerError, "library root not found")
		return
	}
	dirAbs := filepath.Join(root, mf.DirPath)

	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "parse multipart: "+err.Error())
		return
	}
	uploads := r.MultipartForm.File["files"]
	if len(uploads) == 0 {
		writeError(w, http.StatusBadRequest, "no files uploaded")
		return
	}
	langs := r.MultipartForm.Value["langs"]

	// validate extensions up front so nothing is written on a bad batch
	allowed := map[string]bool{".srt": true, ".ass": true, ".ssa": true}
	uploadList := make([]subtitlepkg.Upload, 0, len(uploads))
	for i, fh := range uploads {
		ext := strings.ToLower(filepath.Ext(fh.Filename))
		if !allowed[ext] {
			writeError(w, http.StatusBadRequest, "unsupported subtitle format: "+fh.Filename)
			return
		}
		lang := "chi"
		if i < len(langs) && langs[i] != "" {
			lang = subtitlepkg.LangCode(langs[i])
		}
		uploadList = append(uploadList, subtitlepkg.Upload{Filename: fh.Filename, Lang: lang})
	}

	// existing filenames in the release dir (for version allocation)
	existingFilenames := []string{}
	entries, err := os.ReadDir(dirAbs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "release dir not readable: "+err.Error())
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			existingFilenames = append(existingFilenames, e.Name())
		}
	}

	alloc := subtitlepkg.Allocate(mf.FileName, uploadList, existingFilenames)

	// pre-existing subtitles that must vacate the unversioned slot go first,
	// on disk and in the subtitles table (the aggregate is unchanged - same
	// language, same format, only the path moves)
	for _, rn := range alloc.Renames {
		if err := os.Rename(filepath.Join(dirAbs, rn.From), filepath.Join(dirAbs, rn.To)); err != nil {
			writeError(w, http.StatusInternalServerError, "make room for upload ("+rn.From+"): "+err.Error())
			return
		}
		if err := a.Store.UpdateSubtitlePath(r.Context(), mf.ID,
			filepath.Join(dirAbs, rn.From), filepath.Join(dirAbs, rn.To)); err != nil {
			a.Logger.Warn("repoint renamed subtitle", "from", rn.From, "err", err)
		}
		a.Logger.Info("existing subtitle vacated unversioned slot",
			"movie_file", mf.ID, "from", rn.From, "to", rn.To)
	}

	plans := alloc.Plans
	type resultRow struct {
		OriginalName string `json:"original_name"`
		FinalName    string `json:"final_name"`
		Lang         string `json:"lang"`
		Size         int64  `json:"size"`
	}
	results := make([]resultRow, 0, len(plans))
	for i, plan := range plans {
		src, err := uploads[i].Open()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "open upload: "+err.Error())
			return
		}
		dst, err := os.OpenFile(filepath.Join(dirAbs, plan.FinalName), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
		if err != nil {
			src.Close()
			writeError(w, http.StatusInternalServerError, "write subtitle: "+err.Error())
			return
		}
		size, copyErr := io.Copy(dst, src)
		src.Close()
		dst.Close()
		if copyErr != nil {
			_ = os.Remove(filepath.Join(dirAbs, plan.FinalName))
			writeError(w, http.StatusInternalServerError, "save subtitle: "+copyErr.Error())
			return
		}
		sub := model.Subtitle{
			FilePath:   filepath.Join(dirAbs, plan.FinalName),
			Language:   plan.Lang,
			Format:     strings.TrimPrefix(plan.Ext, "."),
			IsEmbedded: false,
			FileSize:   size,
		}
		if err := a.Store.AddSubtitle(r.Context(), mf.ID, sub); err != nil {
			writeError(w, http.StatusInternalServerError, "register subtitle: "+err.Error())
			return
		}
		results = append(results, resultRow{
			OriginalName: plan.OriginalName,
			FinalName:    plan.FinalName,
			Lang:         plan.Lang,
			Size:         size,
		})
	}
	type renameRow struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	renamed := make([]renameRow, 0, len(alloc.Renames))
	for _, rn := range alloc.Renames {
		renamed = append(renamed, renameRow{From: rn.From, To: rn.To})
	}
	a.Logger.Info("subtitles uploaded", "movie_id", m.ID, "movie_file", mf.ID,
		"count", len(results), "existing_renamed", len(renamed))
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"uploaded": results, "renamed_existing": renamed,
	})
}
