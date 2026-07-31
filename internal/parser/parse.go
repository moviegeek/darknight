package parser

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// ParseTitle parses a release name (directory or file name) into structured
// metadata. It is deliberately forgiving: anything it cannot classify is left
// in the Title.
//
// Pipeline:
//  1. strip a trailing "[size]" and a leading "[zh-title]" bracket block
//  2. normalise brackets / parens / commas / underscores to spaces (dots and
//     dashes are handled by the tokeniser, which keeps "5.1" and "H.264" whole)
//  3. tokenise
//  4. peel the release group off the last token (everything after the last "-"
//     in the trailing token)
//  5. classify each token; the title is whatever precedes the first technical
//     marker (year / resolution / source / codec)
func ParseTitle(name string) FileMeta {
	m := FileMeta{}
	if name = strings.TrimSpace(name); name == "" {
		return m
	}

	name, _ = stripTrailingBracket(name)
	name = stripLeadingBracket(name)
	name = normaliseSeparators(name)

	fields := tokenize(name)

	// Peel the release group off the trailing token. Internal hyphens inside
	// technical terms (DTS-HD, Blu-Ray, DD-EX, E-AC-3, VC-1, DTS-X) are kept
	// because they live in non-trailing tokens; only the last token can carry
	// a "-Group" suffix.
	if n := len(fields); n > 0 {
		head, group := splitGroup(fields[n-1])
		m.ReleaseGroup = group
		if head == "" {
			fields = fields[:n-1]
		} else {
			fields[n-1] = head
		}
	}

	year, yearIdx := findYear(fields)
	m.Year = year

	source, sourceIdx := findSource(fields)
	m.Source = source

	resolution, resIdx := findResolution(fields)
	m.Resolution = resolution

	m.VideoCodec, m.AudioCodec, m.AudioChannels = findCodecs(fields)
	m.HDR, m.BitDepth = findHDR(fields)
	m.Edition = findEdition(fields)
	m.AudioCount = findAudioCount(fields)
	m.Language = findLanguage(fields)
	m.IsCollection = detectCollection(fields)

	// Build the title slice, then drop any trailing token that is itself a
	// detected technical marker (year range, "Collection" brand, edition).
	titleEnd := minNonNegative(yearIdx, sourceIdx, resIdx)
	var titleFields []string
	if titleEnd < 0 {
		titleFields = fields
	} else {
		titleFields = fields[:titleEnd]
	}
	// trim a trailing year-range token ("1979-1997") that findYear skipped.
	for len(titleFields) > 0 {
		last := titleFields[len(titleFields)-1]
		if _, _, ok := splitYearRange(last); ok {
			titleFields = titleFields[:len(titleFields)-1]
			continue
		}
		break
	}
	m.Title = cleanTitle(strings.Join(titleFields, " "))

	return m
}

// ---------- normalisation ----------

var (
	trailingBracketRe = regexp.MustCompile(`\s*-*\s*\[[^\[\]]*\]\s*$`)
	leadingBracketRe  = regexp.MustCompile(`^\[[^\[\]]*\]\s*`)
)

func stripTrailingBracket(s string) (string, string) {
	loc := trailingBracketRe.FindStringSubmatchIndex(s)
	if loc == nil {
		return s, ""
	}
	inner := s[loc[2]:loc[3]]
	return strings.TrimSpace(s[:loc[0]]), strings.TrimSpace(inner)
}

func stripLeadingBracket(s string) string {
	return leadingBracketRe.ReplaceAllString(s, "")
}

// normaliseSeparators turns structural punctuation into spaces. Dots and dashes
// are intentionally left alone — the tokeniser handles them so that decimal
// channel layouts ("5.1") and version numbers survive.
func normaliseSeparators(s string) string {
	r := strings.NewReplacer(
		"_", " ",
		"(", " ", ")", " ",
		"[", " ", "]", " ",
		"{", " ", "}", " ",
		",", " ",
	)
	return collapseSpaces(r.Replace(s))
}

func collapseSpaces(s string) string {
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

// tokenize splits on spaces, dots and underscores, but keeps a dot that sits
// between two digits ("5.1", "7.1", "2.0") or between a single letter and a
// digit ("H.264", "x.264"). This preserves channel layouts and codec version
// numbers while still splitting "DTS-HD.MA" -> ["DTS-HD", "MA"].
func tokenize(s string) []string {
	runes := []rune(s)
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = cur[:0]
		}
	}
	isSep := func(r rune) bool {
		return r == ' ' || r == '.' || r == '_' || r == '\t'
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if !isSep(r) {
			cur = append(cur, r)
			continue
		}
		// r is a separator — decide whether to keep it inline.
		if r == '.' && keepDot(runes, i) {
			cur = append(cur, r)
			continue
		}
		flush()
	}
	flush()
	return out
}

