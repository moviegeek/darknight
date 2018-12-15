package main

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

func main() {
	go startPTSyncJob("", 0)

	err := startWebServer()
	if err != nil {
		log.Fatal(err)
	}
}

func startWebServer() error {
	router := gin.Default()
	router.LoadHTMLGlob("templates/*")
	router.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.tmpl", gin.H{
			"title": "Main site",
		})
	})

	return router.Run(":8080")
}

func startPTSyncJob(ptSyncAPIURL string, interval time.Duration) {

}
