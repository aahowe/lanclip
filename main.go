// LanClip — a tiny self-hosted LAN clipboard.
// Drop text, images or files on one device; pick them up on any other.
package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed web
var webFS embed.FS

const textLimit = 1 << 20 // 1 MiB of text per item

type Config struct {
	Addr     string
	DataDir  string
	TTL      time.Duration // 0 = keep forever
	MaxItems int
	MaxFile  int64
}

func loadConfig() Config {
	c := Config{
		Addr:     env("LANCLIP_ADDR", ":9501"),
		DataDir:  env("LANCLIP_DATA", "./data"),
		TTL:      7 * 24 * time.Hour,
		MaxItems: 200,
		MaxFile:  4 << 30,
	}
	if v := os.Getenv("LANCLIP_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			log.Fatalf("invalid LANCLIP_TTL %q: %v", v, err)
		}
		c.TTL = d
	}
	if v := os.Getenv("LANCLIP_MAX_ITEMS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			log.Fatalf("invalid LANCLIP_MAX_ITEMS %q", v)
		}
		c.MaxItems = n
	}
	if v := os.Getenv("LANCLIP_MAX_FILE_MB"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			log.Fatalf("invalid LANCLIP_MAX_FILE_MB %q", v)
		}
		c.MaxFile = n << 20
	}
	return c
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// ---------- store ----------

type Item struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"` // "text" | "file"
	Text    string    `json:"text,omitempty"`
	Name    string    `json:"name,omitempty"`
	Size    int64     `json:"size,omitempty"`
	Mime    string    `json:"mime,omitempty"`
	Created time.Time `json:"created"`
}

type Store struct {
	mu    sync.Mutex
	cfg   Config
	items []*Item // newest first
}

func (s *Store) indexPath() string         { return filepath.Join(s.cfg.DataDir, "items.json") }
func (s *Store) filesDir() string          { return filepath.Join(s.cfg.DataDir, "files") }
func (s *Store) filePath(id string) string { return filepath.Join(s.filesDir(), id) }

func openStore(cfg Config) (*Store, error) {
	s := &Store{cfg: cfg}
	if err := os.MkdirAll(s.filesDir(), 0o755); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(s.indexPath())
	if err == nil {
		if err := json.Unmarshal(b, &s.items); err != nil {
			return nil, fmt.Errorf("parse %s: %w", s.indexPath(), err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	// Drop index entries whose file vanished, and files nobody references.
	known := map[string]bool{}
	kept := s.items[:0]
	for _, it := range s.items {
		if it.Kind == "file" {
			if _, err := os.Stat(s.filePath(it.ID)); err != nil {
				continue
			}
		}
		known[it.ID] = true
		kept = append(kept, it)
	}
	s.items = kept
	entries, _ := os.ReadDir(s.filesDir())
	for _, e := range entries {
		if !known[e.Name()] {
			os.Remove(filepath.Join(s.filesDir(), e.Name()))
		}
	}
	return s, s.saveLocked()
}

func (s *Store) saveLocked() error {
	b, err := json.Marshal(s.items)
	if err != nil {
		return err
	}
	tmp := s.indexPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.indexPath())
}

func (s *Store) List() []*Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*Item(nil), s.items...)
}

func (s *Store) Get(id string) *Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.items {
		if it.ID == id {
			return it
		}
	}
	return nil
}

// Add prepends an item and returns ids evicted to stay under MaxItems.
func (s *Store) Add(it *Item) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append([]*Item{it}, s.items...)
	var evicted []string
	for len(s.items) > s.cfg.MaxItems {
		last := s.items[len(s.items)-1]
		s.items = s.items[:len(s.items)-1]
		s.dropFile(last)
		evicted = append(evicted, last.ID)
	}
	return evicted, s.saveLocked()
}

