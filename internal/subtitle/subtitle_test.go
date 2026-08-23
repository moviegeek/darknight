package subtitle

import (
	"reflect"
	"testing"
)

func TestNameBatch(t *testing.T) {
	video := "Z.1969.1080p.BluRay.x264-HANDJOB.mkv"
	base := "Z.1969.1080p.BluRay.x264-HANDJOB"

	t.Run("simple upload", func(t *testing.T) {
		existing := map[string]bool{video: true, base + ".nfo": true}
		plans := NameBatch(video, []Upload{{Filename: "my-chinese.ass", Lang: "chi"}}, existing)
		if len(plans) != 1 || plans[0].FinalName != base+".chi.ass" || plans[0].Version != 0 {
			t.Fatalf("got %+v", plans)
		}
	})

	t.Run("collide with existing same lang+ext -> ver1", func(t *testing.T) {
		existing := map[string]bool{
			video:            true,
			base + ".chi.ass": true,
		}
		plans := NameBatch(video, []Upload{{Filename: "another.ass", Lang: "chi"}}, existing)
		if plans[0].FinalName != base+".ver1.chi.ass" || plans[0].Version != 1 {
			t.Fatalf("got %+v", plans)
		}
	})

	t.Run("different ext never versions", func(t *testing.T) {
		existing := map[string]bool{
			video:            true,
			base + ".chi.srt": true,
		}
		plans := NameBatch(video, []Upload{{Filename: "new.ass", Lang: "chi"}}, existing)
		if plans[0].FinalName != base+".chi.ass" {
			t.Fatalf("got %+v", plans)
		}
	})

	t.Run("two same lang+ext in one batch -> ALL versioned ver1, ver2", func(t *testing.T) {
		// the unversioned slot is reserved for a single unambiguous upload;
		// a same-(lang,ext) group inside one batch cannot decide which file
		// deserves it, so every member is versioned
		existing := map[string]bool{video: true}
		plans := NameBatch(video, []Upload{
			{Filename: "a.srt", Lang: "chi"},
			{Filename: "b.srt", Lang: "chi"},
		}, existing)
		got := SortedFilenames(plans)
		want := []string{base + ".ver1.chi.srt", base + ".ver2.chi.srt"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
	})

	t.Run("user case: Bad Sleep Well, two chs srt uploads, clean release", func(t *testing.T) {
		v := "The.Bad.Sleep.Well.1960.720p.BluRay.FLAC.x264-EA.mkv"
		b := "The.Bad.Sleep.Well.1960.720p.BluRay.FLAC.x264-EA"
		existing := map[string]bool{v: true, b + ".nfo": true}
		plans := NameBatch(v, []Upload{
			{Filename: "[zmk.pw]The.Bad.Sleep.Well.1960.1080p.BluRay.x264.chs.utf8.srt", Lang: "chi"},
			{Filename: "[zmk.pw]The.Bad.Sleep.Well.1960.BluRay.720p.x264.AC3-MySiLU.utf8.srt", Lang: "chi"},
		}, existing)
		got := SortedFilenames(plans)
		want := []string{b + ".ver1.chi.srt", b + ".ver2.chi.srt"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
	})

	t.Run("existing chi.srt + ver1, then upload two more -> ver2, ver3", func(t *testing.T) {
		existing := map[string]bool{
			video:              true,
			base + ".chi.srt":   true,
			base + ".ver1.chi.srt": true,
		}
		plans := NameBatch(video, []Upload{
			{Filename: "c.srt", Lang: "chi"},
			{Filename: "d.srt", Lang: "chi"},
		}, existing)
		got := SortedFilenames(plans)
		want := []string{base + ".ver2.chi.srt", base + ".ver3.chi.srt"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
	})

	t.Run("single upload onto clean release stays unversioned", func(t *testing.T) {
		existing := map[string]bool{video: true}
		plans := NameBatch(video, []Upload{{Filename: "one.srt", Lang: "chi"}}, existing)
		if plans[0].FinalName != base+".chi.srt" || plans[0].Version != 0 {
			t.Fatalf("got %+v", plans)
		}
	})

	t.Run("different languages same ext coexist", func(t *testing.T) {
		existing := map[string]bool{video: true}
		plans := NameBatch(video, []Upload{
			{Filename: "cn.srt", Lang: "chi"},
			{Filename: "en.srt", Lang: "eng"},
		}, existing)
		if plans[0].FinalName != base+".chi.srt" || plans[1].FinalName != base+".eng.srt" {
			t.Fatalf("got %+v", plans)
		}
	})

	t.Run("lang code passthrough and unknown fallback", func(t *testing.T) {
		if LangCode("中文") != "chi" || LangCode("JPN") != "jpn" {
			t.Errorf("LangCode resolution broken")
		}
		if LangCode("martian") != "und" {
			t.Errorf("unknown lang should fall back to und, got %q", LangCode("martian"))
		}
	})
}
