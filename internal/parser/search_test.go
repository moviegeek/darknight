package parser

import "testing"

func TestSearchTitle(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Avatar Extended Collector's Edition", "Avatar"},
		{"JFK DirCut", "JFK"},
		{"The Ballad of Narayama MOC", "The Ballad of Narayama"},
		{"Three Colors GBR UHD", "Three Colors"},
		{"Tacones Lejanos AKA High Heels", "Tacones Lejanos"}, // aka handled in variants
		{"The Lord of the Rings The Two Towers Extended Edition", "The Lord of the Rings The Two Towers"},
		{"A Touch of Zen Masters of Cinema", "A Touch of Zen"},
		{"12 Angry Men Criterion Collection", "12 Angry Men"},
		{"All Quiet on the Western Front 100th Anniversary Edition CEE", "All Quiet on the Western Front"},
		{"In Bruges FRE", "In Bruges"},
		{"The Last Emperor CC UHD", "The Last Emperor"},
		{"Takeshis'", "Takeshis'"},
		{"Átame!", "Átame!"},
		{"Amélie Poulain", "Amélie Poulain"},
		{"1917", "1917"},
		{"", ""},
		// guard: real title words that must NOT be stripped
		{"The Final Countdown", "The Final Countdown"}, // "Final" not at tail
		{"American History X", "American History X"},
		{"The Complete Metropolis", "The Complete Metropolis"}, // real title word kept
		{"Three Colors 1993-1994", "Three Colors"},
		{"Z.1969", "Z"}, // glued letter.year token is split
	}
	for _, c := range cases {
		if got := SearchTitle(c.in); got != c.want {
			t.Errorf("SearchTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSearchVariants(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"Avatar Extended Collector's Edition", []string{"Avatar"}},
		{"Tacones Lejanos AKA High Heels", []string{"Tacones Lejanos", "High Heels"}},
		{"Asako I & II", []string{"Asako I & II", "Asako I and II"}},
		{"The Matrix", []string{"The Matrix", "Matrix"}},
		{"The Godfather Part II", []string{"The Godfather Part II", "The Godfather", "Godfather"}},
		{"Z", []string{"Z"}},
	}
	for _, c := range cases {
		got := SearchVariants(c.in)
		if len(got) != len(c.want) {
			t.Errorf("SearchVariants(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("SearchVariants(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}