// Remove deletes items for which match returns true and returns their ids.
func (s *Store) Remove(match func(*Item) bool) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var removed []string
	kept := s.items[:0]
	for _, it := range s.items {
		if match(it) {
			s.dropFile(it)
			removed = append(removed, it.ID)
			continue
		}
		kept = append(kept, it)
	}
	s.items = kept
	if len(removed) == 0 {
		return nil, nil
	}
	return removed, s.saveLocked()
}

func (s *Store) dropFile(it *Item) {
	if it.Kind == "file" {
		os.Remove(s.filePath(it.ID))
	}
}

// ---------- realtime hub (SSE) ----------

type Hub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func (h *Hub) subscribe() chan []byte {
	c := make(chan []byte, 32)
	h.mu.Lock()
	h.subs[c] = struct{}{}
	h.mu.Unlock()
	h.presence()
	return c
}

func (h *Hub) unsubscribe(c chan []byte) {
	h.mu.Lock()
	delete(h.subs, c)
	h.mu.Unlock()
	h.presence()
}

func (h *Hub) presence() {
	h.mu.Lock()
	n := len(h.subs)
	h.mu.Unlock()
	h.publish(map[string]any{"type": "presence", "clients": n})
}

func (h *Hub) publish(ev any) {
	b, _ := json.Marshal(ev)
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.subs {
		select {
		case c <- b:
		default: // slow client; it will resync on reconnect
		}
	}
}

// ---------- http ----------

type App struct {
	cfg   Config
	store *Store
	hub   *Hub
}

func newID() string {
	b := make([]byte, 9)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (a *App) added(it *Item, evicted []string) {
	a.hub.publish(map[string]any{"type": "add", "item": it})
	if len(evicted) > 0 {
		a.hub.publish(map[string]any{"type": "remove", "ids": evicted})
	}
}

func (a *App) handleList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"items": a.store.List(),
		"config": map[string]any{
			"ttl":       int64(a.cfg.TTL / time.Second),
			"maxFile":   a.cfg.MaxFile,
			"maxItems":  a.cfg.MaxItems,
			"textLimit": textLimit,
		},
	})
}

func (a *App) handleText(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, textLimit+4096)).Decode(&body); err != nil {
		httpError(w, 400, "invalid request body")
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		httpError(w, 400, "text is empty")
		return
	}
	if len(body.Text) > textLimit {
		httpError(w, 413, "text too long")
		return
	}
	it := &Item{ID: newID(), Kind: "text", Text: body.Text, Created: time.Now()}
	evicted, err := a.store.Add(it)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	a.added(it, evicted)
	writeJSON(w, 201, it)
}

func (a *App) handleUpload(w http.ResponseWriter, r *http.Request) {
	name := sanitizeName(r.URL.Query().Get("name"))
	if r.ContentLength > a.cfg.MaxFile {
		httpError(w, 413, "file too large")
		return
	}
	tmp, err := os.CreateTemp(a.store.filesDir(), ".upload-*")
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	defer os.Remove(tmp.Name()) // no-op after successful rename
	n, err := io.Copy(tmp, http.MaxBytesReader(w, r.Body, a.cfg.MaxFile))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			httpError(w, 413, "file too large")
		} else {
			httpError(w, 400, "upload interrupted")
		}
		return
	}
	it := &Item{ID: newID(), Kind: "file", Name: name, Size: n, Mime: detectMime(name, tmp.Name()), Created: time.Now()}
	if err := os.Rename(tmp.Name(), a.store.filePath(it.ID)); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	evicted, err := a.store.Add(it)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	a.added(it, evicted)
	writeJSON(w, 201, it)
}

func sanitizeName(n string) string {
	n = strings.TrimSpace(filepath.Base(strings.ReplaceAll(n, "\\", "/")))
	n = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, n)
	if n == "" || n == "." || n == "/" {
		return "untitled"
	}
	if len(n) > 255 {
		ext := filepath.Ext(n)
		if len(ext) > 16 {
			ext = ""
		}
		n = strings.ToValidUTF8(n[:200], "") + ext
	}
	return n
}

