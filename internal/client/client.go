// Package client calls the public Kuticon HTTP API. The CLI and the MCP
// server use it; it holds no server logic, only requests and polling.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the public site.
const DefaultBaseURL = "https://kuticon.com"

// Client talks to one Kuticon site.
type Client struct {
	BaseURL   string
	HTTP      *http.Client
	UserAgent string
	// Sleep waits between polls. Tests swap it; nil means a context-aware sleep.
	Sleep func(ctx context.Context, d time.Duration) error
}

// New is a Client for base, or DefaultBaseURL when base is empty.
func New(base, userAgent string) *Client {
	if base == "" {
		base = DefaultBaseURL
	}
	return &Client{
		BaseURL:   strings.TrimRight(base, "/"),
		HTTP:      &http.Client{Timeout: 60 * time.Second},
		UserAgent: userAgent,
	}
}

// Error is a refusal from the site, with its own sentence.
type Error struct {
	Status     int
	Message    string
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("kuticon: HTTP %d", e.Status)
	}
	return e.Message
}

// NotFound, Limited, and Busy sort an error for exit codes and retries.
func NotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}
func Limited(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusTooManyRequests
}
func Busy(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusServiceUnavailable
}

// Style, Format and Meta mirror GET /api/meta.
type Style struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

type Format struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Default     bool   `json:"default"`
}

type Meta struct {
	Styles       []Style  `json:"styles"`
	Weights      []string `json:"weights"`
	Corners      []string `json:"corners"`
	Counts       []int    `json:"counts"`
	Formats      []Format `json:"formats"`
	Sizes        []int    `json:"sizes"`
	DailyLimit   int      `json:"dailyLimit"`
	PromptMaxLen int      `json:"promptMaxLen"`
}

// Hit and Search mirror GET /api/icons.
type Hit struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Pack     string `json:"pack"`
	PackName string `json:"packName"`
	Category string `json:"category"`
	Page     string `json:"page"`
	SVG      string `json:"svg"`
}

type Search struct {
	Query  string `json:"query"`
	Total  int    `json:"total"`
	Offset int    `json:"offset"`
	Icons  []Hit  `json:"icons"`
}

// DrawRequest is the POST /api/packs body. Empty fields take the site defaults.
type DrawRequest struct {
	Prompt  string   `json:"prompt"`
	Weight  string   `json:"weight"`
	Cap     string   `json:"cap"`
	Style   string   `json:"style,omitempty"`
	Count   int      `json:"count,omitempty"`
	Formats []string `json:"formats,omitempty"`
	Sizes   []int    `json:"sizes,omitempty"`
}

// Item is one drawn icon.
type Item struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Job is POST /api/packs and GET /api/packs/{id}.
type Job struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Position int    `json:"position"`
	Error    string `json:"error,omitempty"`
	Title    string `json:"title,omitempty"`
	Icons    []Item `json:"icons,omitempty"`
}

// Done is true for ready and failed.
func (j Job) Done() bool { return j.Status == "ready" || j.Status == "failed" }

func (c *Client) url(path string, q url.Values) string {
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

func (c *Client) do(ctx context.Context, method, u string, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 400 {
		defer res.Body.Close()
		return nil, readError(res)
	}
	return res, nil
}

func readError(res *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	e := &Error{Status: res.StatusCode}
	var j struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &j) == nil && j.Error != "" {
		e.Message = j.Error
	} else if t := strings.TrimSpace(string(b)); t != "" && !strings.HasPrefix(t, "<") && len(t) <= 300 && !strings.Contains(t, "\n") {
		e.Message = t
	}
	if e.Message == "" {
		// No JSON sentence (an HTML error page, or nothing): say what failed
		// instead of printing the page.
		e.Message = fmt.Sprintf("HTTP %d %s", res.StatusCode, http.StatusText(res.StatusCode))
		if res.Request != nil && res.Request.URL != nil {
			e.Message += " from " + res.Request.Method + " " + res.Request.URL.Redacted()
		}
		if res.StatusCode == http.StatusNotFound && res.Request != nil && res.Request.URL != nil && !strings.HasPrefix(res.Request.URL.Path, "/api/packs/") {
			e.Message += ". This site may not have this API yet; check --base-url or KUTICON_URL."
		}
	}
	if s, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && s > 0 {
		e.RetryAfter = time.Duration(s) * time.Second
	}
	return e
}

func (c *Client) getJSON(ctx context.Context, u string, v any) error {
	res, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return json.NewDecoder(res.Body).Decode(v)
}

func (c *Client) getBytes(ctx context.Context, u string, max int64) ([]byte, error) {
	res, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, max+1))
	if err == nil && int64(len(b)) > max {
		return nil, fmt.Errorf("kuticon: answer larger than %d bytes", max)
	}
	return b, err
}

// Meta is every choice the site accepts.
func (c *Client) Meta(ctx context.Context) (Meta, error) {
	var m Meta
	return m, c.getJSON(ctx, c.url("/api/meta", nil), &m)
}

