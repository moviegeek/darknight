// Package rename rebuilds release names after a manual TMDB match so the
// on-disk layout follows the library's scene-name convention:
//
//	dir:   Title.Name.YEAR.<technical tokens>[-Group]
//	file:  <dir name>.<ext>
//	subs:  <dir name>.<lang>.srt   (language tag preserved from the old name)
//
// The technical tail (resolution, source, codecs, channels, group) is taken
// from the EXISTING name - the matcher only got the title wrong, so the rest
// of the release name is already correct. Only the leading "Title.YEAR"
// segment is rebuilt from the matched TMDB movie.
package rename

import (
	"fmt"
	"path/filepath"

	"sort"
	"strings"
)

// yearAt reports whether field is a bare release-year token (4 digits,
// 1910-2099). It replaces a lookahead regex, which Go's regexp does not
// support.
func yearAt(field string) bool {
	if len(field) != 4 {
		return false
	}
	if field[0] != '1' && field[0] != '2' {
		return false
	}
	for i := 0; i < 4; i++ {
		if field[i] < '0' || field[i] > '9' {
			return false
		}
	}
	y := int(field[0]-'0')*1000 + int(field[1]-'0')*100 + int(field[2]-'0')*10 + int(field[3]-'0')
	return y >= 1910 && y <= 2099
}

// TitleYear replaces the leading "Title[.Year]" segment of a release name
// with the matched movie's title and year, keeping the technical tail.
//
//	"Grean.Snake.1993.BluRay.1080p.x264-HDChina" + ("Green Snake", 1993)
//	  -> "Green.Snake.1993.BluRay.1080p.x264-HDChina"
//
// When the old name has no year token, the new title+year is prepended and
// the first technical token onwards is kept.
func TitleYear(oldName string, title string, year int) string {
	title = sanitize(title)
	if title == "" {
		return oldName
	}
	head := title
	if year > 0 {
		head += "." + fmt.Sprint(year)
	}
	rest := technicalTail(oldName)
	if rest == "" {
		return head
	}
	return head + "." + rest
}

// technicalTail returns the technical segment of a release name - everything
// after the "Title[.Year]" head. The year belongs to the head (it is what a
// rematch replaces), so when the year is the first technical token the tail
// starts AFTER it. A name with nothing technical yields "".
func technicalTail(name string) string {
	fields := strings.FieldsFunc(name, func(r rune) bool { return r == '.' || r == '_' || r == ' ' })
	start := -1
	for i, f := range fields {
		if !isTechnical(f) {
			continue
		}
		if yearAt(strings.ToLower(f)) {
			// year closes the head; the technical tail begins after it
			start = i + 1
			continue
		}
		if start < 0 {
			start = i
		}
		break
	}
	if start < 0 || start >= len(fields) {
		return ""
	}
	return strings.Join(fields[start:], ".")
}

var technicalTokens = map[string]bool{
	// resolution
	"480p": true, "576p": true, "720p": true, "1080p": true, "1080i": true,
	"2160p": true, "2160i": true,
	// source
	"blu-ray": true, "bluray": true, "blueray": true, "bdrip": true,
	"brrip": true, "web-dl": true, "webdl": true, "webrip": true, "web": true,
	"hdtv": true, "uhdtv": true, "dvdrip": true, "dvd": true, "hddvd": true,
	// video codecs
	"x264": true, "x265": true, "x.264": true, "x.265": true,
	"avc": true, "h264": true, "h.264": true, "hevc": true, "h265": true,
	"h.265": true, "vc-1": true, "vc1": true, "mpeg-2": true, "vp9": true,
	// audio codecs
	"dts": true, "truehd": true, "atmos": true, "flac": true, "lpcm": true,
	"pcm": true, "aac": true, "ac3": true, "dd": true, "dd+": true,
	"dts-hd": true, "dts-x": true, "eac3": true,
	// quality flags
	"hdr": true, "hdr10": true, "hdr10+": true, "dv": true, "dovi": true,
	"10bit": true, "8bit": true, "remux": true, "remastered": true,
	"repack": true, "imax": true, "extended": true, "uncut": true,
	// edition brands often appear before the year
	"criterion": true, "collection": true,
}

