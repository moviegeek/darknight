package parser

import (
	"testing"
)

// TestParseTitle_RealSamples exercises the parser against release names taken
// directly from the media.txt library tree. Each case documents the expected
// decomposition; when the parser drifts these tests fail loudly.
func TestParseTitle_RealSamples(t *testing.T) {
	cases := []struct {
		name string
		want FileMeta
	}{
		// --- baseline BluRay, 1080p, x264 ---
		{
			"12.12.The.Day.2023.1080p.BluRay.DD+5.1.x265-PuTao",
			FileMeta{Title: "12 12 The Day", Year: 2023, Source: BluRay, Resolution: Res1080p,
				VideoCodec: X265, AudioCodec: DDP, AudioChannels: "5.1", ReleaseGroup: "PuTao"},
		},
		{
			"1917.2019.1080p.BluRay.x264.Atmos.TrueHD7.1-HDChina",
			FileMeta{Title: "1917", Year: 2019, Source: BluRay, Resolution: Res1080p,
				VideoCodec: X264, AudioCodec: TrueHD, AudioChannels: "7.1", ReleaseGroup: "HDChina"},
		},
		// "DTS-HD.MA.5.1" spans tokens; channels glued on "MA5.1"
		{
			"2001.A.Space.Odyssey.1968.BluRay.1080p.x264.DTS-HD.MA.5.1-HDChina",
			FileMeta{Title: "2001 A Space Odyssey", Year: 1968, Source: BluRay, Resolution: Res1080p,
				VideoCodec: X264, AudioCodec: DTSHDMA, AudioChannels: "5.1", ReleaseGroup: "HDChina"},
		},
		// "DD-EX5.1" — odd DD-EX variant; channels glued.
		{
			"3.10.to.Yuma.2007.720p.BluRay.DD-EX5.1.x264-EbP",
			FileMeta{Title: "3 10 to Yuma", Year: 2007, Source: BluRay, Resolution: Res720p,
				VideoCodec: X264, AudioChannels: "5.1", ReleaseGroup: "EbP"},
		},
		// 720p, simple DTS, no fancy channels.
		{
			"Akira.1988.720p.BluRay.DD+5.1.x264-DON",
			FileMeta{Title: "Akira", Year: 1988, Source: BluRay, Resolution: Res720p,
				VideoCodec: X264, AudioCodec: DDP, AudioChannels: "5.1", ReleaseGroup: "DON"},
		},
		// "FLAC1.0" — channels glued onto FLAC.
		{
			"A.Brighter.Summer.Day.1991.Criterion.Collection.1080p.BluRay.x264.FLAC1.0-HDChina",
			FileMeta{Title: "A Brighter Summer Day", Year: 1991, Source: BluRay, Resolution: Res1080p,
				VideoCodec: X264, AudioCodec: FLAC, AudioChannels: "1.0", Edition: "Criterion",
				ReleaseGroup: "HDChina"},
		},
		// 10bit + x265, DTS-HD MA 5.1
		{
			"A.Dirty.Carnival.2006.BluRay.1080p.x265.10bit.DTS-HD.MA.5.1-HDChina",
			FileMeta{Title: "A Dirty Carnival", Year: 2006, Source: BluRay, Resolution: Res1080p,
				VideoCodec: X265, AudioCodec: DTSHDMA, AudioChannels: "5.1", BitDepth: 10,
				ReleaseGroup: "HDChina"},
		},
		// Criterion Collection + "CC" alias
		{
			"4.Months,3.Weeks.and.2.Days.2007.CC.BluRay.1080p.x264.DTS-CMCT",
			FileMeta{Title: "4 Months 3 Weeks and 2 Days", Year: 2007, Source: BluRay,
				Resolution: Res1080p, VideoCodec: X264, AudioCodec: DTS, Edition: "Criterion Collection",
				ReleaseGroup: "CMCT"},
		},
		// user@group
		{
			"A.Beautiful.Mind.2001.1080p.Blu-ray.DTS-HD.MA5.1.x264-HiS@beAst",
			FileMeta{Title: "A Beautiful Mind", Year: 2001, Source: BluRay, Resolution: Res1080p,
				VideoCodec: X264, AudioCodec: DTSHDMA, AudioChannels: "5.1", ReleaseGroup: "beAst"},
		},
		// Atmos + TrueHD ordering (Atmos must not overwrite TrueHD)
		{
			"All.Quiet.on.the.Western.Front.2022.BluRay.1080p.x264.Atmos.TrueHD7.1-HDChina",
			FileMeta{Title: "All Quiet on the Western Front", Year: 2022, Source: BluRay,
				Resolution: Res1080p, VideoCodec: X264, AudioCodec: TrueHD, AudioChannels: "7.1",
				ReleaseGroup: "HDChina"},
		},
		// 3Audio — multi audio count
		{
			"A.Chinese.Odyssey.Part.I.II.1994.BluRay.1080p.DTS-HD.MA.5.1.3Audio.x264-HDWinG",
			FileMeta{Title: "A Chinese Odyssey Part I II", Year: 1994, Source: BluRay,
				Resolution: Res1080p, VideoCodec: X264, AudioCodec: DTSHDMA, AudioChannels: "5.1",
				AudioCount: 3, ReleaseGroup: "HDWinG"},
		},
		// regional: KOR
		{
			"All.for.the.Winner.1990.KOR.BluRay.1080p.x264.DTS.2Audios-CMCT",
			FileMeta{Title: "All for the Winner", Year: 1990, Source: BluRay, Resolution: Res1080p,
				VideoCodec: X264, AudioCodec: DTS, AudioCount: 2, Language: "KOR", ReleaseGroup: "CMCT"},
		},
		// 100th Anniversary Edition (multi-token)
		{
			"All.Quiet.on.the.Western.Front.1930.100th.Anniversary.Edition.CEE.BluRay.720p.x264.AC3-HDWinG",
			FileMeta{Title: "All Quiet on the Western Front", Year: 1930, Source: BluRay,
				Resolution: Res720p, VideoCodec: X264, AudioCodec: AC3, Edition: "100th Anniversary Edition",
				Language: "CEE", ReleaseGroup: "HDWinG"},
		},
		// DC. single-token edition
		{
			"Amadeus.1984.DC.1080p.BluRay.x264.DTS-FGT",
			FileMeta{Title: "Amadeus", Year: 1984, Source: BluRay, Resolution: Res1080p,
				VideoCodec: X264, AudioCodec: DTS, Edition: "Director's Cut", ReleaseGroup: "FGT"},
		},
		// REMASTERED
		{
			"A.River.Runs.Through.It.1992.REMASTERED.BluRay.720p.x264.DTS-HDChina",
			FileMeta{Title: "A River Runs Through It", Year: 1992, Source: BluRay, Resolution: Res720p,
				VideoCodec: X264, AudioCodec: DTS, Edition: "Remastered", ReleaseGroup: "HDChina"},
		},
		// Repack + Blu-ray
		{
			"Apocalypto.2006.Repack.Blu-Ray.1080p.DTS-HD.MA.5.1.x264-beAst",
			FileMeta{Title: "Apocalypto", Year: 2006, Source: BluRay, Resolution: Res1080p,
				VideoCodec: X264, AudioCodec: DTSHDMA, AudioChannels: "5.1", Edition: "Repack",
				ReleaseGroup: "beAst"},
		},
		// AAC2.0 glued
		{
			"A.Man.and.a.Woman.1966.720p.BluRay.AAC2.0.x264-DON",
			FileMeta{Title: "A Man and a Woman", Year: 1966, Source: BluRay, Resolution: Res720p,
				VideoCodec: X264, AudioCodec: AAC, AudioChannels: "2.0", ReleaseGroup: "DON"},
		},
		// Year range collection: "1979-1997"
		{
			"Alien.Anthology.1979-1997.BluRay.1080p.x264.DTS-HD.MA.5.1-HDChina",
			FileMeta{Title: "Alien Anthology", Source: BluRay, Resolution: Res1080p,
				VideoCodec: X264, AudioCodec: DTSHDMA, AudioChannels: "5.1", IsCollection: true,
				ReleaseGroup: "HDChina"},
		},
		// --- 4K / HDR / DoVi cluster ---
		{
			"Barbie.2023.Repack.UHD.BluRay.2160p.10bit.DoVi.2Audio.TrueHD(Atmos).7.1.x265-beAst",
			FileMeta{Title: "Barbie", Year: 2023, Source: UHDBluRay, Resolution: Res2160p,
				VideoCodec: X265, AudioCodec: TrueHD, AudioChannels: "7.1", AudioCount: 2,
				HDR: DolbyVision, BitDepth: 10, Edition: "Repack", ReleaseGroup: "beAst"},
		},
		{
			"Big.Fish.2003.2160p.UHD.BluRay.TrueHD.Atmos.7.1.HDR.x265-PuTao",
			FileMeta{Title: "Big Fish", Year: 2003, Source: UHDBluRay, Resolution: Res2160p,
				VideoCodec: X265, AudioCodec: TrueHD, AudioChannels: "7.1", HDR: HDR10,
				ReleaseGroup: "PuTao"},
		},
		{
			"Departures.2008.REPACK.2160p.UHD.BluRay.DTS-HD.MA.5.1.DV.HDR.x265-PuTao",
			FileMeta{Title: "Departures", Year: 2008, Source: UHDBluRay, Resolution: Res2160p,
				VideoCodec: X265, AudioCodec: DTSHDMA, AudioChannels: "5.1", HDR: DolbyVision,
				Edition: "Repack", ReleaseGroup: "PuTao"},
		},
		// DV before HDR -> DV wins (last HDR flag set)
		{
			"Casino.1995.UHD.BluRay.2160p.10bit.HDR.DTS-X.7.1.x265-beAst",
			FileMeta{Title: "Casino", Year: 1995, Source: UHDBluRay, Resolution: Res2160p,
				VideoCodec: X265, AudioCodec: DTSX, AudioChannels: "7.1", HDR: HDR10, BitDepth: 10,
				ReleaseGroup: "beAst"},
		},
		// --- collection / regional ---
		{
			"All.the.President's.Men.1976.JPN.BluRay.1080p.DTS-HD.MA.2.0.x264-Fallout",
			FileMeta{Title: "All the President's Men", Year: 1976, Source: BluRay, Resolution: Res1080p,
				VideoCodec: X264, AudioCodec: DTSHDMA, AudioChannels: "2.0", Language: "JPN",
				ReleaseGroup: "Fallout"},
		},
		// Extended Cut + UHD
		{
			"Gladiator.2000.REPACK.Extended.Cut.1080p.UHD.BluRay.DD+7.1.HDR.x265-DON",
			FileMeta{Title: "Gladiator", Year: 2000, Source: UHDBluRay, Resolution: Res1080p,
				VideoCodec: X265, AudioCodec: DDP, AudioChannels: "7.1", HDR: HDR10,
				Edition: "Extended Cut", ReleaseGroup: "DON"},
		},
		// IMAX Edition
		{
			"Interstellar.2014.IMAX.Edition.Blu-ray.CEE.1080p.AVC.DTS-HD.MA5.1-HDclub",
			FileMeta{Title: "Interstellar", Year: 2014, Source: BluRay, Resolution: Res1080p,
				VideoCodec: AVC, AudioCodec: DTSHDMA, AudioChannels: "5.1", Edition: "IMAX Edition",
				Language: "CEE", ReleaseGroup: "HDclub"},
		},
		// 4K Remastered (title-internal "4K" must NOT be treated as resolution).
		// Here "2160p" is absent, so resolution stays unknown and the "4K" in
		// "4K.Remastered" should be ignored.
		{
			"Crouching.Tiger.Hidden.Dragon.2000.4K.Remastered.1080p.BluRay.x264.Atmos.TrueHD.7.1-HDChina",
			FileMeta{Title: "Crouching Tiger Hidden Dragon", Year: 2000, Source: BluRay,
				Resolution: Res1080p, VideoCodec: X264, AudioCodec: TrueHD, AudioChannels: "7.1",
				Edition: "Remastered", ReleaseGroup: "HDChina"},
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got := ParseTitle(c.name)
			if !fileMetaEq(got, c.want) {
				t.Errorf("\n  input : %s\n  want  : %+v\n  got   : %+v", c.name, c.want, got)
			}
		})
	}
}

