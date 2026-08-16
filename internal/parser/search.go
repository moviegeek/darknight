package parser

import (
	"regexp"
	"strings"
)

// SearchTitle derives the cleanest title to feed a metadata search (TMDB etc.)
// from an already-parsed FileMeta title. It differs from FileMeta.Title, which
// keeps edition/brand words for display: SearchTitle aggressively strips
// release-name noise so the search query matches the canonical movie title.
//
// "Avatar Extended Collector's Edition" -> "Avatar"
// "The Ballad of Narayama MOC"          -> "The Ballad of Narayama"
// "Three Colors GBR UHD"                -> "Three Colors"
// "JFK DirCut"                          -> "JFK"
//
// The empty string is returned when nothing survives the cleanup.
func SearchTitle(title string) string {
	fields := tokenize(normaliseSeparators(title))
	// tokenize keeps codec-style dots ("Z.1969" = letter+digit glue), so split
	// any token that is exactly <letter-run>.<4-digit year> into two before the
	// year peel below can see the year.
	fields = splitGluedYear(fields)
	// "X AKA Y" never helps an exact search; keep the first half (the primary
	// release title) - SearchVariants additionally offers the second half.
	for i, f := range fields {
		if strings.EqualFold(f, "aka") {
			fields = fields[:i]
			break
		}
	}
	// Trailing "Title.Year" glue (a name like "Z.1969" where the year was not
	// split by ParseTitle): peel a 4-digit year-like token off the tail.
	for len(fields) > 1 {
		last := fields[len(fields)-1]
		if len(last) == 4 && (last[0] == '1' || last[0] == '2') && isDigits(last) {
			fields = fields[:len(fields)-1]
			continue
		}
		break
	}
	// Trailing year-range token ("Three Colors 1993-1994" after ParseTitle
	// found no single year): drop it. The matcher searches without a hard
	// year for collections, so no range hint is needed here.
	for len(fields) > 0 {
		if _, _, ok := splitYearRange(fields[len(fields)-1]); ok {
			fields = fields[:len(fields)-1]
			continue
		}
		break
	}
	// Iterate from the tail dropping noise tokens; a noise phrase may span
	// multiple tokens ("Criterion Collection", "Collector's Edition") and be
	// followed by more noise ("... 100th Anniversary Edition CEE"), so keep
	// peeling until the tail is clean.
	for len(fields) > 0 {
		if n := matchNoiseSuffix(fields); n > 0 {
			fields = fields[:len(fields)-n]
			continue
		}
		// trailing ordinals ("100th") belong to an already-stripped
		// "100th Anniversary Edition" phrase
		if n := matchOrdinalSuffix(fields); n > 0 {
			fields = fields[:len(fields)-n]
			continue
		}
		break
	}
	return cleanTitle(strings.Join(fields, " "))
}

// ordinalRe matches bare ordinal tokens like "100th", "25th", "3rd", "1st".
var ordinalRe = regexp.MustCompile(`^[0-9]+(st|nd|rd|th)$`)

// matchOrdinalSuffix returns 1 when the trailing token is a bare ordinal
// (e.g. "100th" left behind after "Anniversary Edition" was stripped).
func matchOrdinalSuffix(fields []string) int {
	if ordinalRe.MatchString(strings.ToLower(fields[len(fields)-1])) {
		return 1
	}
	return 0
}

// SearchVariants returns the ordered list of query strings to try against a
// search API, best candidate first. The list always starts with SearchTitle;
// additional variants are appended when the title contains patterns that
// commonly defeat exact search: AKA aliases, "&" vs "and", leading articles,
// and roman-numeral / digit part suffixes.
func SearchVariants(title string) []string {
	primary := SearchTitle(title)
	if primary == "" {
		return nil
	}
	variants := []string{primary}
	seen := map[string]bool{strings.ToLower(primary): true}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		key := strings.ToLower(v)
		if !seen[key] {
			seen[key] = true
			variants = append(variants, v)
		}
	}

	// AKA split on the RAW title, because SearchTitle already truncated it at
	// "aka": "Tacones Lejanos AKA High Heels" -> both halves.
	rawFields := tokenize(normaliseSeparators(title))
	for i, f := range rawFields {
		if strings.EqualFold(f, "aka") && i > 0 && i+1 < len(rawFields) {
			add(SearchTitle(strings.Join(rawFields[:i], " ")))
			add(SearchTitle(strings.Join(rawFields[i+1:], " ")))
			break
		}
	}

	fields := tokenize(normaliseSeparators(primary))

	// "&" <-> "and": "Asako I & II" -> "Asako I and II" (tokenize keeps "&").
	if strings.Contains(primary, "&") {
		add(strings.ReplaceAll(primary, "&", "and"))
	}
	if strings.Contains(primary, " and ") {
		add(strings.ReplaceAll(primary, " and ", " & "))
	}

	// Part suffix drop: "The Godfather Part II" -> "The Godfather". The fuller
	// variant stays first; the shorter one is a fallback for releases whose
	// part token defeats search. Done before the article drop so the variants
	// stay ordered longest-to-shortest.
	if n := partSuffixLen(fields); n > 0 && len(fields) > n {
		fields = fields[:len(fields)-n]
		add(strings.Join(fields, " "))
	}

	// Leading-article drop: "The Matrix" -> "Matrix" as a last-resort variant.
	if len(fields) > 1 {
		switch strings.ToLower(fields[0]) {
		case "the", "a", "an":
			add(strings.Join(fields[1:], " "))
		}
	}

	return variants
}

