package main

import (
	"encoding/gob"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/mariusor/littr.go/app"

	"github.com/jmoiron/sqlx"
	"github.com/mariusor/littr.go/app/db"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/gorilla/sessions"
	"github.com/juju/errors"
	_ "github.com/lib/pq"
	"github.com/mariusor/littr.go/app/api"
	"github.com/mariusor/littr.go/app/frontend"
	"github.com/mariusor/littr.go/app/models"
	log "github.com/sirupsen/logrus"
)

const defaultHost = "localhost"
const defaultPort = 3000

var listenHost string
var listenPort int64
var listenOn string

var littr app.Littr

func loadEnv(l *app.Littr) (bool, error) {
	l.SessionKeys[0] = []byte(os.Getenv("SESS_AUTH_KEY"))
	l.SessionKeys[1] = []byte(os.Getenv("SESS_ENC_KEY"))

	listenHost = os.Getenv("HOSTNAME")
	listenPort, _ = strconv.ParseInt(os.Getenv("PORT"), 10, 64)
	listenOn = os.Getenv("LISTEN")

	frontend.Version = os.Getenv("VERSION")
	env := app.EnvType(os.Getenv("ENV"))
	if !app.ValidEnv(env) {
		env = app.DEV
	}
	littr.Env = env
	if listenPort == 0 {
		listenPort = defaultPort
	}
	if listenHost == "" {
		listenHost = defaultHost
	}
	l.HostName = listenHost
	l.Port = listenPort
	l.Listen = listenOn

	if littr.Env == app.PROD {
		log.SetLevel(log.WarnLevel)
	} else {
		log.SetFormatter(&log.TextFormatter{
			DisableColors:          false,
			DisableLevelTruncation: true,
			ForceColors:            true,
		})
		log.SetOutput(os.Stdout)
		log.SetLevel(log.DebugLevel)
	}

	logger := log.StandardLogger()
	middleware.DefaultLogger = middleware.RequestLogger(&middleware.DefaultLogFormatter{Logger: logger})
	api.Logger = logger

	return true, nil
}

func init() {
	littr = app.Littr{HostName: listenHost, Port: listenPort}

	loadEnv(&littr)

	gob.Register(models.Account{})
	gob.Register(frontend.Flash{})

	s := sessions.NewCookieStore(littr.SessionKeys[0], littr.SessionKeys[1])
	s.Options.Domain = littr.HostName
	s.Options.Path = "/"
	frontend.SessionStore = s

	dbPw := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")
	dbUser := os.Getenv("DB_USER")
	dbHost := os.Getenv("DB_HOST")

	connStr := fmt.Sprintf("host=%s user=%s password=%s dbname=%s sslmode=disable", dbHost, dbUser, dbPw, dbName)
	con, err := sqlx.Open("postgres", connStr)
	if err != nil {
		log.WithFields(log.Fields{
			"dbName": dbName,
			"dbUser": dbUser,
		}).Error(errors.NewErrWithCause(err, "failed to connect to the database"))
	}

	db.Config.DB = con
	api.Config.BaseUrl = os.Getenv("LISTEN")
}

