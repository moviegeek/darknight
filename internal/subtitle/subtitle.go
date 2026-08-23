// Package subtitle names uploaded subtitle files after the library's
// convention:
//
//	<movie stem>.<lang>.<ext>          e.g. Z.1969....-HANDJOB.chi.ass
//	<movie stem>.verN.<lang>.<ext>     when the pair already has files
//
// The movie stem is the video file's name without its extension; lang is an
// ISO 639-2 code chosen by the user.
//
// The unversioned name is the canonical slot for one (lang, ext) pair and is
// only ever held by exactly one file. When an upload would put a second file
// into an occupied namespace, EVERY file in it is versioned: an existing
// unversioned subtitle vacates to the next free version (ver1 when the
// namespace had none) and the uploads take the higher numbers that follow.
// Version numbers are per (lang, ext) - a chi.ass never versions against a
// chi.srt.
package subtitle

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// iso639-2 codes the UI offers, mapped from their common display names.
// The map is intentionally small: these are the languages the library's
// subtitles actually carry.
var langCodes = map[string]string{
	"中文":   "chi",
	"粤语":   "yue",
	"英文":   "eng",
	"日文":   "jpn",
	"韩文":   "kor",
	"法语":   "fre",
	"德语":   "ger",
	"西班牙语": "spa",
	"意大利语": "ita",
	"葡萄牙语": "por",
	"俄语":   "rus",
	"泰语":   "tha",
	"其他":   "und",
}

// LangCode resolves a display name or a raw code to an ISO 639-2 code.
// Unknown values return "und" (undetermined) rather than erroring - a wrong
// language tag is repairable by renaming, a failed upload is not.
func LangCode(display string) string {
	if code, ok := langCodes[display]; ok {
		return code
	}
	if display != "" && len(display) == 3 {
		return strings.ToLower(display)
	}
	return "und"
}

// Upload is one file in an upload batch, already validated.
type Upload struct {
	Filename string // the uploaded file's original name
	Lang     string // ISO 639-2 code chosen by the user
}

// Plan is the naming decision for one uploaded subtitle.
type Plan struct {
	OriginalName string // the file as uploaded
	FinalName    string // the name it gets on disk
	Ext          string // ".srt" / ".ass" / ".ssa"
	Lang         string // ISO 639-2 code
	Version      int    // 0 = unversioned, N for verN
}

// Rename is an on-disk rename of an EXISTING subtitle that must vacate the
// unversioned slot so the namespace stays unambiguous.
type Rename struct {
	From string // old filename (basename)
	To   string // new filename (basename)
}

// Allocation is the complete naming decision for one upload batch: the
// uploads' plans plus any pre-existing file that has to move first.
type Allocation struct {
	Plans   []Plan
	Renames []Rename
}

// Allocate decides the naming for an upload batch against the current
// contents of the release directory (dirFiles = basenames of every regular
// file in it). Groups are per (lang, ext) pair and independent of each other.
//
// Per group:
//
//   - empty namespace, one upload      -> upload takes the unversioned name
//   - empty namespace, N uploads       -> ver1 .. verN
//   - existing unversioned file        -> it vacates to the next free
//     version (ver1 on a fresh namespace), uploads take the numbers after it
//   - existing versioned files only    -> uploads continue after the highest
func Allocate(videoFile string, uploads []Upload, dirFiles []string) Allocation {
	stem := strings.TrimSuffix(videoFile, filepath.Ext(videoFile))
	existing := make(map[string]bool, len(dirFiles))
	for _, f := range dirFiles {
		existing[f] = true
	}

	// group uploads by (lang, ext), preserving batch order within a group
	type groupKey struct{ lang, ext string }
	groups := make(map[groupKey][]Upload)
	var order []groupKey
	for _, u := range uploads {
		ext := strings.ToLower(filepath.Ext(u.Filename))
		k := groupKey{u.Lang, ext}
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], u)
	}

	var alloc Allocation
	for _, k := range order {
		maxVer := 0
		unversioned := ""
		for _, f := range dirFiles {
			if v, ok := parseNamespaceFile(f, stem, k.lang, k.ext); ok {
				if v == 0 {
					unversioned = f
				} else if v > maxVer {
					maxVer = v
				}
			}
		}
		next := maxVer + 1

		// an existing unversioned file vacates the canonical slot first
		if unversioned != "" {
			to := fmt.Sprintf("%s.ver%d.%s%s", stem, next, k.lang, k.ext)
			for existing[to] {
				next++
				to = fmt.Sprintf("%s.ver%d.%s%s", stem, next, k.lang, k.ext)
			}
			alloc.Renames = append(alloc.Renames, Rename{From: unversioned, To: to})
			existing[to] = true
			next++
		}

		for _, u := range groups[k] {
			// the unversioned name is only for a sole, unambiguous file: a
			// group of several uploads, or any file already in the namespace,
			// means everything gets versioned
			sole := len(groups[k]) == 1 && unversioned == "" && maxVer == 0 && next == 1
			if sole {
				name := fmt.Sprintf("%s.%s%s", stem, k.lang, k.ext)
				if !existing[name] {
					existing[name] = true
					alloc.Plans = append(alloc.Plans, Plan{
						OriginalName: u.Filename, FinalName: name,
						Ext: k.ext, Lang: k.lang, Version: 0,
					})
					continue
				}
			}
			name := fmt.Sprintf("%s.ver%d.%s%s", stem, next, k.lang, k.ext)
			for existing[name] {
				next++
				name = fmt.Sprintf("%s.ver%d.%s%s", stem, next, k.lang, k.ext)
			}
			existing[name] = true
			alloc.Plans = append(alloc.Plans, Plan{
				OriginalName: u.Filename, FinalName: name,
				Ext: k.ext, Lang: k.lang, Version: next,
			})
			next++
		}
	}
	return alloc
}

// parseNamespaceFile reports whether filename belongs to the
// stem[.verN].lang.ext namespace and returns its version (0 = unversioned).
func parseNamespaceFile(filename, stem, lang, ext string) (int, bool) {
	suffix := "." + lang + ext
	if !strings.HasSuffix(filename, suffix) {
		return 0, false
	}
	head := strings.TrimSuffix(filename, suffix)
	if head == stem {
		return 0, true
	}
	if strings.HasPrefix(head, stem+".ver") {
		if n, err := strconv.Atoi(strings.TrimPrefix(head, stem+".ver")); err == nil && n >= 1 {
			return n, true
		}
	}
	return 0, false
}

// SortedFilenames returns the plans' final names sorted, for tests and
// diagnostics.
func SortedFilenames(plans []Plan) []string {
	out := make([]string, 0, len(plans))
	for _, p := range plans {
		out = append(out, p.FinalName)
	}
	sort.Strings(out)
	return out
}
