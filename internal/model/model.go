// Package model holds plain domain types shared across the scanner, store and
// API layers. These structs mirror the SQLite schema but are decoupled from
// any particular driver.
package model

// Library is a media library root directory.
type Library struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	RootPath     string `json:"root_path"`
	ScanInterval int    `json:"scan_interval"` // seconds; 0 = manual
	LastScanAt   int64  `json:"last_scan_at"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

// Movie is a logical film (one row per TMDB entry or, when offline, per
// .nfo/title+year match).
type Movie struct {
	ID               int64   `json:"id"`
	Title            string  `json:"title"`
	SortTitle        string  `json:"sort_title"`
	OriginalTitle    string  `json:"original_title"` // TMDB original_title (original language)
	TitleEn          string  `json:"title_en"`       // localized English title
	TitleZh          string  `json:"title_zh"`       // localized Chinese title
	Year             int     `json:"year"`
	ReleaseDate      string  `json:"release_date"`
	Runtime          int     `json:"runtime"`
	Synopsis         string  `json:"synopsis"`
	PosterPath       string  `json:"poster_path"`
	BackdropPath     string  `json:"backdrop_path"`
	TMDBID           int64   `json:"tmdb_id"`
	IMDBID           string  `json:"imdb_id"`
	VoteAverage      float64 `json:"vote_average"`
	VoteCount        int     `json:"vote_count"`
	CollectionID     int64   `json:"collection_id"`
	Country          string  `json:"country"`          // primary production country (ISO or name)
	Countries        string  `json:"countries"`        // all production countries, comma-separated ISO codes
	OriginalLanguage string  `json:"original_language"` // ISO 639-1 code from TMDB
	CreatedAt        int64   `json:"created_at"`
	UpdatedAt        int64   `json:"updated_at"`
}

// Collection is a system-level movie collection (series / anthology).
type Collection struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	TMDBID       int64  `json:"tmdb_id"`
	PosterPath   string `json:"poster_path"`
	BackdropPath string `json:"backdrop_path"`
	Overview     string `json:"overview"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