// fileMetaEq compares the user-visible fields. ExtraFiles is informational and
// not compared.
func fileMetaEq(a, b FileMeta) bool {
	return a.Title == b.Title &&
		a.Year == b.Year &&
		a.Source == b.Source &&
		a.Resolution == b.Resolution &&
		a.VideoCodec == b.VideoCodec &&
		a.AudioCodec == b.AudioCodec &&
		a.AudioChannels == b.AudioChannels &&
		a.HDR == b.HDR &&
		a.BitDepth == b.BitDepth &&
		a.Edition == b.Edition &&
		a.AudioCount == b.AudioCount &&
		a.ReleaseGroup == b.ReleaseGroup &&
		a.IsCollection == b.IsCollection &&
		a.Language == b.Language
}

func TestMinNonNegative(t *testing.T) {
	cases := []struct {
		in   []int
		want int
	}{
		{[]int{1, 2, 3}, 1},
		{[]int{-1, -1, -1}, -1},
		{[]int{-1, 0, 1}, 0},
		{[]int{-1, 3, 4}, 3},
		{[]int{1}, 1},
		{[]int{-1}, -1},
		{nil, -1},
	}
	for _, c := range cases {
		if got := minNonNegative(c.in...); got != c.want {
			t.Errorf("minNonNegative(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestExtractChannels(t *testing.T) {
	cases := map[string]string{
		"truehd7.1": "7.1",
		"dd+5.1":    "5.1",
		"aac2.0":    "2.0",
		"flac1.0":   "1.0",
		"1080p":     "", // numeric prefix must not match
		"2023":      "",
		"ma5.1":     "5.1",
	}
	for in, want := range cases {
		if got := extractChannels(in); got != want {
			t.Errorf("extractChannels(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitYearRange(t *testing.T) {
	cases := map[string]struct {
		a, b int
		ok   bool
	}{
		"1979-1997": {1979, 1997, true},
		"1979":      {0, 0, false},
		"foo-bar":   {0, 0, false},
		"1910-2099": {1910, 2099, true},
		"1900-2000": {0, 0, false}, // 1900 out of range
	}
	for in, want := range cases {
		a, b, ok := splitYearRange(in)
		if a != want.a || b != want.b || ok != want.ok {
			t.Errorf("splitYearRange(%q) = (%d,%d,%v), want (%d,%d,%v)",
				in, a, b, ok, want.a, want.b, want.ok)
		}
	}
}
