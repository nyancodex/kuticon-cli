// Package clienttest is a fake Kuticon site for CLI and MCP tests. It answers
// the public API with fixed data and records what it was asked.
package clienttest

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

const PackID = "0123456789abcdef0123456789abcdef"

// Fake is the site. Steps is the status sequence GET /api/packs/{id} walks;
// the last entry repeats.
type Fake struct {
	*httptest.Server
	mu      sync.Mutex
	Steps   []map[string]any
	polls   int
	Posts   []map[string]any
	Refuse  int    // status for POST /api/packs, 0 is 202
	Message string // its error sentence
	Agents  []string
	// TestTokens are the X-Kuticon-Test headers of the pack posts.
	TestTokens []string
	Zip        []byte
}

// PNG is a tiny real PNG header, enough for tests that only compare bytes.
var PNG = []byte("\x89PNG\r\n\x1a\nfake")

func SVG(id string) string {
	return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" data-id="` + id + `"><path d="M4 4h16"/></svg>`
}

func New() *Fake {
	f := &Fake{Steps: []map[string]any{
		{"status": "queued", "position": 2},
		{"status": "queued", "position": 1},
		{"status": "drawing", "position": 0},
		{"status": "ready", "position": 0, "title": "Bakery", "icons": []map[string]string{{"id": "croissant", "name": "Croissant"}, {"id": "bread", "name": "Bread"}}},
	}}
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for name, body := range map[string]string{"svg/croissant.svg": SVG("croissant"), "svg/bread.svg": SVG("bread"), "README.md": "# Bakery\n", "manifest.json": "{}"} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	f.Zip = b.Bytes()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/meta", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{
			"styles":  []map[string]string{{"id": "outline", "label": "Outline", "description": "Line icons in currentColor."}, {"id": "neon", "label": "Neon", "description": "Glowing tubes on a dark board."}},
			"weights": []string{"fine", "text", "bold"}, "corners": []string{"round", "sharp"}, "counts": []int{1, 4, 12},
			"formats":    []map[string]any{{"id": "svg", "description": "SVG masters.", "default": true}, {"id": "font", "description": "Icon font.", "default": false}},
			"sizes":      []int{16, 256},
			"dailyLimit": 8, "promptMaxLen": 280,
		})
	})
	mux.HandleFunc("GET /api/icons", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		hits := []map[string]string{}
		if strings.Contains("croissant coffee", q) {
			hits = append(hits, map[string]string{"id": "croissant", "name": "Croissant", "pack": "bakery", "packName": "Bakery", "category": "Food", "page": f.URL + "/icons/croissant", "svg": f.URL + "/icons/croissant/svg"})
		}
		writeJSON(w, 200, map[string]any{"query": q, "total": len(hits), "offset": 0, "icons": hits})
	})
	mux.HandleFunc("GET /icons/{id}/svg", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "croissant" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Write([]byte(SVG("croissant-" + r.URL.Query().Get("style"))))
	})
	mux.HandleFunc("GET /icons/{id}/png", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "croissant" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("size") == "999" {
			http.Error(w, "Pixels must be 16, 24, 32, 48, 64, 128, or 256.", 400)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(PNG)
	})
	mux.HandleFunc("POST /api/packs", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.Posts = append(f.Posts, body)
		f.Agents = append(f.Agents, r.UserAgent())
		f.TestTokens = append(f.TestTokens, r.Header.Get("X-Kuticon-Test"))
		refuse, msg := f.Refuse, f.Message
		f.mu.Unlock()
		if refuse != 0 {
			if refuse == 503 {
				w.Header().Set("Retry-After", "120")
			}
			writeJSON(w, refuse, map[string]string{"error": msg})
			return
		}
		writeJSON(w, 202, map[string]any{"id": PackID, "status": "queued", "position": 3})
	})
	mux.HandleFunc("GET /api/packs/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != PackID {
			writeJSON(w, 404, map[string]string{"error": "That pack does not exist."})
			return
		}
		f.mu.Lock()
		i := f.polls
		if i >= len(f.Steps) {
			i = len(f.Steps) - 1
		}
		f.polls++
		step := f.Steps[i]
		f.mu.Unlock()
		writeJSON(w, 200, step)
	})
	mux.HandleFunc("GET /api/packs/{id}/download", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="kuticon-bakery.zip"`)
		w.Write(f.Zip)
	})
	mux.HandleFunc("GET /api/packs/{id}/svg/{icon}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Write([]byte(SVG(r.PathValue("icon"))))
	})
	f.Server = httptest.NewServer(mux)
	return f
}

// Polls is how many status reads the fake answered.
func (f *Fake) Polls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.polls
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// SetSteps swaps the status sequence and restarts it, safely under load.
func (f *Fake) SetSteps(steps []map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Steps, f.polls = steps, 0
}

// SetRefuse makes POST /api/packs answer code with msg. 0 accepts again.
func (f *Fake) SetRefuse(code int, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Refuse, f.Message = code, msg
}

// PostCount is how many packs were posted.
func (f *Fake) PostCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Posts)
}
