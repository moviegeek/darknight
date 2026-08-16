// Package matcher scores TMDB search candidates against a locally parsed
// release and decides between auto-accept, pending (needs human confirmation)
// and unmatched. It replaces the old "first search result wins" behaviour.
//
// Pipeline per movie:
//
//	SearchVariants(title)  (parser, cleaned for search)
//	  -> SearchMovieAll(query) for each variant until a confident hit
//	  -> Score(candidate, parsedTitle, parsedYear)
//	  -> threshold decision
package matcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/moviegeek/darknight/internal/tmdb"
)

// ErrNoAPIKey forwards the tmdb client's disabled state.
var ErrNoAPIKey = tmdb.ErrNoAPIKey

// Thresholds for the score decision.
const (
	// AutoAccept is the minimum score to accept a match without review.
	AutoAccept = 85
	// PendingMin is the minimum score to keep a candidate for manual review.
	PendingMin = 60

	// MaxCandidates caps how many TMDB results are considered per query.
	MaxCandidates = 10
	// MaxAPICalls budgets the number of search requests per movie (variants
	// are tried in order until one yields a confident-enough candidate).
	MaxAPICalls = 4
)

// Decision is the outcome of matching one movie.
type Decision int

const (
	DecisionNone    Decision = iota // no candidate reached PendingMin
	DecisionPending                 // best candidate in [PendingMin, AutoAccept)
	DecisionAccept                  // best candidate >= AutoAccept
)

// Candidate is one scored TMDB search result.
type Candidate struct {
	TMDBID   int64   `json:"tmdb_id"`
	Title    string  `json:"title"`
	Original string  `json:"original_title"`
	Year     int     `json:"year"`
	Poster   string  `json:"poster_path"`
	Overview string  `json:"overview"`
	Score    float64 `json:"score"`
	YearDiff int     `json:"year_diff"`
}

// Result is the full outcome for one movie.
type Result struct {
	Decision   Decision
	Best       *Candidate
	Candidates []Candidate // top candidates for manual review (pending)
	Tried      int         // API calls made
	Reason     string      // human-readable failure/decision note
}

// Searcher abstracts the TMDB search the matcher needs. *tmdb.Client satisfies it.
type Searcher interface {
	SearchMovieAll(ctx context.Context, query string, limit int) ([]tmdb.SearchResult, error)
}

// Matcher scores TMDB candidates for a release.
type Matcher struct {
	TMDB   Searcher
	Logger *slog.Logger
}

// New returns a Matcher. log may be nil.
func New(s Searcher, log *slog.Logger) *Matcher {
	if log == nil {
		log = slog.Default()
	}
	return &Matcher{TMDB: s, Logger: log}
}

// Match takes the cleaned search variants (from parser.SearchVariants) and the
// parsed year, queries TMDB until a confident candidate is found, and scores
// every candidate of the winning query.
func (m *Matcher) Match(ctx context.Context, variants []string, year int) (*Result, error) {
	if len(variants) == 0 {
		return &Result{Decision: DecisionNone, Reason: "no search variants"}, nil
	}
	res := &Result{}
	var best []Candidate
	for i, q := range variants {
		if i >= MaxAPICalls {
			break
		}
		if err := ctx.Err(); err != nil {
			return res, err
		}
		res.Tried++
		srs, err := m.TMDB.SearchMovieAll(ctx, q, MaxCandidates)
		if err != nil {
			if errors.Is(err, tmdb.ErrNotFound) {
				m.Logger.Debug("matcher: no results", "query", q)
				continue
			}
			return res, err
		}
		scored := scoreAll(srs, q, year)
		best = append(best, scored...)
		if top := topCandidate(scored); top != nil && top.Score >= AutoAccept {
			res.Decision = DecisionAccept
			res.Best = top
			res.Candidates = scored[:min(5, len(scored))]
			res.Reason = fmt.Sprintf("accepted %q (%d) score %.0f via %q", top.Title, top.Year, top.Score, q)
			return res, nil
		}
	}
	if len(best) == 0 {
		res.Reason = "no candidates from any variant"
		return res, nil
	}
	sort.SliceStable(best, func(i, j int) bool { return best[i].Score > best[j].Score })
	top := &best[0]
	res.Candidates = best[:min(5, len(best))]
	switch {
	case top.Score >= AutoAccept: // unreachable in practice; kept for clarity
		res.Decision = DecisionAccept
		res.Best = top
	case top.Score >= PendingMin:
		res.Decision = DecisionPending
		res.Best = top
		res.Reason = fmt.Sprintf("best %q (%d) score %.0f below auto-accept", top.Title, top.Year, top.Score)
	default:
		res.Decision = DecisionNone
		res.Best = top
		res.Reason = fmt.Sprintf("best %q (%d) score %.0f below pending floor", top.Title, top.Year, top.Score)
	}
	return res, nil
}

// Search is the manual-review entry point: one query, all scored candidates,
// no decision thresholds. The API layer uses it to render "pick the right
// film" cards; the ordering (score desc) makes the likely match first.
func (m *Matcher) Search(ctx context.Context, query string, year int) (*Result, error) {
	srs, err := m.TMDB.SearchMovieAll(ctx, query, MaxCandidates)
	if err != nil {
		return nil, err
	}
	scored := scoreAll(srs, query, year)
	var best *Candidate
	if len(scored) > 0 {
		best = &scored[0]
	}
	return &Result{Candidates: scored, Best: best, Tried: 1}, nil
}

