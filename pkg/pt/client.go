package pt

import (
	"encoding/json"
	"io/ioutil"
	"net/http"

	log "github.com/sirupsen/logrus"

	"github.com/moviegeek/darknight/pkg/model"
)

/** example:
{
	"Title": "Shadow",
	"Year": 2018,
	"Group": "PuTao",
	"Source": "webdl",
	"Resolution": "1080p",
	"Size": 2500000000,
	"ID": "156571",
	"SiteName": "Putao"
}
*/
type ptSyncMovie struct {
	Title      string
	Year       int
	Group      string
	Source     string
	Resolution string
	Size       int64
	ID         string
	SiteName   string
}

type Client struct {
	url    string
	client *http.Client
}

func (pc *Client) GetPTMovies() ([]model.PTMovie, error) {
	result := []model.PTMovie{}

	resp, err := pc.client.Get(pc.url)

	if err != nil {
		log.Info("failed to call pt sync http")
		return result, err
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Info("failed to read response body")
		return result, err
	}

	log.Debugf("got response %d with body: %s", resp.StatusCode, body)

	ptsyncMovies := []ptSyncMovie{}
	err = json.Unmarshal(body, &ptsyncMovies)
	if err != nil {
		log.Info("failed to decode ptsync response into json")
		return result, err
	}

	result = convertPTSyncMovies(ptsyncMovies)

	return result, nil
}

//NewClient create a new pt client to get pt movies
func NewClient(ptSyncURL string) *Client {
	return &Client{ptSyncURL, &http.Client{}}
}

func convertPTSyncMovies(movies []ptSyncMovie) []model.PTMovie {
	ptMovies := []model.PTMovie{}
	for _, m := range movies {
		pm := model.PTMovie{}
		pm.Source = (model.PTSourceSite)(m.SiteName)
		pm.SourceID = m.ID
		pm.Title = m.Title
		pm.Year = m.Year
		pm.DigitalFormat = (model.DigitalFormat)(m.Source)
		pm.DigitalResolution = (model.DigitalResolution)(m.Resolution)
		pm.FileSize = (model.DigitalFileSize)(m.Size)
		pm.Group = m.Group
		ptMovies = append(ptMovies, pm)
	}

	return ptMovies
}