// CollectionPart is one member movie of a collection, cached from TMDB's
// /collection/{id} parts array. LocalMovieID is the row id of the matching
// local movies row (joined on tmdb_id) when the film is in the library, or 0
// when it is missing locally. The detail page uses that to distinguish "已有"
// from "缺失" while keeping the series in release order.
type CollectionPart struct {
	ID            int64   `json:"id"`
	CollectionID  int64   `json:"collection_id"`
	TMDBID        int64   `json:"tmdb_id"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	ReleaseDate   string  `json:"release_date"`
	PosterPath    string  `json:"poster_path"`
	Overview      string  `json:"overview"`
	VoteAverage   float64 `json:"vote_average"`
	Order         int     `json:"order"`
	LocalMovieID  int64   `json:"local_movie_id"` // 0 = not in library (missing)
}

// Genre is a movie category.
type Genre struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Person is a cast/crew member.
type Person struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	TMDBID      int64  `json:"tmdb_id"`
	ProfilePath string `json:"profile_path"`
}

// Credit links a Person to a Movie with a role.
type Credit struct {
	MovieID   int64  `json:"movie_id"`
	PersonID  int64  `json:"person_id"`
	Role      string `json:"role"` // "cast" | "crew"
	Job       string `json:"job"`
	Character string `json:"character"`
	Order     int    `json:"order"`
}

// MovieFile is a physical release of a movie: a single video file or a Blu-ray
// disc folder. Technical fields come from filename parsing; ffprobe refines the
// container-level ones.
type MovieFile struct {
	ID            int64   `json:"id"`
	MovieID       int64   `json:"movie_id"`
	LibraryID     int64   `json:"library_id"`
	DirPath       string  `json:"dir_path"`
	FileName      string  `json:"file_name"`
	IsDisc        bool    `json:"is_disc"`
	FileSize      int64   `json:"file_size"`
	FileModified  int64   `json:"file_modified"`
	ReleaseGroup  string  `json:"release_group"`
	Edition       string  `json:"edition"`
	Source        string  `json:"source"`
	Resolution    string  `json:"resolution"`
	VideoCodec    string  `json:"video_codec"`
	AudioCodec    string  `json:"audio_codec"`
	AudioChannels string  `json:"audio_channels"`
	HDR           string  `json:"hdr"`
	DolbyVision   bool    `json:"dolby_vision"`
	BitDepth      int     `json:"bit_depth"`
	AudioCount    int     `json:"audio_count"`
	Language      string  `json:"language"`
	IsCollection  bool    `json:"is_collection"`
	RawName       string  `json:"raw_name"`
	DurationSec   float64 `json:"duration_sec"`
	VideoBitrate  int64   `json:"video_bitrate"`
	FrameRate     float64 `json:"frame_rate"`
	Width         int     `json:"width"`
	Height        int     `json:"height"`
	Container           string `json:"container"`
	// FFProbeJSON is the verbatim ffprobe JSON stdout cached for this file,
	// so future media-info fields can be backfilled without re-probing.
	// Empty when not yet cached (disc releases are never probed).
	FFProbeJSON    string `json:"ffprobe_json"`
	FFProbeVersion int    `json:"ffprobe_version"` // matches ffprobe.ProbeVersion when cached; 0 = none
	FFProbeAt      int64  `json:"ffprobe_at"`      // unix ts when the JSON was captured
	NFOPath             string `json:"nfo_path"`
	SubtitleLanguages   string `json:"subtitle_languages"`    // comma-separated, e.g. "chi,eng"
	HasExternalSubtitle bool   `json:"has_external_subtitle"`
	ScannedAt           int64  `json:"scanned_at"`
	CreatedAt           int64  `json:"created_at"`
	UpdatedAt           int64  `json:"updated_at"`
}

// AudioTrack is one audio stream inside a MovieFile.
type AudioTrack struct {
	ID          int64  `json:"id"`
	MovieFileID int64  `json:"movie_file_id"`
	Language    string `json:"language"`
	Codec       string `json:"codec"`
	Channels    int    `json:"channels"`
	Title       string `json:"title"`
	IsDefault   bool   `json:"is_default"`
	IsLossless  bool   `json:"is_lossless"`
	Order       int    `json:"order"`
}

// Subtitle is an external file or embedded stream of a MovieFile.
type Subtitle struct {
	ID          int64  `json:"id"`
	MovieFileID int64  `json:"movie_file_id"`
	FilePath    string `json:"file_path"`
	Language    string `json:"language"`
	Format      string `json:"format"`
	IsEmbedded  bool   `json:"is_embedded"`
	IsDefault   bool   `json:"is_default"`
	Order       int    `json:"order"`
	FileSize    int64  `json:"file_size"` // external subtitles only; 0 for embedded
}

// WatchStatus is the per-movie watching state for the current user.
type WatchStatus struct {
	MovieID      int64   `json:"movie_id"`
	Status       string  `json:"status"` // unwatched | watching | watched
	Progress     float64 `json:"progress"`
	LastPlayedAt int64   `json:"last_played_at"`
	Rating       int     `json:"rating"`
	UpdatedAt    int64   `json:"updated_at"`
}

// UserCollection is a user-curated themed album of movies.
type UserCollection struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CoverPath   string `json:"cover_path"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

// ScanJob records the outcome of one scan run against a library.
type ScanJob struct {
	ID           int64  `json:"id"`
	LibraryID    int64  `json:"library_id"`
	StartedAt    int64  `json:"started_at"`
	FinishedAt   int64  `json:"finished_at"`
	Status       string `json:"status"` // running | completed | failed
	FilesAdded   int    `json:"files_added"`
	FilesUpdated int    `json:"files_updated"`
	FilesRemoved int    `json:"files_removed"`
	Error        string `json:"error"`
}