// partSuffixLen returns the number of trailing tokens forming a "Part X" /
// "Part I" suffix (0 when absent): ["The","Godfather","Part","II"] -> 2.
func partSuffixLen(fields []string) int {
	n := len(fields)
	if n >= 2 && strings.EqualFold(fields[n-2], "part") {
		last := strings.ToLower(fields[n-1])
		if isRomanNumeral(last) || isDigits(last) {
			return 2
		}
	}
	return 0
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isRomanNumeral(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch r {
		case 'i', 'I', 'v', 'V', 'x', 'X':
		default:
			return false
		}
	}
	return true
}

// noisePhrases are multi-token edition/brand/region suffixes stripped by
// SearchTitle, longest-first so "Collector's Edition" matches before
// "Collector's".
var noisePhrases = [][]string{
	{"criterion", "collection"},
	{"masters", "of", "cinema"},
	{"collector's", "edition"},
	{"collectors", "edition"},
	{"extended", "edition"},
	{"extended", "cut"},
	{"extended", "collector's", "edition"},
	{"director's", "cut"},
	{"directors", "cut"},
	{"dircut"},
	{"anniversary", "edition"},
	{"special", "edition"},
	{"ultimate", "edition"},
	{"imax", "edition"},
	{"100th", "anniversary"},
	{"100th", "anniversary", "edition"},
}

// noiseTokens are single-token edition/brand/region/source words stripped from
// the tail of a title by SearchTitle.
var noiseTokens = map[string]bool{
	// edition words
	"extended": true, "edition": true, "collector's": true, "collectors": true,
	"uncut": true, "remastered": true, "repack": true, "proper": true,
	"remux": true, "imax": true, "hybrid": true, "internal": true,
	"anniversary": true, "dircut": true, "dc": true, "ee": true,
	"criterion": true, "cc": true, "moc": true, "rm": true,
	"theatrical": true, "final": true, "complete": true,
	// media / quality words
	"uhd": true, "remuxed": true,
	// region tags
	"cee": true, "fra": true, "fre": true, "ger": true, "jpn": true, "kor": true,
	"hk": true, "tw": true, "usa": true, "us": true, "uk": true, "eur": true,
	"gbr": true, "rus": true, "esp": true, "ita": true, "nordic": true,
	// "aka" alone (handled more thoroughly in SearchVariants)
	"aka": true,
}

// splitGluedYear splits tokens like "Z.1969" (kept whole by tokenize because
// the dot sits between a letter and a digit) into "Z" and "1969" so the year
// peel in SearchTitle can strip the year.
func splitGluedYear(fields []string) []string {
	out := make([]string, 0, len(fields)+2)
	for _, f := range fields {
		idx := strings.LastIndex(f, ".")
		if idx > 0 && len(f)-idx-1 == 4 && isDigits(f[idx+1:]) {
			head := f[:idx]
			year := f[idx+1:]
			if year[0] == '1' || year[0] == '2' {
				out = append(out, head, year)
				continue
			}
		}
		out = append(out, f)
	}
	return out
}

// matchNoiseSuffix returns how many trailing tokens of fields form a noise
// phrase or token; 0 when the tail is clean.
func matchNoiseSuffix(fields []string) int {
	for _, phrase := range noisePhrases {
		n := len(phrase)
		if n > len(fields) {
			continue
		}
		tail := fields[len(fields)-n:]
		ok := true
		for i, p := range phrase {
			if !strings.EqualFold(tail[i], p) {
				ok = false
				break
			}
		}
		if ok {
			return n
		}
	}
	if noiseTokens[strings.ToLower(fields[len(fields)-1])] {
		return 1
	}
	return 0
}
