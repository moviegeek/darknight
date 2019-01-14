package main

import (
	log "github.com/sirupsen/logrus"

	"os"
	"time"

	"github.com/go-pg/pg"
	"github.com/moviegeek/darknight/pkg/pt"
)

func main() {
	db := pg.Connect(&pg.Options{
		User:     "darknight",
		Password: "test",
		Database: "darknight",
	})
	defer db.Close()

	ptsyncURL := os.Getenv("PT_SYNC_URL")
	if ptsyncURL == "" {
		ptsyncURL = "https://pt-rss-sync-erc5ewm24.now.sh"
	}

	ptClient := pt.NewClient(ptsyncURL)

	startPTSyncJob(db, ptClient, time.Duration(1)*time.Hour)
}

func startPTSyncJob(db *pg.DB, ptClient *pt.Client, interval time.Duration) {
	timer := time.NewTicker(interval)

	for {
		select {
		case <-timer.C:

			ptMovies, err := ptClient.GetPTMovies()
			if err != nil {
				log.Infof("failed to get pt movies: %v", err)
				continue
			}

			for _, ptmovie := range ptMovies {
				log.Debugf("insert movie into db: %v", ptmovie)
				_, err := db.Model(&ptmovie).OnConflict("ON CONSTRAINT pt_movies_source_source_id_key DO NOTHING").Insert()
				if err != nil {
					log.Infof("failed to insert movie %v: %v", ptmovie, err)
				} else {
					log.Debugf("insert success")
				}
			}
		}
		break
	}
}
