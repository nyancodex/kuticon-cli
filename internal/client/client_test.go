package client_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyancodex/kuticon-cli/internal/client"
	"github.com/nyancodex/kuticon-cli/internal/client/clienttest"
)

func instant(ctx context.Context, d time.Duration) error { return ctx.Err() }

func TestDrawFlow(t *testing.T) {
	f := clienttest.New()
	defer f.Close()
	c := client.New(f.URL, "test/1")
	c.Sleep = instant
	ctx := context.Background()
	job, err := c.Create(ctx, client.DrawRequest{Prompt: "bakery", Weight: "text", Cap: "round", Count: 4})
	if err != nil || job.ID != clienttest.PackID || job.Position != 3 {
		t.Fatalf("create = %+v, %v", job, err)
	}
	if f.Posts[0]["count"] != float64(4) || f.Posts[0]["style"] != nil || f.Agents[0] != "test/1" {
		t.Fatalf("post = %v %v", f.Posts[0], f.Agents)
	}
	var seen []string
	job, err = c.Wait(ctx, job.ID, func(j client.Job) { seen = append(seen, j.Status) })
	if err != nil || job.Status != "ready" || len(job.Icons) != 2 || job.Title != "Bakery" {
		t.Fatalf("wait = %+v, %v", job, err)
	}
	if len(seen) != 4 {
		t.Fatalf("progress = %v", seen)
	}
	var buf bytes.Buffer
	name, n, err := c.Download(ctx, job.ID, &buf)
	if err != nil || name != "kuticon-bakery.zip" || n != int64(len(f.Zip)) {
		t.Fatalf("download = %q %d %v", name, n, err)
	}
	svg, err := c.PackSVG(ctx, job.ID, "bread")
	if err != nil || svg != clienttest.SVG("bread") {
		t.Fatalf("svg = %q %v", svg, err)
	}
}

func TestErrors(t *testing.T) {
	f := clienttest.New()
	defer f.Close()
	c := client.New(f.URL, "")
	ctx := context.Background()
	f.SetRefuse(429, "Eight packs is the limit for this day.")
	_, err := c.Create(ctx, client.DrawRequest{Prompt: "x"})
	if !client.Limited(err) || err.Error() != f.Message {
		t.Fatalf("429 = %v", err)
	}
	f.SetRefuse(503, "The queue is full.")
	_, err = c.Create(ctx, client.DrawRequest{Prompt: "x"})
	var e *client.Error
	if !client.Busy(err) || !asErr(err, &e) || e.RetryAfter != 120*time.Second {
		t.Fatalf("503 = %#v", err)
	}
	if f.PostCount() != 2 {
		t.Fatalf("create retried: %d posts", len(f.Posts))
	}
	if _, err := c.IconSVG(ctx, "nope", ""); !client.NotFound(err) {
		t.Fatalf("404 = %v", err)
	}
	if _, err := c.IconPNG(ctx, "croissant", "", 999); err == nil || err.Error() != "Pixels must be 16, 24, 32, 48, 64, 128, or 256." {
		t.Fatalf("plain text error = %v", err)
	}
	if _, err := c.Status(ctx, "ffffffffffffffffffffffffffffffff"); !client.NotFound(err) {
		t.Fatalf("status 404 = %v", err)
	}
}

func asErr(err error, e **client.Error) bool {
	x, ok := err.(*client.Error)
	*e = x
	return ok
}

// A busy status read waits its Retry-After and does not give up.
func TestWaitBacksOff(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) <= 2 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(503)
			return
		}
		w.Write([]byte(`{"status":"ready","position":0,"title":"T","icons":[]}`))
	}))
	defer srv.Close()
	c := client.New(srv.URL, "")
	var waits []time.Duration
	c.Sleep = func(ctx context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	j, err := c.Wait(context.Background(), "abc", nil)
	if err != nil || j.Status != "ready" {
		t.Fatalf("wait = %+v %v", j, err)
	}
	if len(waits) != 2 || waits[0] != 7*time.Second {
		t.Fatalf("waits = %v", waits)
	}
}

func TestWaitHonoursContext(t *testing.T) {
	f := clienttest.New()
	defer f.Close()
	f.SetSteps(f.Steps[:1])
	c := client.New(f.URL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	j, err := c.Wait(ctx, clienttest.PackID, nil)
	if err != context.DeadlineExceeded || j.Status != "queued" {
		t.Fatalf("wait = %+v %v", j, err)
	}
}

func TestBaseURL(t *testing.T) {
	c := client.New("", "")
	if c.BaseURL != client.DefaultBaseURL || c.DownloadURL("a b") != "https://kuticon.com/api/packs/a%20b/download" {
		t.Fatal(c.BaseURL, c.DownloadURL("a b"))
	}
	if client.New("http://x/", "").IconPage("cup") != "http://x/icons/cup" {
		t.Fatal("trailing slash")
	}
}

func TestHTMLErrorPageIsNotPrinted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("<!DOCTYPE html>\n<html><body>Not here</body></html>\n"))
	}))
	defer srv.Close()
	c := client.New(srv.URL, "test")
	_, err := c.Meta(context.Background())
	if err == nil {
		t.Fatal("want an error")
	}
	msg := err.Error()
	if strings.Contains(msg, "<") || !strings.Contains(msg, "HTTP 404") || !strings.Contains(msg, "/api/meta") {
		t.Fatalf("message = %q", msg)
	}
	if !client.NotFound(err) {
		t.Fatal("want NotFound")
	}
}
