// Package parser turns scene/release-style movie directory and file names into
// structured metadata.
//
// A typical name follows the convention:
//
//	Title.Year.Resolution.Source.VideoCodec.Audio-Group
//
// for example "1917.2019.1080p.BluRay.x264.Atmos.TrueHD7.1-HDChina" decomposes
// into Title="1917", Year=2019, Resolution=1080p, Source=BluRay, VideoCodec=x264,
// AudioCodec=TrueHD, AudioChannels=7.1, ReleaseGroup="HDChina".
//
// Real-world names are much messier than the convention (HDR/DoVi, multi-audio
// counts, regional editions, collections, brackets, ...). ParseTitle handles
// the variations observed in the library.
package parser

// Source is the origin medium of a release.
type Source string

const (
	SourceUnknown Source = ""
	BluRay        Source = "BluRay"     // Blu-ray disc / rip (incl. "Blu-ray", "Bluray")
	UHDBluRay     Source = "UHD BluRay" // UHD/4K Blu-ray (often appears as "UHD.BluRay")
	BDRip         Source = "BDRip"      // re-encode from a Blu-ray source
	BRRip         Source = "BRRip"
	WebDL         Source = "WebDL" // WEB-DL / WEBRip
	HDTV          Source = "HDTV"
	DVDRip        Source = "DVDRip"
	UHDTV         Source = "UHDTV"
	ThreeD        Source = "3D" // SBS / 3D Blu-ray
)

// Resolution is the video frame resolution class.
type Resolution string

const (
	ResUnknown Resolution = ""
	Res480p    Resolution = "480p"
	Res720p    Resolution = "720p"
	Res1080p   Resolution = "1080p"
	Res2160p   Resolution = "2160p" // 4K UHD
)

// VideoCodec is the video (de)coder used for encoding.
type VideoCodec string

const (
	CodecUnknown VideoCodec = ""
	X264         VideoCodec = "x264"
	X265         VideoCodec = "x265"
	AVC          VideoCodec = "AVC"  // H.264, as labeled on Blu-ray
	HEVC         VideoCodec = "HEVC" // H.265, as labeled on Blu-ray
	H264         VideoCodec = "H.264"
	H265         VideoCodec = "H.265"
	VC1          VideoCodec = "VC-1"
	VP9          VideoCodec = "VP9"
)

// AudioCodec is the audio (de)coder / container format.
type AudioCodec string

const (
	AudioUnknown     AudioCodec = ""
	DTS              AudioCodec = "DTS"
	DTSHDMA          AudioCodec = "DTS-HD MA" // DTS-HD Master Audio
	DTSX             AudioCodec = "DTS:X"
	TrueHD           AudioCodec = "TrueHD"
	Atmos            AudioCodec = "Atmos" // Dolby Atmos (usually a TrueHD/DD+ flag)
	EAC3             AudioCodec = "E-AC-3" // Dolby Digital Plus (DD+)
	AC3              AudioCodec = "AC-3"   // Dolby Digital (DD)
	DDP              AudioCodec = "DD+"    // alias for E-AC-3 used in filenames
	DD               AudioCodec = "DD"     // alias for AC-3 used in filenames
	FLAC             AudioCodec = "FLAC"
	AAC              AudioCodec = "AAC"
	PCM              AudioCodec = "PCM"
	LPCM             AudioCodec = "LPCM"
	DolbyAtmos       AudioCodec = "Dolby Atmos"
)

// HDRMode is the high-dynamic-range mode, if any.
type HDRMode string

const (
	HDRNone     HDRMode = ""
	HDR10       HDRMode = "HDR10"
	HDR10Plus   HDRMode = "HDR10+"
	HLG         HDRMode = "HLG"
	DolbyVision HDRMode = "DV" // Dolby Vision (often abbreviated "DV" or "DoVi")
)

// FileMeta is the fully parsed representation of a release name.
//
// Unknown fields keep their zero value; callers should treat zero/empty as
// "not detected" rather than an error.
type FileMeta struct {
	Title         string     // human-readable title with dots/spaces normalized to spaces
	Year          int        // release year; 0 if not detected
	Source        Source     // BluRay, WebDL, ...
	Resolution    Resolution // 1080p, 2160p, ...
	VideoCodec    VideoCodec
	AudioCodec    AudioCodec
	AudioChannels string     // e.g. "5.1", "7.1", "2.0"; "" if unknown
	HDR           HDRMode    // HDR10 / DV / ...
	BitDepth      int        // 8 / 10 / 12; 0 if not stated
	Edition       string     // "Directors Cut", "Extended Cut", "Criterion", "IMAX", ...
	AudioCount    int        // number of audio tracks if stated (2Audio, 3Audio, TriAudio)
	ReleaseGroup  string     // the group after the last '-'; "" if none
	IsCollection  bool       // Anthology/Trilogy/Collection or a year range like "1979-1997"
	Language      string     // regional/language tag if present (JPN, KOR, CEE, FRA, ...)
	ExtraFiles    []string   // detected side files (e.g. ".chi.srt"); informational only
}

// HasHDR reports whether any HDR/Dolby Vision flag was detected.
func (m FileMeta) HasHDR() bool { return m.HDR != HDRNone }

// IsUHD reports whether this is a 4K release (2160p).
func (m FileMeta) IsUHD() bool { return m.Resolution == Res2160p }