func isTechnical(f string) bool {
	lf := strings.ToLower(f)
	if technicalTokens[lf] {
		return true
	}
	return yearAt(lf)
}

// sanitize turns a display title into a release-name token: punctuation and
// Unicode punctuation collapse to dots, CJK stays verbatim, runs of dots
// collapse, leading/trailing dots strip. Ampersands become "and" (scene
// convention dislikes '&' in names).
//
//	"Léon: The Professional" -> "Léon.The.Professional"
//	"Alien³"                 -> "Alien³"   (kept; scene names keep superscripts)
//	"Thelma & Louise"        -> "Thelma.and.Louise"
func sanitize(title string) string {
	var b strings.Builder
	lastDot := false
	for _, r := range strings.TrimSpace(title) {
		switch {
		case r == ' ':
			if !lastDot {
				b.WriteRune('.')
				lastDot = true
			}
		case r == '&':
			b.WriteString("and")
			lastDot = false
		case isNameRune(r):
			b.WriteRune(r)
			lastDot = false
		default:
			// punctuation -> separator
			if !lastDot {
				b.WriteRune('.')
				lastDot = true
			}
		}
	}
	s := strings.Trim(b.String(), ".")
	// collapse any ".." left by consecutive punctuation
	for strings.Contains(s, "..") {
		s = strings.ReplaceAll(s, "..", ".")
	}
	return s
}

func isNameRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r >= 0x00C0: // Latin-1 supplement onward: é, ł, CJK, kana, hangul, ³
		return true
	}
	return false
}

// Plan is the computed rename for one release directory: the new dir name and
// the renames of every file inside it (video, subs, nfo).
type Plan struct {
	DirOld string `json:"dir_old"` // old dir name (base)
	DirNew string `json:"dir_new"` // new dir name (base)
	// Moves are ordered: files first, dir last (a dir cannot be renamed while
	// containing files with the old name contextually... actually it can, but
	// doing files first keeps each move within one dir).
	Moves []Move `json:"moves"`
}

// Move is one filesystem rename: from -> to, both absolute.
type Move struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"` // video | subtitle | nfo | other
}

// Build computes the rename plan for one release: given the release dir's
// absolute path, the main video file name, every sibling file name, and the
// matched (title, year). It preserves extensions, subtitle language tags and
// secondary file suffixes; the plan is empty when the name is already
// canonical.
func Build(dirAbs, mainFile string, siblings []string, title string, year int) Plan {
	oldDirBase := filepath.Base(dirAbs)
	newDirBase := TitleYear(oldDirBase, title, year)
	plan := Plan{DirOld: oldDirBase, DirNew: newDirBase}
	if newDirBase == oldDirBase {
		return plan // already canonical
	}
	for _, f := range siblings {
		ext := filepath.Ext(f)
		stem := strings.TrimSuffix(f, ext)
		newStem := TitleYear(stem, title, year)
		if newStem == stem {
			continue
		}
		plan.Moves = append(plan.Moves, Move{
			From: filepath.Join(dirAbs, f),
			To:   filepath.Join(dirAbs, newStem+ext),
			Kind: classify(f, mainFile),
		})
	}
	// deterministic order: video first, then subs, nfo, others; alphabetical
	sort.SliceStable(plan.Moves, func(i, j int) bool {
		ki, kj := kindRank(plan.Moves[i].Kind), kindRank(plan.Moves[j].Kind)
		if ki != kj {
			return ki < kj
		}
		return plan.Moves[i].From < plan.Moves[j].From
	})
	return plan
}

func classify(f, mainFile string) string {
	if f == mainFile {
		return "video"
	}
	switch strings.ToLower(filepath.Ext(f)) {
	case ".srt", ".ass", ".ssa", ".sub", ".idx":
		return "subtitle"
	case ".nfo":
		return "nfo"
	}
	return "other"
}

func kindRank(k string) int {
	switch k {
	case "video":
		return 0
	case "subtitle":
		return 1
	case "nfo":
		return 2
	default:
		return 3
	}
}
