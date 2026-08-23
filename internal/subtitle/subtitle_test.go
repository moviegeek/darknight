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

	t.Run("two same lang+ext in one batch -> ver1, ver2", func(t *testing.T) {
		existing := map[string]bool{video: true}
		plans := NameBatch(video, []Upload{
			{Filename: "a.srt", Lang: "chi"},
			{Filename: "b.srt", Lang: "chi"},
		}, existing)
		got := SortedFilenames(plans)
		want := []string{base + ".chi.srt", base + ".ver1.chi.srt"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
	})

	t.Run("existing ver1 then upload two more", func(t *testing.T) {
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
