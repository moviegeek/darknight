package nfo

import (
	"encoding/xml"
	"io"
	"os"
	"strconv"
	"strings"
)

// NFO is the subset of a movie .nfo file we care about. NFOs are written by
// PT tools (Kodi/Jellyfin compatible) and embed uniqueids for IMDB / TMDB.
type NFO struct {
	Title         string     `xml:"title"`
	OriginalTitle string     `xml:"originaltitle"`
	SortTitle     string     `xml:"sorttitle"`
	Year          int        `xml:"year"`
	Plot          string     `xml:"plot"`
	Runtime       int        `xml:"runtime"` // minutes
	Studio        string     `xml:"studio"`
	Country       string     `xml:"country"`
	Genre         string     `xml:"genre"` // " / " separated
	IMDBID        string     `xml:"imdbid"`
	TMDBID        int64      `xml:"tmdbid"`
	UniqueIDs     []UniqueID `xml:"uniqueid"`
	Ratings       Ratings    `xml:"ratings"`
}

// UniqueID is a named external id (tmdb, imdb, ...).
type UniqueID struct {
	Type string `xml:"type,attr"`
	ID   string `xml:",chardata"`
}

// Ratings wraps the <ratings> element.
type Ratings struct {
	Rating []Rating `xml:"rating"`
}

// Rating is one rating entry.
type Rating struct {
	Name  string  `xml:"name,attr"`
	Max   int     `xml:"max,attr"`
	Value float64 `xml:"value"`
	Votes int     `xml:"votes"`
}

// Parse reads an NFO document from r.
func Parse(r io.Reader) (*NFO, error) {
	var n NFO
	dec := xml.NewDecoder(r)
	if err := dec.Decode(&n); err != nil {
		return nil, err
	}
	// uniqueid elements often carry the authoritative ids even when the
	// bare <imdbid>/<tmdbid> tags are absent.
	for _, uid := range n.UniqueIDs {
		switch strings.ToLower(uid.Type) {
		case "imdb":
			if n.IMDBID == "" {
				n.IMDBID = uid.ID
			}
		case "tmdb":
			if n.TMDBID == 0 {
				if v, err := strconv.ParseInt(uid.ID, 10, 64); err == nil {
					n.TMDBID = v
				}
			}
		}
	}
	if n.IMDBID != "" {
		n.IMDBID = strings.TrimPrefix(strings.ToLower(n.IMDBID), "tt")
		n.IMDBID = "tt" + n.IMDBID
	}
	return &n, nil
}

// ParseFile reads an NFO from a file path.
func ParseFile(path string) (*NFO, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// Genres splits the Genre field into individual genre names.
func (n *NFO) Genres() []string {
	if n.Genre == "" {
		return nil
	}
	parts := strings.Split(n.Genre, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