// keepDot reports whether the dot at runes[i] should stay inside the current
// token rather than acting as a separator.
//
// We keep a dot in three situations:
//
//  1. codec version, e.g. "H.264", "x.265": a single letter at the token start
//     followed by a digit.
//  2. a glued channel layout: the run AFTER the dot is a single digit that is
//     itself followed by a separator, and the character before the dot is a
//     single digit (channel count) preceded by a letter or by nothing
//     ("FLAC1.0", "MA5.1", "AAC2.0", "TrueHD7.1", "DD-EX5.1", "5.1").
//  3. a standalone channel token "5.1" / "7.1": lone digit on both sides, each
//     surrounded by separators.
//
// NOT kept: "2001.1080p" (left run "2001" has 4 digits), "2160p.10bit" (letter
// 'p' before the dot, not a single-digit channel count),
// "5.1.3Audio" (right run "1" is chased by a letter).
func keepDot(runes []rune, i int) bool {
	if i <= 0 || i >= len(runes)-1 {
		return false
	}
	left := runes[i-1]
	right := runes[i+1]

	// (1) codec version: letter at token start, then digit. The letter must be
	// at the start of a *space*-delimited token, so hyphens inside "DTS-X" /
	// "E-AC-3" / "VC-1" do not trigger this branch.
	if unicode.IsLetter(left) && unicode.IsDigit(right) {
		return i-2 < 0 || isSpaceSep(runes[i-2])
	}

	if !unicode.IsDigit(left) || !unicode.IsDigit(right) {
		return false
	}

	// right run must be a single digit followed by a separator (or end).
	if i+2 < len(runes) && !isSep(runes[i+2]) {
		return false
	}

	// left run: must be a single channel-count digit (1..9). That means the
	// character before `left` is either absent or NOT a digit. It may be a
	// letter (codec stem like "FLAC" / "MA" / "AAC" / "TrueHD") — that's the
	// glued case — or a separator (standalone "5.1").
	if i-2 >= 0 && unicode.IsDigit(runes[i-2]) {
		return false
	}
	return true
}

// isSep reports whether r is a token boundary. For keepDot purposes the dash
// also counts as a boundary, because it introduces the release group
// ("TrueHD7.1-HDChina" -> the "1" is chased by "-").
func isSep(r rune) bool {
	return r == ' ' || r == '.' || r == '_' || r == '\t' || r == '-'
}

// isSpaceSep is the stricter space-only boundary, used to decide whether a
// letter is at the start of a token. Hyphens inside "DTS-X" / "VC-1" must NOT
// count as token starts.
func isSpaceSep(r rune) bool {
	return r == ' ' || r == '_' || r == '\t'
}

// splitGroup separates a trailing "-Group" suffix from the head of the last
// token. "user@group" collapses to "group". Returns head="" if the token had no
// group separator (so the caller can drop it).
func splitGroup(last string) (head, group string) {
	idx := strings.LastIndex(last, "-")
	if idx < 0 {
		return last, ""
	}
	head = last[:idx]
	group = last[idx+1:]
	if at := strings.LastIndex(group, "@"); at >= 0 {
		group = group[at+1:]
	}
	group = strings.Trim(group, "[](){}")
	return head, group
}

// ---------- year ----------

func findYear(fields []string) (int, int) {
	for i := len(fields) - 1; i >= 0; i-- {
		if y, ok := tryYear(fields[i]); ok {
			return y, i
		}
	}
	return 0, -1
}

func tryYear(s string) (int, bool) {
	if len(s) != 4 {
		return 0, false
	}
	c0 := s[0]
	if c0 != '1' && c0 != '2' {
		return 0, false
	}
	y, err := strconv.Atoi(s)
	if err != nil || y < 1910 || y > 2099 {
		return 0, false
	}
	return y, true
}

// ---------- source ----------

var sourceMap = map[string]Source{
	"blu-ray": BluRay, "bluray": BluRay, "blueray": BluRay,
	"bdrip": BDRip, "brrip": BRRip,
	"hdtv": HDTV, "uhdtv": UHDTV,
	"web-dl": WebDL, "webdl": WebDL, "webrip": WebDL, "web": WebDL,
	"dvdrip": DVDRip, "dvd": DVDRip,
	"3d": ThreeD, "sbs": ThreeD,
}

