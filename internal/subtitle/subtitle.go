// Package subtitle names uploaded subtitle files after the library's
// convention:
//
//	<movie stem>.<lang>.<ext>          e.g. Z.1969....-HANDJOB.chi.ass
//	<movie stem>.verN.<lang>.<ext>     when (lang, ext) is already taken
//
// The movie stem is the video file's name without its extension; lang is an
// ISO 639-2 code chosen by the user. Version suffixes start at ver1 and are
// allocated per (lang, ext) pair - a chi.srt colliding with an existing
// chi.srt becomes ver1, a second one ver2, while a chi.ass never versions
// against a chi.srt.
package subtitle

import (
	"fmt"
	"path/filepath"
	"sort"
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

// Plan is the naming decision for one uploaded subtitle.
type Plan struct {
	OriginalName string // the file as uploaded
	FinalName    string // the name it gets on disk
	Ext          string // ".srt" / ".ass" / ".ssa"
	Lang         string // ISO 639-2 code
	Version      int    // 0 = unversioned, N for verN
}

// Name allocates the on-disk filename for one uploaded subtitle given the
// movie's video file name and the set of filenames already present in the
// release directory (video, subs, nfo - anything). existing is used to
// reserve the (lang, ext) namespace so colliding uploads version themselves.
//
// forceVersion skips the unversioned name entirely and starts at ver1 - used
// for members of a same-(lang, ext) group inside one batch (see NameBatch).
func Name(videoFile, lang, uploadedName string, existing map[string]bool, forceVersion bool) Plan {
	ext := strings.ToLower(filepath.Ext(uploadedName))
	stem := strings.TrimSuffix(videoFile, filepath.Ext(videoFile))

	try := fmt.Sprintf("%s.%s%s", stem, lang, ext)
	version := 0
	if forceVersion || existing[try] {
		version = nextFreeVersion(stem, lang, ext, existing)
		try = fmt.Sprintf("%s.ver%d.%s%s", stem, version, lang, ext)
	}
	existing[try] = true
	return Plan{
		OriginalName: uploadedName,
		FinalName:    try,
		Ext:          ext,
		Lang:         lang,
		Version:      version,
	}
}

// nextFreeVersion returns the smallest N >= 1 whose verN name is not taken.
func nextFreeVersion(stem, lang, ext string, existing map[string]bool) int {
	for n := 1; ; n++ {
		if !existing[fmt.Sprintf("%s.ver%d.%s%s", stem, n, lang, ext)] {
			return n
		}
	}
}

// NameBatch allocates names for a whole upload batch. existing should contain
// every filename currently in the release directory; the batch's own
// allocations are added to it as they are made.
//
// When several files in the batch share a (lang, ext) pair, NONE of them takes
// the unversioned name - they all get versions, starting at the first free
// one. The unversioned slot is the canonical subtitle for that pair, and
// picking which upload deserves it is a judgement the uploader did not make;
// e.g. uploading two chi.srt files onto a clean release yields ver1 and ver2
// (a lone chi.srt upload with no collision still lands unversioned).
func NameBatch(videoFile string, uploads []Upload, existing map[string]bool) []Plan {
	taken := make(map[string]bool, len(existing)+len(uploads))
	for k := range existing {
		taken[k] = true
	}
	groupSize := make(map[string]int, len(uploads))
	for _, u := range uploads {
		ext := strings.ToLower(filepath.Ext(u.Filename))
		groupSize[u.Lang+ext]++
	}
	plans := make([]Plan, 0, len(uploads))
	for _, u := range uploads {
		ext := strings.ToLower(filepath.Ext(u.Filename))
		forceVersion := groupSize[u.Lang+ext] > 1
		plans = append(plans, Name(videoFile, u.Lang, u.Filename, taken, forceVersion))
	}
	return plans
}

// Upload is one file in an upload batch, already validated.
type Upload struct {
	Filename string // the uploaded file's original name
	Lang     string // ISO 639-2 code chosen by the user
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
