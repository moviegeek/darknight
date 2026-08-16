package store

import (
	"strings"
	"unicode"
)

// MatchKey folds a title into a comparison key used to reattach a scanned
// release to an existing movie row without a TMDB round-trip.
//
// Release names and TMDB titles differ in ways that carry no meaning:
//
//	"As Good As It Gets"  vs "As Good as It Gets"      (title-case vs sentence-case)
//	"Fantastic Mr Fox"    vs "Fantastic Mr. Fox"       (dropped abbreviation dots)
//	"Czlowiek z marmuru"  vs "Człowiek z marmuru"      (stripped diacritics)
//	"Le Samourai"         vs "Le samouraï"             (both of the above)
//	"Jeanne Dielman 23 …" vs "Jeanne Dielman, 23, …"   (dropped commas)
//
// The key lowercases, folds accented Latin letters to their base form, and
// removes everything that is not a letter or digit. CJK / Hangul / Kana are
// kept verbatim so original-language titles still compare exactly.
//
// The key is intentionally lossy: it is a *candidate* finder, always paired
// with a year check or a TMDB confirmation by the caller.
func MatchKey(title string) string {
	if title == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(title))
	for _, r := range strings.ToLower(title) {
		if base, ok := latinFolds[r]; ok {
			b.WriteString(base)
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
		// everything else (spaces, punctuation, symbols) is dropped
	}
	return b.String()
}

// latinFolds maps accented / extended Latin letters to their ASCII base. Go's
// stdlib has no Unicode normalisation, and pulling in golang.org/x/text for one
// lookup is not worth a new dependency, so the Latin-1 Supplement plus the
// Latin Extended-A range used by European film titles are tabulated here.
// Values are strings because a few letters expand ("æ" -> "ae").
var latinFolds = map[rune]string{
	// a
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'ā': "a",
	'ă': "a", 'ą': "a", 'ǎ': "a", 'ȧ': "a", 'ạ': "a", 'ả': "a", 'ấ': "a",
	'ầ': "a", 'ẩ': "a", 'ẫ': "a", 'ậ': "a", 'ắ': "a", 'ằ': "a", 'ẳ': "a",
	'ẵ': "a", 'ặ': "a",
	'æ': "ae",
	// c
	'ç': "c", 'ć': "c", 'ĉ': "c", 'ċ': "c", 'č': "c",
	// d
	'ď': "d", 'đ': "d", 'ð': "d",
	// e
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ē': "e", 'ĕ': "e", 'ė': "e",
	'ę': "e", 'ě': "e", 'ẹ': "e", 'ẻ': "e", 'ẽ': "e", 'ế': "e", 'ề': "e",
	'ể': "e", 'ễ': "e", 'ệ': "e",
	// g
	'ĝ': "g", 'ğ': "g", 'ġ': "g", 'ģ': "g",
	// h
	'ĥ': "h", 'ħ': "h",
	// i
	'ì': "i", 'í': "i", 'î': "i", 'ï': "i", 'ĩ': "i", 'ī': "i", 'ĭ': "i",
	'į': "i", 'ı': "i", 'ỉ': "i", 'ị': "i",
	// j / k / l
	'ĵ': "j", 'ķ': "k",
	'ĺ': "l", 'ļ': "l", 'ľ': "l", 'ł': "l",
	// n
	'ñ': "n", 'ń': "n", 'ņ': "n", 'ň': "n", 'ŋ': "n",
	// o
	'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o", 'ō': "o",
	'ŏ': "o", 'ő': "o", 'ǒ': "o", 'ọ': "o", 'ỏ': "o", 'ố': "o", 'ồ': "o",
	'ổ': "o", 'ỗ': "o", 'ộ': "o", 'ớ': "o", 'ờ': "o", 'ở': "o", 'ỡ': "o",
	'ợ': "o",
	'œ': "oe",
	// r
	'ŕ': "r", 'ŗ': "r", 'ř': "r",
	// s
	'ś': "s", 'ŝ': "s", 'ş': "s", 'š': "s", 'ș': "s", 'ß': "ss",
	// t
	'ţ': "t", 'ť': "t", 'ŧ': "t", 'ț': "t", 'þ': "t",
	// u
	'ù': "u", 'ú': "u", 'û': "u", 'ü': "u", 'ũ': "u", 'ū': "u", 'ŭ': "u",
	'ů': "u", 'ű': "u", 'ų': "u", 'ǔ': "u", 'ụ': "u", 'ủ': "u", 'ứ': "u",
	'ừ': "u", 'ử': "u", 'ữ': "u", 'ự': "u",
	// w / y / z
	'ŵ': "w",
	'ý': "y", 'ÿ': "y", 'ŷ': "y", 'ỳ': "y", 'ỵ': "y", 'ỷ': "y", 'ỹ': "y",
	'ź': "z", 'ż': "z", 'ž': "z", 'ƶ': "z",
}
