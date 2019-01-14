package main

import (
	"net/http"

	"github.com/moviegeek/darknight/pkg/model"
	log "github.com/sirupsen/logrus"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg"
	"github.com/go-pg/pg/orm"
)

func main() {
	log.SetLevel(log.DebugLevel)

	db := initDB()
	defer db.Close()

	err := createSchema(db)
	if err != nil {
		log.Fatalf("error while create db schema: %v", err)
	}

	err = startWebServer(db)
	if err != nil {
		log.Fatal(err)
	}
}

func initDB() *pg.DB {
	db := pg.Connect(&pg.Options{
		User:     "darknight",
		Password: "test",
		Database: "darknight",
	})

	return db
}

func startWebServer(db *pg.DB) error {
	router := gin.Default()

	router.LoadHTMLGlob("templates/*")

	router.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.tmpl", gin.H{
			"title": "Main site",
		})
	})

	return router.Run(":8080")
}

func createSchema(db *pg.DB) error {
	log.Debugf("creating database tables")
	for _, model := range []interface{}{(*model.Movie)(nil), (*model.PTMovie)(nil)} {
		err := db.CreateTable(model, &orm.CreateTableOptions{
			IfNotExists: true,
		})
		if err != nil {
			return err
		}
	}
	return nil
}