func findSource(fields []string) (Source, int) {
	hasUHD := false
	for i, f := range fields {
		switch strings.ToLower(f) {
		case "uhd":
			hasUHD = true
		case "blu-ray", "bluray", "blueray":
			if hasUHD {
				return UHDBluRay, i
			}
			return BluRay, i
		}
		if src, ok := sourceMap[strings.ToLower(f)]; ok {
			return src, i
		}
	}
	return SourceUnknown, -1
}

// ---------- resolution ----------

var resolutionMap = map[string]Resolution{
	"480p": Res480p, "480i": Res480p,
	"720p": Res720p, "720i": Res720p,
	"1080p": Res1080p, "1080i": Res1080p,
	"2160p": Res2160p, "2160i": Res2160p,
}

// "4k" as a bare resolution token is intentionally NOT mapped, because in
// "4K.Remastered" the "4K" is an edition adjective, not the resolution. The
// presence of "2160p" is the authoritative 4K signal.
func findResolution(fields []string) (Resolution, int) {
	for i, f := range fields {
		if r, ok := resolutionMap[strings.ToLower(f)]; ok {
			return r, i
		}
	}
	return ResUnknown, -1
}

// ---------- codecs ----------

var videoCodecMap = map[string]VideoCodec{
	"x264": X264, "x.264": X264,
	"x265": X265, "x.265": X265,
	"avc": AVC, "h264": H264, "h.264": H264,
	"hevc": HEVC, "h265": H265, "h.265": H265,
	"vc1": VC1, "vc-1": VC1,
	"vp9": VP9,
}

var (
	// standaloneChannels matches a bare "N.M" channel token like "5.1". The dot
	// is required so a lone "3" (title fragment) or a year is not mistaken for
	// a layout.
	standaloneChannels = regexp.MustCompile(`^\d\.\d$`)
	// trailingChannels requires at least one alpha letter to anchor the match,
	// so a bare "AC3" (no dot) does not yield "3".
	trailingChannels = regexp.MustCompile(`[a-z+]\d(?:\.\d)$`)
)

// extractChannels pulls a "N.M" channel layout out of a token, whether it
// stands alone ("5.1") or is glued onto a codec ("TrueHD7.1", "DD+5.1",
// "MA5.1"). It deliberately does NOT extract from codec names that contain a
// trailing digit but no dot ("AC3", "EAC3") — those are codec stems.
func extractChannels(lowerField string) string {
	if standaloneChannels.MatchString(lowerField) {
		return lowerField
	}
	if m := trailingChannels.FindString(lowerField); m != "" {
		return strings.TrimLeft(m, "abcdefghijklmnopqrstuvwxyz+")
	}
	return ""
}

func findCodecs(fields []string) (VideoCodec, AudioCodec, string) {
	var vc VideoCodec
	var ac AudioCodec
	channels := ""

	for _, f := range fields {
		lf := strings.ToLower(f)

		// strip a trailing channel layout to get the codec stem
		stem := lf
		if ch := extractChannels(lf); ch != "" {
			if channels == "" {
				channels = ch
			}
			stem = strings.TrimSuffix(lf, ch)
		}

		// video codec — try stem then the raw token (for "h.264" kept whole)
		if c, ok := videoCodecMap[stem]; ok {
			vc = c
			continue
		}
		if c, ok := videoCodecMap[lf]; ok {
			vc = c
			continue
		}

		// audio codec
		switch stem {
		case "dts":
			ac = pickAudio(ac, DTS)
		case "dts-hd":
			ac = pickAudio(ac, DTSHDMA)
		case "dts-x", "dts:x":
			ac = pickAudio(ac, DTSX)
		case "truehd":
			ac = pickAudio(ac, TrueHD)
		case "atmos":
			ac = pickAudio(ac, Atmos)
		case "dd+":
			ac = pickAudio(ac, DDP)
		case "dd":
			ac = pickAudio(ac, DD)
		case "ac3":
			ac = pickAudio(ac, AC3)
		case "eac3", "e-ac-3", "ddp":
			ac = pickAudio(ac, EAC3)
		case "flac":
			ac = pickAudio(ac, FLAC)
		case "aac":
			ac = pickAudio(ac, AAC)
		case "pcm":
			ac = pickAudio(ac, PCM)
		case "lpcm":
			ac = pickAudio(ac, LPCM)
		case "dolby.atmos":
			ac = pickAudio(ac, DolbyAtmos)
		}
	}

	return vc, ac, channels
}

// pickAudio keeps the higher-priority codec. Real base codecs (TrueHD, DTS-HD
// MA, ...) outrank Atmos, which is only a metadata layer on top of them.
func pickAudio(cur, candidate AudioCodec) AudioCodec {
	if audioPriority(candidate) > audioPriority(cur) {
		return candidate
	}
	return cur
}

