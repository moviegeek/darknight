package model

//Movie maps to an IMDB movie, it's the source of trueth for a single movie
type Movie struct {
	//ID id
	ID int64
	//Title movie title (normally English in IMDB)
	Title string
	//Year production year
	Year int
}

//PTMovie maps to a resource in a PT torrent site, for now putao or HDC
type PTMovie struct {
	ID                int
	Source            PTSourceSite `sql:"unique:pt_source_id"`
	SourceID          string       `sql:"unique:pt_source_id"`
	Title             string
	Year              int
	DigitalFormat     DigitalFormat
	DigitalResolution DigitalResolution
	FileSize          DigitalFileSize
	Group             string
}