// Search finds library icons. limit 0 is the site default.
func (c *Client) Search(ctx context.Context, query string, limit, offset int) (Search, error) {
	q := url.Values{"q": {query}}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	var s Search
	return s, c.getJSON(ctx, c.url("/api/icons", q), &s)
}

func styleQuery(look string) url.Values {
	if look == "" || look == "outline" {
		return nil
	}
	return url.Values{"style": {look}}
}

// IconSVG is one library icon as SVG text.
func (c *Client) IconSVG(ctx context.Context, id, look string) (string, error) {
	b, err := c.getBytes(ctx, c.url("/icons/"+url.PathEscape(id)+"/svg", styleQuery(look)), 1<<20)
	return string(b), err
}

// IconPNG is one library icon as PNG bytes. size 0 is 256.
func (c *Client) IconPNG(ctx context.Context, id, look string, size int) ([]byte, error) {
	q := styleQuery(look)
	if q == nil {
		q = url.Values{}
	}
	if size > 0 {
		q.Set("size", strconv.Itoa(size))
	}
	return c.getBytes(ctx, c.url("/icons/"+url.PathEscape(id)+"/png", q), 8<<20)
}

// IconPage is the public page of a library icon.
func (c *Client) IconPage(id string) string { return c.url("/icons/"+url.PathEscape(id), nil) }

// PackPage, DownloadURL and PackSVGURL are the addresses of a drawn pack.
func (c *Client) PackPage(id string) string { return c.url("/p/"+url.PathEscape(id), nil) }
func (c *Client) DownloadURL(id string) string {
	return c.url("/api/packs/"+url.PathEscape(id)+"/download", nil)
}
func (c *Client) PackSVGURL(id, icon string) string {
	return c.url("/api/packs/"+url.PathEscape(id)+"/svg/"+url.PathEscape(icon), nil)
}

// Create queues one pack. It never retries: a second post could count twice
// against the daily limit.
func (c *Client) Create(ctx context.Context, r DrawRequest) (Job, error) {
	res, err := c.do(ctx, http.MethodPost, c.url("/api/packs", nil), r)
	if err != nil {
		return Job{}, err
	}
	defer res.Body.Close()
	var j Job
	return j, json.NewDecoder(res.Body).Decode(&j)
}

// Status reads one job.
func (c *Client) Status(ctx context.Context, id string) (Job, error) {
	var j Job
	err := c.getJSON(ctx, c.url("/api/packs/"+url.PathEscape(id), nil), &j)
	j.ID = id
	return j, err
}

// Wait polls a job until it is ready or failed, or ctx ends. Polls start at
// every 2s and slow to every 5s; a 429 or 503 waits its Retry-After; up to
// five network errors in a row are retried. progress sees every change.
func (c *Client) Wait(ctx context.Context, id string, progress func(Job)) (Job, error) {
	sleep := c.Sleep
	if sleep == nil {
		sleep = Sleep
	}
	every := 2 * time.Second
	var last Job
	fails := 0
	for n := 0; ; n++ {
		j, err := c.Status(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				return last, ctx.Err()
			}
			var e *Error
			switch {
			case errors.As(err, &e) && (e.Status == 429 || e.Status == 503):
				wait := e.RetryAfter
				if wait <= 0 {
					wait = 10 * time.Second
				}
				if err := sleep(ctx, wait); err != nil {
					return last, err
				}
				continue
			case errors.As(err, &e):
				return last, err
			}
			fails++
			if fails > 5 {
				return last, err
			}
			if err := sleep(ctx, every); err != nil {
				return last, err
			}
			continue
		}
		fails = 0
		if progress != nil && (n == 0 || j.Status != last.Status || j.Position != last.Position) {
			progress(j)
		}
		last = j
		if j.Done() {
			return j, nil
		}
		if n >= 10 {
			every = 5 * time.Second
		}
		if err := sleep(ctx, every); err != nil {
			return last, err
		}
	}
}

// Sleep waits d or until ctx ends.
func Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Download writes the pack zip to w and returns its file name from the site.
func (c *Client) Download(ctx context.Context, id string, w io.Writer) (string, int64, error) {
	res, err := c.do(ctx, http.MethodGet, c.DownloadURL(id), nil)
	if err != nil {
		return "", 0, err
	}
	defer res.Body.Close()
	name := "kuticon-" + id + ".zip"
	if _, p, err := mime.ParseMediaType(res.Header.Get("Content-Disposition")); err == nil && p["filename"] != "" {
		name = p["filename"]
	}
	n, err := io.Copy(w, io.LimitReader(res.Body, 512<<20))
	return name, n, err
}

// PackSVG is one drawn icon's SVG master.
func (c *Client) PackSVG(ctx context.Context, id, icon string) (string, error) {
	b, err := c.getBytes(ctx, c.PackSVGURL(id, icon), 1<<20)
	return string(b), err
}
