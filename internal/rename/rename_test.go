package rename

import (
	"path/filepath"
	"testing"
)

func TestTitleYear(t *testing.T) {
	cases := []struct {
		old, title string
		year       int
		want       string
	}{
		// wrong title + right year: only the title segment is rebuilt
		{"Grean.Snake.1993.BluRay.1080p.x264.DTS-HD.MA5.1-HDChina", "Green Snake", 1993,
			"Green.Snake.1993.BluRay.1080p.x264.DTS-HD.MA5.1-HDChina"},
		// wrong year too
		{"Spirited.Away.2011.BluRay.1080p.x264.DTS.AC3.4Audio-HDWinG",
			"Spirited Away", 2001,
			"Spirited.Away.2001.BluRay.1080p.x264.DTS.AC3.4Audio-HDWinG"},
		// punctuation in the title collapses to dots
		{"Tacones.Lejanos.AKA.High.Heels.1991.RM.FRA.BluRay.1080p.DTS-HD.MA.2.0.x264-Fallout",
			"Tacones lejanos", 1991,
			"Tacones.lejanos.1991.RM.FRA.BluRay.1080p.DTS-HD.MA.2.0.x264-Fallout"},
		// ampersand -> and
		{"Thelma&Louise.1991.BluRay.1080p.x264-DON", "Thelma & Louise", 1991,
			"Thelma.and.Louise.1991.BluRay.1080p.x264-DON"},
		// no year in the old name
		{"Kaidan.BluRay.720p.x264", "Kwaidan", 1964,
			"Kwaidan.1964.BluRay.720p.x264"},
		// original-language title with colon
		{"Doro.no.kawa.1981.1080p.WEB-DL.DD+2.0.H.264-SbR", "泥の河", 1981,
			"泥の河.1981.1080p.WEB-DL.DD+2.0.H.264-SbR"},
		// bare title, nothing technical
		{"Warui.yatsu.hodo.yoku.nemuru", "The Bad Sleep Well", 1960,
			"The.Bad.Sleep.Well.1960"},
		// already canonical -> unchanged
		{"Heat.1995.BluRay.1080p.x264-GROUP", "Heat", 1995,
			"Heat.1995.BluRay.1080p.x264-GROUP"},
		// hyphens and apostrophes in the title are kept verbatim
		{"Bakumatsu.Taiyo.Den.1957.1080p.BluRay.x264", "A Sun-Tribe Myth from the Bakumatsu Era", 1957,
			"A.Sun-Tribe.Myth.from.the.Bakumatsu.Era.1957.1080p.BluRay.x264"},
		{"Coup.d.Etat.1973.1080p.BluRay.x264", "Coup d'Etat", 1973,
			"Coup.d'Etat.1973.1080p.BluRay.x264"},
	}
	for _, c := range cases {
		if got := TitleYear(c.old, c.title, c.year); got != c.want {
			t.Errorf("TitleYear(%q, %q, %d)\n  got  %q\n  want %q", c.old, c.title, c.year, got, c.want)
		}
	}
}

func TestBuild(t *testing.T) {
	dir := "/mnt/Media/Movie/Grean.Snake.1993.BluRay.1080p.x264.DTS-HD.MA5.1-HDChina"
	main := "Grean.Snake.1993.BluRay.1080p.x264.DTS-HD.MA5.1-HDChina.mkv"
	siblings := []string{
		main,
		"Grean.Snake.1993.BluRay.1080p.x264.DTS-HD.MA5.1-HDChina.chi.srt",
		"Grean.Snake.1993.BluRay.1080p.x264.DTS-HD.MA5.1-HDChina.nfo",
	}
	p := Build(dir, main, siblings, "Green Snake", 1993)
	if p.DirNew != "Green.Snake.1993.BluRay.1080p.x264.DTS-HD.MA5.1-HDChina" {
		t.Fatalf("DirNew = %q", p.DirNew)
	}
	if len(p.Moves) != 3 {
		t.Fatalf("expected 3 moves (video+sub+nfo), got %d: %+v", len(p.Moves), p.Moves)
	}
	// video move first
	if p.Moves[0].Kind != "video" {
		t.Errorf("first move should be the video, got %q", p.Moves[0].Kind)
	}
	wantSub := filepath.Join(dir, "Green.Snake.1993.BluRay.1080p.x264.DTS-HD.MA5.1-HDChina.chi.srt")
	if p.Moves[1].To != wantSub {
		t.Errorf("sub rename: got %q want %q", p.Moves[1].To, wantSub)
	}
	// already-canonical release yields an empty plan
	p2 := Build("/m/Heat.1995.BluRay.1080p.x264-G", "Heat.1995.BluRay.1080p.x264-G.mkv",
		[]string{"Heat.1995.BluRay.1080p.x264-G.mkv"}, "Heat", 1995)
	if len(p2.Moves) != 0 {
		t.Errorf("canonical release should produce no moves, got %+v", p2.Moves)
	}
}
