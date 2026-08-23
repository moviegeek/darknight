package subtitle

import (
	"reflect"
	"testing"
)

const video = "Z.1969.1080p.BluRay.x264-HANDJOB.mkv"
const base = "Z.1969.1080p.BluRay.x264-HANDJOB"

func TestAllocate(t *testing.T) {
	cases := []struct {
		name       string
		existing   []string
		uploads    []Upload
		renames    []Rename // expected From->To of existing files
		finalNames []string // expected upload final names, sorted
	}{
		{
			name:       "clean release, single upload takes unversioned slot",
			existing:   []string{video, base + ".nfo"},
			uploads:    []Upload{{Filename: "my.ass", Lang: "chi"}},
			finalNames: []string{base + ".chi.ass"},
		},
		{
			name:     "clean release, two same-pair uploads all versioned",
			existing: []string{video},
			uploads: []Upload{
				{Filename: "a.srt", Lang: "chi"},
				{Filename: "b.srt", Lang: "chi"},
			},
			finalNames: []string{base + ".ver1.chi.srt", base + ".ver2.chi.srt"},
		},
		{
			// the reported bug: an existing chi.srt used to keep the
			// unversioned slot; it must vacate to ver1 and the upload takes
			// the higher number
			name:     "existing unversioned chi.srt + one upload",
			existing: []string{video, base + ".chi.srt"},
			uploads:  []Upload{{Filename: "new.srt", Lang: "chi"}},
			renames: []Rename{
				{From: base + ".chi.srt", To: base + ".ver1.chi.srt"},
			},
			finalNames: []string{base + ".ver2.chi.srt"},
		},
		{
			name:     "existing unversioned chi.srt + two uploads",
			existing: []string{video, base + ".chi.srt"},
			uploads: []Upload{
				{Filename: "a.srt", Lang: "chi"},
				{Filename: "b.srt", Lang: "chi"},
			},
			renames: []Rename{
				{From: base + ".chi.srt", To: base + ".ver1.chi.srt"},
			},
			finalNames: []string{base + ".ver2.chi.srt", base + ".ver3.chi.srt"},
		},
		{
			name:       "existing ver1+ver2, upload continues after the highest",
			existing:   []string{video, base + ".ver1.chi.srt", base + ".ver2.chi.srt"},
			uploads:    []Upload{{Filename: "c.srt", Lang: "chi"}},
			finalNames: []string{base + ".ver3.chi.srt"},
		},
		{
			// chi.srt + ver1.chi.srt mixed state (the old buggy layout):
			// the unversioned file joins the versioned run, upload follows
			name:     "existing unversioned + ver1, upload takes ver3 after vacate",
			existing: []string{video, base + ".chi.srt", base + ".ver1.chi.srt"},
			uploads:  []Upload{{Filename: "d.srt", Lang: "chi"}},
			renames: []Rename{
				{From: base + ".chi.srt", To: base + ".ver2.chi.srt"},
			},
			finalNames: []string{base + ".ver3.chi.srt"},
		},
		{
			name:       "different ext never versions across pairs",
			existing:   []string{video, base + ".chi.srt"},
			uploads:    []Upload{{Filename: "new.ass", Lang: "chi"}},
			finalNames: []string{base + ".chi.ass"},
		},
		{
			name:       "different languages same ext coexist unversioned",
			existing:   []string{video, base + ".chi.srt"},
			uploads:    []Upload{{Filename: "en.srt", Lang: "eng"}},
			finalNames: []string{base + ".eng.srt"},
		},
		{
			name:     "mixed batch: each pair independent",
			existing: []string{video, base + ".chi.ass"},
			uploads: []Upload{
				{Filename: "a.srt", Lang: "chi"},
				{Filename: "b.srt", Lang: "chi"},
				{Filename: "c.ass", Lang: "chi"},
			},
			renames: []Rename{
				{From: base + ".chi.ass", To: base + ".ver1.chi.ass"},
			},
			finalNames: []string{
				base + ".ver1.chi.srt",
				base + ".ver2.chi.ass",
				base + ".ver2.chi.srt",
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			alloc := Allocate(video, c.uploads, c.existing)
			if got := SortedFilenames(alloc.Plans); !reflect.DeepEqual(got, c.finalNames) {
				t.Errorf("upload names\n  got  %v\n  want %v", got, c.finalNames)
			}
			if !reflect.DeepEqual(alloc.Renames, c.renames) {
				t.Errorf("renames\n  got  %+v\n  want %+v", alloc.Renames, c.renames)
			}
		})
	}
}

func TestLangCode(t *testing.T) {
	if LangCode("中文") != "chi" || LangCode("JPN") != "jpn" {
		t.Errorf("LangCode resolution broken")
	}
	if LangCode("martian") != "und" {
		t.Errorf("unknown lang should fall back to und, got %q", LangCode("martian"))
	}
}