func audioPriority(c AudioCodec) int {
	switch c {
	case TrueHD, DTSHDMA, DTSX, DTS, EAC3, AC3, DDP, DD, FLAC, AAC, PCM, LPCM:
		return 2
	case Atmos, DolbyAtmos:
		return 1
	}
	return 0
}

// ---------- HDR / bit depth ----------

func findHDR(fields []string) (HDRMode, int) {
	var mode HDRMode
	bitDepth := 0
	for _, f := range fields {
		switch strings.ToLower(f) {
		case "hdr":
			if mode == HDRNone {
				mode = HDR10
			}
		case "hdr10+":
			mode = HDR10Plus
		case "hdr10":
			mode = HDR10
		case "hlg":
			mode = HLG
		case "dv":
			mode = DolbyVision
		case "dovi", "dolby.vision":
			mode = DolbyVision
		case "10bit":
			bitDepth = 10
		case "8bit":
			bitDepth = 8
		case "12bit":
			bitDepth = 12
		}
	}
	return mode, bitDepth
}

// ---------- edition ----------

var editionMap = map[string]string{
	"repack":      "Repack",
	"remux":       "Remux",
	"remastered":  "Remastered",
	"dc":          "Director's Cut",
	"extended":    "Extended",
	"theatrical":  "Theatrical Cut",
	"uncut":       "Uncut",
	"imax":        "IMAX",
	"criterion":   "Criterion",
	"cc":          "Criterion Collection",
	"collector's": "Collector's Edition",
	"hybrid":      "Hybrid",
	"internal":    "Internal",
	"proper":      "Proper",
	"final":       "Final Cut",
}

// findEdition returns the most specific edition label. Multi-word editions are
// matched first so "Extended Cut" wins over a bare "Extended".
func findEdition(fields []string) string {
	lowered := make([]string, len(fields))
	for i, f := range fields {
		lowered[i] = strings.ToLower(f)
	}

	for i := 0; i+1 < len(lowered); i++ {
		switch lowered[i] + "." + lowered[i+1] {
		case "extended.cut":
			return "Extended Cut"
		case "director's.cut", "directors.cut":
			return "Director's Cut"
		case "imax.edition":
			return "IMAX Edition"
		case "anniversary.edition":
			return "Anniversary Edition"
		case "collector's.edition":
			return "Collector's Edition"
		case "100th.anniversary":
			return "100th Anniversary Edition"
		}
	}

	for _, f := range lowered {
		if e, ok := editionMap[f]; ok {
			return e
		}
	}
	return ""
}

// ---------- audio count ----------

var audioCountRe = regexp.MustCompile(`^(\d+)audios?$`)

func findAudioCount(fields []string) int {
	maxn := 0
	for _, f := range fields {
		lf := strings.ToLower(f)
		if m := audioCountRe.FindStringSubmatch(lf); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > maxn {
				maxn = n
			}
			continue
		}
		switch lf {
		case "triaudio":
			if maxn < 3 {
				maxn = 3
			}
		case "dual", "dual.audio":
			if maxn < 2 {
				maxn = 2
			}
		}
	}
	return maxn
}

// ---------- language / region ----------

var languageSet = map[string]string{
	"jpn": "JPN", "kor": "KOR", "cee": "CEE", "fra": "FRA", "ger": "GER",
	"uk": "UK", "us": "US", "hk": "HK", "tw": "TW", "chi": "CHI", "eng": "ENG",
}

func findLanguage(fields []string) string {
	for _, f := range fields {
		if lang, ok := languageSet[strings.ToLower(f)]; ok {
			return lang
		}
	}
	return ""
}

// ---------- collection detection ----------

// detectCollection flags multi-movie packs only: anthology/trilogy/boxset or a
// year range ("1979-1997"). A bare "Collection" (as in "Criterion Collection")
// is a brand/edition, not a multi-movie pack, and is intentionally ignored.
func detectCollection(fields []string) bool {
	for _, f := range fields {
		switch strings.ToLower(f) {
		case "anthology", "trilogy", "quadrilogy", "boxset", "series":
			return true
		}
	}
	for _, f := range fields {
		if _, _, ok := splitYearRange(f); ok {
			return true
		}
	}
	return false
}

func splitYearRange(s string) (int, int, bool) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	a, ok1 := tryYear(parts[0])
	b, ok2 := tryYear(parts[1])
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	return a, b, true
}

// ---------- title cleanup ----------

func cleanTitle(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, "- ")
	return s
}

// ---------- shared ----------

func minNonNegative(ints ...int) int {
	m := math.MaxInt32
	found := false
	for _, v := range ints {
		if v >= 0 && v < m {
			m = v
			found = true
		}
	}
	if !found {
		return -1
	}
	return m
}