func detectMime(name, path string) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
		return t
	}
	f, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()
	buf := make([]byte, 512)
	k, _ := io.ReadFull(f, buf)
	return http.DetectContentType(buf[:k])
}

// Types that are safe to render inline from our own origin.
func inlineSafe(m string) bool {
	m = strings.ToLower(m)
	switch {
	case m == "image/svg+xml":
		return false
	case strings.HasPrefix(m, "image/"), strings.HasPrefix(m, "video/"), strings.HasPrefix(m, "audio/"),
		m == "application/pdf", strings.HasPrefix(m, "text/plain"):
		return true
	}
	return false
}

func (a *App) handleFile(w http.ResponseWriter, r *http.Request) {
	it := a.store.Get(r.PathValue("id"))
	if it == nil || it.Kind != "file" {
		httpError(w, 404, "not found or expired")
		return
	}
	f, err := os.Open(a.store.filePath(it.ID))
	if err != nil {
		httpError(w, 404, "file missing")
		return
	}
	defer f.Close()
	disp := "attachment"
	if r.URL.Query().Get("dl") == "" && inlineSafe(it.Mime) {
		disp = "inline"
	}
	w.Header().Set("Content-Type", it.Mime)
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename*=UTF-8''%s", disp, url.PathEscape(it.Name)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, "", it.Created, f)
}

func (a *App) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	removed, err := a.store.Remove(func(it *Item) bool { return it.ID == id })
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	if len(removed) > 0 {
		a.hub.publish(map[string]any{"type": "remove", "ids": removed})
	}
	w.WriteHeader(204)
}

func (a *App) handleClear(w http.ResponseWriter, r *http.Request) {
	removed, err := a.store.Remove(func(*Item) bool { return true })
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	if len(removed) > 0 {
		a.hub.publish(map[string]any{"type": "remove", "ids": removed})
	}
	w.WriteHeader(204)
}

func (a *App) handleEvents(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	fmt.Fprint(w, "retry: 2000\n\n")
	if err := rc.Flush(); err != nil {
		return
	}
	c := a.hub.subscribe()
	defer a.hub.unsubscribe(c)
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case b := <-c:
			fmt.Fprintf(w, "data: %s\n\n", b)
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}

func (a *App) janitor() {
	if a.cfg.TTL <= 0 {
		return
	}
	for range time.Tick(time.Minute) {
		cutoff := time.Now().Add(-a.cfg.TTL)
		removed, err := a.store.Remove(func(it *Item) bool { return it.Created.Before(cutoff) })
		if err != nil {
			log.Printf("janitor: %v", err)
		}
		if len(removed) > 0 {
			a.hub.publish(map[string]any{"type": "remove", "ids": removed})
		}
	}
}

func (a *App) routes() http.Handler {
	static, _ := fs.Sub(webFS, "web")
	files := http.FileServerFS(static)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/items", a.handleList)
	mux.HandleFunc("POST /api/text", a.handleText)
	mux.HandleFunc("POST /api/files", a.handleUpload)
	mux.HandleFunc("GET /api/files/{id}", a.handleFile)
	mux.HandleFunc("DELETE /api/items/{id}", a.handleDelete)
	mux.HandleFunc("DELETE /api/items", a.handleClear)
	mux.HandleFunc("GET /api/events", a.handleEvents)
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	}))
	return mux
}

func newApp(cfg Config) (*App, error) {
	store, err := openStore(cfg)
	if err != nil {
		return nil, err
	}
	return &App{cfg: cfg, store: store, hub: &Hub{subs: map[chan []byte]struct{}{}}}, nil
}

var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-v" || os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Println("lanclip", version)
		return
	}
	cfg := loadConfig()
	app, err := newApp(cfg)
	if err != nil {
		log.Fatal(err)
	}
	go app.janitor()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           app.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()

	log.Printf("LanClip %s listening on %s (data: %s, ttl: %s, max items: %d, max file: %d MiB)",
		version, cfg.Addr, cfg.DataDir, cfg.TTL, cfg.MaxItems, cfg.MaxFile>>20)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