func serveFile(st string) func (w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Clean(chi.URLParam(r, "path"))
		fullPath := filepath.Join(st, path)
		w.Header().Del("Cookie")
		http.ServeFile(w, r, fullPath)
	}
}
func main() {
	var wait time.Duration
	flag.DurationVar(&wait, "graceful-timeout", time.Second*15, "the duration for which the server gracefully wait for existing connections to finish - e.g. 15s or 1m")
	flag.Parse()

	// Routes
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)

	if littr.Env == app.PROD {
		r.Use(middleware.Recoverer)
	}

	// Frontend
	r.With(api.Repository).Route("/", func(r chi.Router) {
		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			frontend.HandleError(w, r, http.StatusNotFound, errors.Errorf("%q not found", r.RequestURI))
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			frontend.HandleError(w, r, http.StatusMethodNotAllowed, errors.Errorf("invalid %q request", r.Method))
		})

		r.Use(frontend.LoadSessionData)

		r.Get("/", frontend.HandleIndex)

		r.Get("/submit", frontend.ShowSubmit)
		r.Post("/submit", frontend.HandleSubmit)

		r.Get("/register", frontend.ShowRegister)
		r.Post("/register", frontend.HandleRegister)

		//r.With(Item).Get("/~{handle}/{hash}", frontend.ShowItem)
		r.Route("/~{handle}", func(r chi.Router) {
			r.Get("/", frontend.ShowAccount)
			r.Get("/{hash}", frontend.ShowItem)
			r.Post("/{hash}", frontend.HandleSubmit)
			r.Get("/{hash}/{direction}", frontend.HandleVoting)
		})

		//r.Get("/{year:[0-9]{4}}/{month:[0-9]{2}}/{day:[0-9]{2}}/", frontend.HandleDate)
		r.Get("/{year:[0-9]{4}}/{month:[0-9]{2}}/{day:[0-9]{2}}/{hash}", frontend.ShowItem)
		r.Get("/{year:[0-9]{4}}/{month:[0-9]{2}}/{day:[0-9]{2}}/{hash}/{direction}", frontend.HandleVoting)
		r.Post("/{year:[0-9]{4}}/{month:[0-9]{2}}/{day:[0-9]{2}}/{hash}", frontend.HandleSubmit)

		r.Get("/item/{hash}", frontend.HandleItemRedirect)

		r.Get("/domains/{domain}", frontend.HandleDomains)

		r.Get("/logout", frontend.HandleLogout)
		r.Get("/login", frontend.ShowLogin)
		r.Post("/login", frontend.HandleLogin)

		r.Get("/auth/{provider}", frontend.HandleAuth)
		r.Get("/auth/{provider}/callback", frontend.HandleCallback)

		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			frontend.HandleError(w, r, http.StatusNotFound, errors.Errorf("%q not found", r.RequestURI))
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			frontend.HandleError(w, r, http.StatusMethodNotAllowed, errors.Errorf("invalid %q request", r.Method))
		})
	})

	// API
	r.With(db.Repository).Route("/api", func(r chi.Router) {
		r.Route("/accounts", func(r chi.Router) {
			r.With(api.LoadFiltersCtxt).Get("/", api.HandleAccountsCollection)

			r.With(frontend.LoadSessionData).Get("/accounts/verify_credentials", api.HandleVerifyCredentials)
			r.Route("/{handle}", func(r chi.Router) {
				r.Use(api.AccountCtxt)

				r.Get("/", api.HandleAccount)
				r.Route("/{collection}", func(r chi.Router) {
					r.With(api.LoadFiltersCtxt, api.ItemCollectionCtxt).Get("/", api.HandleCollection)
					r.With(api.LoadFiltersCtxt).Post("/", api.UpdateItem)
					r.Route("/{hash}", func(r chi.Router) {
						r.With(api.LoadFiltersCtxt, api.ItemCtxt).Get("/", api.HandleCollectionItem)
						r.With(api.LoadFiltersCtxt).Put("/", api.UpdateItem)

						r.With(api.LoadFiltersCtxt, api.ItemCtxt).Get("/replies", api.HandleItemReplies)
					})
				})
			})
		})

		r.Route("/{collection}", func(r chi.Router) {
			r.Use(api.ServiceCtxt)

			r.With(api.LoadFiltersCtxt, api.ItemCollectionCtxt).Get("/", api.HandleCollection)
			r.With(api.LoadFiltersCtxt, api.ItemCtxt).Get("/{hash}", api.HandleCollectionItem)
			r.With(api.LoadFiltersCtxt, api.ItemCtxt).Get("/{hash}/replies", api.HandleItemReplies)
		})
		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			api.HandleError(w, r, http.StatusNotFound, errors.Errorf("%s not found", r.RequestURI))
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			api.HandleError(w, r, http.StatusMethodNotAllowed, errors.Errorf("invalid %s request", r.Method))
		})
	})

	// Web-Finger
	r.With(db.Repository).Route("/.well-known", func(r chi.Router) {
		r.Get("/webfinger", api.HandleWebFinger)
		r.Get("/host-meta", api.HandleHostMeta)
		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			api.HandleError(w, r, http.StatusNotFound, errors.Errorf("%s not found", r.RequestURI))
		})
	})

	workDir, _ := os.Getwd()
	assets := filepath.Join(workDir, "assets")
	// static
	r.Get("/ns", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json+ld")
		w.Header().Del("Cookie")
		http.ServeFile(w, r, filepath.Join(assets, "ns.json"))
	}))
	r.Get("/favicon.ico", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Del("Cookie")
		http.ServeFile(w, r, filepath.Join(assets, "favicon.ico"))
	}))

	r.Get("/css/{path}", serveFile(filepath.Join(assets, "css")))
	r.Get("/js/{path}", serveFile(filepath.Join(assets, "js")))

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		frontend.HandleError(w, r, http.StatusNotFound, errors.Errorf("%s not found", r.RequestURI))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		frontend.HandleError(w, r, http.StatusMethodNotAllowed, errors.Errorf("%s not allowed", r.Method))
	})

	littr.Run(r, wait)
}
