package store_test

import (
	"testing"

	"github.com/moviegeek/darknight/internal/store"
)

func TestMatchKey(t *testing.T) {
	// pairs that MUST fold to the same key (real cases from the library)
	same := [][2]string{
		{"As Good As It Gets", "As Good as It Gets"},
		{"Crows And Sparrows", "Crows and Sparrows"},
		{"Fantastic Mr Fox", "Fantastic Mr. Fox"},
		{"E T The Extra-Terrestrial", "E.T. the Extra-Terrestrial"},
		{"Czlowiek z marmuru", "Człowiek z marmuru"},
		{"Le Samourai", "Le samouraï"},
		{"Anatomie d'Une Chute", "Anatomie d'une chute"},
		{"Jeanne Dielman 23 Quai du Commerce 1080 Bruxelles",
			"Jeanne Dielman, 23, quai du Commerce, 1080 Bruxelles"},
		{"This Is Not a Burial It's a Resurrection", "This Is Not a Burial, It's a Resurrection"},
		{"Eo", "EO"},
		{"Amelie Poulain", "Amélie Poulain"},
		{"Atame", "¡Átame!"},
	}
	for _, p := range same {
		if a, b := store.MatchKey(p[0]), store.MatchKey(p[1]); a != b {
			t.Errorf("MatchKey(%q)=%q != MatchKey(%q)=%q", p[0], a, p[1], b)
		}
	}

	// pairs that must stay DISTINCT (different films)
	diff := [][2]string{
		{"Alien", "Aliens"},
		{"The Godfather", "The Godfather Part II"},
		{"Living", "Ikiru"},
		{"Three Colors Blue", "Three Colors White"},
	}
	for _, p := range diff {
		if a, b := store.MatchKey(p[0]), store.MatchKey(p[1]); a == b {
			t.Errorf("MatchKey collapsed distinct titles %q / %q -> %q", p[0], p[1], a)
		}
	}

	// CJK titles pass through untouched (minus punctuation)
	if got := store.MatchKey("無間道III: 終極無間"); got != "無間道iii終極無間" {
		t.Errorf("CJK key = %q", got)
	}
	if store.MatchKey("") != "" {
		t.Error("empty title must yield empty key")
	}
}