func topCandidate(cs []Candidate) *Candidate {
	if len(cs) == 0 {
		return nil
	}
	return &cs[0] // scoreAll returns sorted desc
}

func scoreAll(srs []tmdb.SearchResult, query string, year int) []Candidate {
	out := make([]Candidate, 0, len(srs))
	for i := range srs {
		sr := &srs[i]
		out = append(out, Candidate{
			TMDBID:   sr.ID,
			Title:    sr.Title,
			Original: sr.OriginalTitle,
			Year:     sr.Year(),
			Poster:   sr.PosterPath,
			Overview: sr.Overview,
			Score:    Score(sr.Title, sr.OriginalTitle, sr.Year(), query, year, i),
			YearDiff: abs(sr.Year() - year),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Score computes the 0-100 match score:
//
//	60 * titleSim + 30 * yearSim + 10 * popularityRank
//
// titleSim compares the query against BOTH the localized and original titles,
// taking the max; token-level fuzzy equality (edit distance <= 1 per word)
// absorbs release-name typos ("Grean Snake" vs "Green Snake"). An exact
// normalised title match with |yearDiff| <= 1 short-circuits to 100.
func Score(title, originalTitle string, candYear int, query string, queryYear int, rank int) float64 {
	qn := normTitle(query)
	ts := titleSimilarity(qn, normTitle(title))
	if originalTitle != "" {
		if o := titleSimilarity(qn, normTitle(originalTitle)); o > ts {
			ts = o
		}
	}

	// exact-title short circuit (same normalised text, plausible year)
	if ts >= 0.999 {
		d := abs(candYear - queryYear)
		if queryYear == 0 || d <= 1 {
			return 100
		}
	}

	var yearSim float64
	switch d := abs(candYear - queryYear); {
	case queryYear == 0, candYear == 0:
		yearSim = 0.5 // unknown year: neither reward nor punish
	case d == 0:
		yearSim = 1.0
	case d == 1:
		yearSim = 0.7
	case d == 2:
		yearSim = 0.3
	default:
		yearSim = 0
	}

	pop := 10.0
	if rank > 0 {
		pop -= math.Log2(float64(rank)+1) * 2 // rank 1 -> ~8, rank 10 -> ~3.4
		if pop < 0 {
			pop = 0
		}
	}

	score := 60*ts + 30*yearSim + pop
	if score > 100 {
		score = 100
	}
	return math.Round(score*10) / 10
}

// titleSimilarity is the max of token-set Jaccard (order-free) and a
// character-level similarity on the whole normalised string (order-sensitive).
// Both use fuzzy token equality (edit distance <= 1) so near-identical titles
// score high while different titles do not.
func titleSimilarity(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	if j := jaccardFuzzy(tokens(a), tokens(b)); j > 0 {
		if j2 := jaccardFuzzy(tokens(b), tokens(a)); j2 > j {
			j = j2
		}
		if j > 0 {
			return j
		}
	}
	return normalizedLevenshtein(a, b)
}

func jaccardFuzzy(as, bs []string) float64 {
	if len(as) == 0 || len(bs) == 0 {
		return 0
	}
	matched := 0
	used := make([]bool, len(bs))
	for _, a := range as {
		for j, b := range bs {
			if used[j] {
				continue
			}
			if fuzzyEq(a, b) {
				used[j] = true
				matched++
				break
			}
		}
	}
	union := len(as) + len(bs) - matched
	if union == 0 {
		return 0
	}
	return float64(matched) / float64(union)
}

// fuzzyEq reports whether two lowercase tokens are equal or one edit apart
// ("grean"~"green", "goes"~"go" is distance 2 and does NOT match).
func fuzzyEq(a, b string) bool {
	if a == b {
		return true
	}
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	d := levenshtein(a, b)
	return d <= 1
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = minInt(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

func normalizedLevenshtein(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 || len(rb) == 0 {
		return 0
	}
	d := levenshtein(a, b)
	maxLen := len(ra)
	if len(rb) > maxLen {
		maxLen = len(rb)
	}
	sim := 1 - float64(d)/float64(maxLen)
	if sim < 0 {
		sim = 0
	}
	return sim
}

func minInt(vals ...int) int {
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

// superscripts maps Unicode superscript digits to their ASCII forms so
// "Alien³" and "Alien 3" compare equal.
var superscripts = map[rune]rune{
	'⁰': '0', '¹': '1', '²': '2', '³': '3', '⁴': '4',
	'⁵': '5', '⁶': '6', '⁷': '7', '⁸': '8', '⁹': '9',
}

// normTitle lowercases, drops punctuation/quotes, folds superscripts to ASCII
// digits and collapses whitespace, so "One Flew Over the Cuckoo's Nest",
// "one flew over the cuckoos nest" and "Alien³" vs "alien 3" compare equal.
func normTitle(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		// superscripts must fold BEFORE the IsDigit check - Unicode classifies
		// ³ as Nd. The folded digit becomes its own token: "Alien³" ->
		// "alien 3", matching "Alien 3".
		if sup, ok := superscripts[r]; ok {
			if unicode.IsLetter(lastRune(b.String())) {
				b.WriteRune(' ')
			}
			b.WriteRune(sup)
			continue
		}
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), unicode.IsSpace(r):
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// lastRune returns the final rune of s, or 0 when empty.
func lastRune(s string) rune {
	runes := []rune(s)
	if len(runes) == 0 {
		return 0
	}
	return runes[len(runes)-1]
}

func tokens(s string) []string {
	return strings.Fields(s)
}
