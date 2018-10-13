package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/mariusor/littr.go/app/db"

	log "github.com/sirupsen/logrus"

	"github.com/mariusor/littr.go/app/models"

	_ "github.com/lib/pq"
)

var defaultSince, _ = time.ParseDuration("90h")

func init() {
	dbPw := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")
	dbUser := os.Getenv("DB_USER")

	var err error
	connStr := fmt.Sprintf("user=%s password=%s dbname=%s sslmode=disable", dbUser, dbPw, dbName)

	db.Config.DB, err = sqlx.Connect("postgres", connStr)
	if err != nil {
		log.WithFields(log.Fields{}).Error(err)
	}
}

func main() {
	var key string
	var handle string
	var since time.Duration
	var items bool
	var accounts bool
	flag.StringVar(&handle, "handle", "", "the content key to update votes for, implies -accounts")
	flag.StringVar(&key, "key", "", "the content key to update votes for")
	flag.BoolVar(&items, "items", true, "update scores for items")
	flag.BoolVar(&accounts, "accounts", false, "update scores for account")
	flag.DurationVar(&since, "since", defaultSince, "the content key to update votes for, default is 90h")
	flag.Parse()

	var err error
	// recount all votes for content items
	var scores []models.Score
	if accounts {
		which := ""
		val := ""
		if handle != "" || key != "" {
			if len(handle) > 0 {
				which = "handle"
				val = handle
			} else {
				which = "key"
				val = key
			}
		}
		scores, err = db.LoadScoresForAccounts(since, which, val)
	} else if items {
		scores, err = db.LoadScoresForItems(since, key)
	}
	if err != nil {
		panic(err)
	}

	sql := `update "%s" set score = $1 where id = $2;`
	for _, score := range scores {
		var col string
		if score.Type == models.ScoreItem {
			col = `content_items`
		} else {
			col = `accounts`
		}
		_, err := db.Config.DB.Exec(fmt.Sprintf(sql, col), score.Score, score.ID)
		if err != nil {
			panic(err)
		}
	}
}
