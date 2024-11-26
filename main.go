package main

import (
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"time"

	_ "github.com/lib/pq"
	"golang.org/x/exp/rand"
)

type ShortLinks struct {
	tmpl *template.Template
	mux  *http.ServeMux
	db   *sql.DB
	r    *rand.Rand
}

type response struct {
	Short string `json:"short"`
}

func NewShortLinks(db *sql.DB) (*ShortLinks, error) {
	tmpl := template.New("index.html")

	tmpl, err := tmpl.ParseFiles("static/index.html")
	if err != nil {
		return nil, fmt.Errorf("static/index.html: %w", err)
	}

	sl := &ShortLinks{
		tmpl: tmpl,
		mux:  http.NewServeMux(),
		db:   db,
		r:    rand.New(rand.NewSource(uint64(time.Now().UnixNano()))),
	}

	sl.mux.HandleFunc("GET /{$}", sl.serveRoot)
	sl.mux.HandleFunc("GET /{short}", sl.serveShort)
	sl.mux.HandleFunc("POST /{$}", sl.serveSet)

	return sl, nil
}

func (sl *ShortLinks) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sl.mux.ServeHTTP(w, r)
}

func (sl *ShortLinks) serveRoot(w http.ResponseWriter, r *http.Request) {
	log.Printf("%s %s", r.RemoteAddr, r.URL.Path)
	sl.serveRootWithPath(w, r, "")
}

func (sl *ShortLinks) serveRootWithPath(w http.ResponseWriter, r *http.Request, path string) {
	err := sl.tmpl.Execute(w, map[string]any{
		"path": path,
	})
	if err != nil {
		sendError(w, http.StatusInternalServerError, "error executing template: %s", err)
		return
	}
}

func (sl *ShortLinks) serveShort(w http.ResponseWriter, r *http.Request) {
	log.Printf("%s %s", r.RemoteAddr, r.URL.Path)

	short := r.PathValue("short")

	row := sl.db.QueryRow(`SELECT long FROM links WHERE short = $1`, short)
	var long string
	err := row.Scan(&long)
	if err != nil {
		sl.serveRootWithPath(w, r, short)
		return
	}

	http.Redirect(w, r, long, http.StatusTemporaryRedirect)
}

func (sl *ShortLinks) serveSet(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		sendError(w, http.StatusBadRequest, "Parse form: %s", err)
		return
	}

	log.Printf("%s %s %s", r.RemoteAddr, r.URL.Path, r.Form.Encode())

	short := r.Form.Get("short")

	if short == "" {
		short, err = sl.genShort()
		if err != nil {
			sendError(w, http.StatusInternalServerError, "genShort: %s", err)
			return
		}
	}

	long := r.Form.Get("long")
	if long == "" {
		sendError(w, http.StatusBadRequest, "long= param required")
		return
	}

	_, err = sl.db.Exec(`
INSERT INTO links (short, long)
VALUES ($1, $2)
ON CONFLICT (short)
DO UPDATE SET long = $2;
	`, short, long)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "upsert: %s", err)
		return
	}

	sendJSON(w, response{
		Short: short,
	})
}

func (sl *ShortLinks) genShort() (string, error) {
	for chars := 3; chars <= 10; chars++ {
		b := make([]byte, chars)

		for i := range b {
			b[i] = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"[sl.r.Intn(62)]
		}

		short := string(b)

		exists := false
		err := sl.db.QueryRow("SELECT EXISTS(SELECT 1 FROM links WHERE short = $1)", short).Scan(&exists)
		if err != nil {
			return "", fmt.Errorf("check exists: %w", err)
		}

		if !exists {
			return short, nil
		}
	}

	return "", fmt.Errorf("no available short link found")
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		log.Fatalf("please set PORT")
	}

	pgConn := os.Getenv("PGCONN")
	if pgConn == "" {
		log.Fatalf("please set PGCONN")
	}

	db, err := sql.Open("postgres", pgConn)
	if err != nil {
		log.Fatal(err)
	}

	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS links (
    short VARCHAR(100) PRIMARY KEY,
    long TEXT NOT NULL
);`)
	if err != nil {
		log.Fatalf("Failed to create table: %v", err)
	}

	sl, err := NewShortLinks(db)
	if err != nil {
		log.Fatalf("Failed to create shortlinks: %v", err)
	}

	http.Handle("/", sl)

	bind := fmt.Sprintf(":%s", port)
	log.Printf("listening on %s", bind)

	if err := http.ListenAndServe(bind, nil); err != nil {
		log.Fatalf("listen: %s", err)
	}
}
