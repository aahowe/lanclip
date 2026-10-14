package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testServer(t *testing.T, mutate func(*Config)) (*httptest.Server, *App) {
	t.Helper()
	cfg := Config{DataDir: t.TempDir(), TTL: time.Hour, MaxItems: 50, MaxFile: 1 << 20}
	if mutate != nil {
		mutate(&cfg)
	}
	app, err := newApp(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.routes())
	t.Cleanup(srv.Close)
	return srv, app
}

func postText(t *testing.T, base, text string) *http.Response {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"text": text})
	r, err := http.Post(base+"/api/text", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func upload(t *testing.T, base, name string, body []byte) (*http.Response, Item) {
	t.Helper()
	r, err := http.Post(base+"/api/files?name="+name, "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var it Item
	json.NewDecoder(r.Body).Decode(&it)
	r.Body.Close()
	return r, it
}

func listItems(t *testing.T, base string) []Item {
	t.Helper()
	r, err := http.Get(base + "/api/items")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var out struct{ Items []Item }
	json.NewDecoder(r.Body).Decode(&out)
	return out.Items
}

func TestTextRoundTrip(t *testing.T) {
	srv, _ := testServer(t, nil)
	if r := postText(t, srv.URL, "hello 世界"); r.StatusCode != 201 {
		t.Fatalf("status %d", r.StatusCode)
	}
	if r := postText(t, srv.URL, "   \n"); r.StatusCode != 400 {
		t.Fatalf("blank text: status %d, want 400", r.StatusCode)
	}
	if r := postText(t, srv.URL, strings.Repeat("x", textLimit+1)); r.StatusCode != 413 && r.StatusCode != 400 {
		t.Fatalf("oversized text: status %d", r.StatusCode)
	}
	items := listItems(t, srv.URL)
	if len(items) != 1 || items[0].Text != "hello 世界" {
		t.Fatalf("items = %+v", items)
	}
}

func TestFileRoundTripAndRange(t *testing.T) {
	srv, _ := testServer(t, nil)
	data := []byte("0123456789abcdef")
	r, it := upload(t, srv.URL, "%E6%8A%A5%E5%91%8A.txt", data)
	if r.StatusCode != 201 || it.Name != "报告.txt" || it.Size != int64(len(data)) {
		t.Fatalf("upload: %d %+v", r.StatusCode, it)
	}

	resp, _ := http.Get(srv.URL + "/api/files/" + it.ID + "?dl=1")
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(got, data) {
		t.Fatalf("download mismatch: %q", got)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") || !strings.Contains(cd, "%E6%8A%A5") {
		t.Fatalf("Content-Disposition = %q", cd)
	}

	req, _ := http.NewRequest("GET", srv.URL+"/api/files/"+it.ID, nil)
	req.Header.Set("Range", "bytes=10-")
	resp, _ = http.DefaultClient.Do(req)
	got, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || string(got) != "abcdef" {
		t.Fatalf("range: %d %q", resp.StatusCode, got)
	}
}

func TestUnsafeTypesAreNotInline(t *testing.T) {
	srv, _ := testServer(t, nil)
	for name, want := range map[string]string{
		"a.png":  "inline",
		"a.html": "attachment",
		"a.svg":  "attachment",
	} {
		_, it := upload(t, srv.URL, name, []byte("<script>alert(1)</script>"))
		resp, _ := http.Get(srv.URL + "/api/files/" + it.ID)
		resp.Body.Close()
		if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, want) {
			t.Errorf("%s: Content-Disposition = %q, want %s", name, cd, want)
		}
		if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: missing nosniff", name)
		}
	}
}

func TestFileTooLarge(t *testing.T) {
	srv, app := testServer(t, func(c *Config) { c.MaxFile = 10 })
	r, _ := upload(t, srv.URL, "big.bin", make([]byte, 11))
	if r.StatusCode != 413 {
		t.Fatalf("status %d, want 413", r.StatusCode)
	}
	entries, _ := os.ReadDir(app.store.filesDir())
	if len(entries) != 0 {
		t.Fatalf("leftover files: %v", entries)
	}
}

func TestEvictionAndDelete(t *testing.T) {
	srv, app := testServer(t, func(c *Config) { c.MaxItems = 2 })
	_, first := upload(t, srv.URL, "first.bin", []byte("1"))
	postText(t, srv.URL, "second")
	postText(t, srv.URL, "third")

	items := listItems(t, srv.URL)
	if len(items) != 2 || items[0].Text != "third" {
		t.Fatalf("items = %+v", items)
	}
	if _, err := os.Stat(app.store.filePath(first.ID)); !os.IsNotExist(err) {
		t.Fatal("evicted file still on disk")
	}

	req, _ := http.NewRequest("DELETE", srv.URL+"/api/items/"+items[0].ID, nil)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 204 || len(listItems(t, srv.URL)) != 1 {
		t.Fatal("delete failed")
	}
	req, _ = http.NewRequest("DELETE", srv.URL+"/api/items", nil)
	http.DefaultClient.Do(req)
	if n := len(listItems(t, srv.URL)); n != 0 {
		t.Fatalf("clear left %d items", n)
	}
}

func TestPersistenceAndOrphanCleanup(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{DataDir: dir, TTL: time.Hour, MaxItems: 10, MaxFile: 1 << 20}
	app, _ := newApp(cfg)
	srv := httptest.NewServer(app.routes())
	_, it := upload(t, srv.URL, "keep.txt", []byte("keep"))
	postText(t, srv.URL, "note")
	srv.Close()

	orphan := filepath.Join(dir, "files", "orphan")
	os.WriteFile(orphan, []byte("x"), 0o644)

	app2, err := newApp(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(app2.store.List()); n != 2 {
		t.Fatalf("reloaded %d items, want 2", n)
	}
	if app2.store.Get(it.ID) == nil {
		t.Fatal("file item lost after reload")
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("orphan file not removed")
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"":                 "untitled",
		"../../etc/passwd": "passwd",
		`C:\Users\a\b.txt`: "b.txt",
		"  spaced.txt  ":   "spaced.txt",
		"tab\there.txt":    "tabhere.txt",
		"/":                "untitled",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("长", 100) + ".pdf" // 304 bytes
	got := sanitizeName(long)
	if len(got) > 255 || !strings.HasSuffix(got, ".pdf") || !strings.ContainsRune(got, '长') {
		t.Errorf("long name = %q", got)
	}
	for _, r := range got {
		if r == '\uFFFD' {
			t.Errorf("long name has broken rune: %q", got)
		}
	}
}
