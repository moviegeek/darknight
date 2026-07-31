// Package ffprobe shells out to the system ffprobe binary to extract
// container-level metadata (duration, codecs, bitrate, resolution, audio
// tracks, subtitle streams) from a media file. ffprobe must be on PATH.
package ffprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Result is the parsed ffprobe output we use.
type Result struct {
	Format   Format   `json:"format"`
	Streams  []Stream `json:"streams"`
}

// Format is the container-level info.
type Format struct {
	Filename     string `json:"filename"`
	FormatName   string `json:"format_name"`   // e.g. "matroska,webm"
	FormatLong   string `json:"format_long_name"`
	Duration     string `json:"duration"`      // seconds, as a string
	BitRate      string `json:"bit_rate"`      // bits/sec, as a string
	Size         string `json:"size"`          // bytes, as a string
	Tags         Tags   `json:"tags"`
}

// Stream is one elementary stream (video / audio / subtitle).
type Stream struct {
	Index          int    `json:"index"`
	CodecType      string `json:"codec_type"`     // video | audio | subtitle | data
	CodecName      string `json:"codec_name"`     // h264, hevc, ac3, ...
	Profile        string `json:"profile"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	PixFmt         string `json:"pix_fmt"`
	AvgFrameRate   string `json:"avg_frame_rate"` // fraction string like "24000/1001"
	RFrameRate     string `json:"r_frame_rate"`
	Channels       int    `json:"channels"`
	ChannelLayout  string `json:"channel_layout"`
	SampleRate     string `json:"sample_rate"`
	Duration       string `json:"duration"`
	BitRate        string `json:"bit_rate"`
	Disposition    Disposition `json:"disposition"`
	Tags           Tags   `json:"tags"`
	// Side data carries Dolby Vision / HDR10+ metadata as a list of objects.
	SideDataList []map[string]SideData `json:"side_data_list"`
}

// Disposition holds stream disposition flags.
type Disposition struct {
	Default     int `json:"default"`
	Dub         int `json:"dub"`
	Original    int `json:"original"`
	Comment     int `json:"comment"`
	Lyrics      int `json:"lyrics"`
	Forced      int `json:"forced"`
	HearingImpaired int `json:"hearing_impaired"`
}

// Tags is a free-form string map (title, language, ...).
type Tags map[string]string

// SideData is a side-data block (e.g. DOVI configuration record).
type SideData struct {
	SideDataType string `json:"side_data_type"`
}

// ProbeVersion is bumped whenever the probe command's flags change (e.g.
// adding -show_chapters). The scanner treats a stored version mismatch as a
// stale cache and re-probes even when size+mtime are unchanged - this is the
// cache-invalidation path for the raw JSON stored in movie_files.
const ProbeVersion = 1

// Probe runs ffprobe on path and returns the parsed metadata plus the verbatim
// ffprobe JSON stdout. The raw bytes are cached by the scanner so future
// media-info fields can be backfilled without re-probing; the parsed Result is
// the selective projection used by the derived columns today.
func Probe(ctx context.Context, path string) (*Result, []byte, error) {
	cmd := exec.CommandContext(ctx,
		"ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("ffprobe %s: %w", path, err)
	}
	var r Result
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, nil, fmt.Errorf("parse ffprobe output: %w", err)
	}
	return &r, out, nil
}

// DurationSeconds parses the format duration string.
func (f Format) DurationSeconds() float64 {
	return parseFloat(f.Duration)
}

// BitRateInt parses the format bit_rate string.
func (f Format) BitRateInt() int64 {
	return parseInt64(f.BitRate)
}

// SizeBytes parses the format size string.
func (f Format) SizeBytes() int64 {
	return parseInt64(f.Size)
}

// Container returns the leading format name (e.g. "matroska").
func (f Format) Container() string {
	if i := strings.IndexByte(f.FormatName, ','); i >= 0 {
		return f.FormatName[:i]
	}
	return f.FormatName
}

// FirstVideo returns the first video stream, or nil.
func (r *Result) FirstVideo() *Stream {
	for i := range r.Streams {
		if r.Streams[i].CodecType == "video" {
			return &r.Streams[i]
		}
	}
	return nil
}

// AudioStreams returns all audio streams in order.
func (r *Result) AudioStreams() []Stream {
	var out []Stream
	for _, s := range r.Streams {
		if s.CodecType == "audio" {
			out = append(out, s)
		}
	}
	return out
}

// SubtitleStreams returns all subtitle streams in order.
func (r *Result) SubtitleStreams() []Stream {
	var out []Stream
	for _, s := range r.Streams {
		if s.CodecType == "subtitle" {
			out = append(out, s)
		}
	}
	return out
}

// HasDolbyVision reports whether the video stream carries a DOVI side-data
// block.
func (s *Stream) HasDolbyVision() bool {
	for _, blocks := range s.SideDataList {
		for _, sd := range blocks {
			if strings.Contains(strings.ToLower(sd.SideDataType), "dovi") {
				return true
			}
		}
	}
	return false
}

// HasHDR10Plus reports whether the video stream carries HDR10+ dynamic
// metadata.
func (s *Stream) HasHDR10Plus() bool {
	for _, blocks := range s.SideDataList {
		for _, sd := range blocks {
			t := strings.ToLower(sd.SideDataType)
			if strings.Contains(t, "hdr10+") || strings.Contains(t, "hdr plus") {
				return true
			}
		}
	}
	return false
}

// FrameRate parses avg_frame_rate (returned as a fraction string like "24000/1001")
// from the format-level entry. Since ffprobe attaches frame rates to streams,
// callers should pass r.FirstVideo().AvgFrameRate; we expose ParseFraction
// here for convenience.
func ParseFraction(s string) float64 {
	if s == "" {
		return 0
	}
	parts := strings.SplitN(s, "/", 2)
	if len(parts) == 1 {
		return parseFloat(parts[0])
	}
	num := parseFloat(parts[0])
	den := parseFloat(parts[1])
	if den == 0 {
		return 0
	}
	return num / den
}

func parseFloat(s string) float64 {
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func parseInt64(s string) int64 {
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}
