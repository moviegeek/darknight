package model

import (
	"github.com/moviegeek/pt"
)
//Movie maps to an IMDB movie, it's the source of trueth for a single movie
type Movie struct {
	//ID id
	ID int64
	//Title movie title (normally English in IMDB)
	Title string
}

//PTMovie maps to a resource in a PT torrent site, for now putao or HDC
type PTMovie struct {
	pt.
	//ID id
	ID int64
	//
}
