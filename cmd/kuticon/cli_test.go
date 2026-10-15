package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyancodex/kuticon-cli/internal/client/clienttest"
)

type result struct {
	code           int
	stdout, stderr string
}

func runCLI(t *testing.T, f *clienttest.Fake, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	e := env{
		stdin: strings.NewReader(""), stdout: &out, stderr: &errb,
		getenv: func(k string) string {
			if k == "KUTICON_URL" && f != nil {
				return f.URL
			}
			return ""
		},
		sleep: func(ctx context.Context, d time.Duration) error { return ctx.Err() },
	}
	code := run(context.Background(), args, e)
	return result{code, out.String(), errb.String()}
}

func TestHelpAndUsage(t *testing.T) {
	r := runCLI(t, nil)
	if r.code != exitUsage || !strings.Contains(r.stderr, "Commands:") {
		t.Fatalf("no args = %+v", r)
	}
	r = runCLI(t, nil, "help")
	if r.code != 0 || !strings.Contains(r.stdout, "Exit codes:") {
		t.Fatalf("help = %+v", r)
	}
	r = runCLI(t, nil, "draw", "--help")
	if r.code != 0 || !strings.Contains(r.stderr, "--resume") || !strings.Contains(r.stderr, "-style") {
		t.Fatalf("draw help = %+v", r)
	}
	if r := runCLI(t, nil, "frobnicate"); r.code != exitUsage {
		t.Fatalf("unknown = %+v", r)
	}
	if r := runCLI(t, nil, "draw"); r.code != exitUsage || !strings.Contains(r.stderr, "needs a sentence") {
		t.Fatalf("draw empty = %+v", r)
	}
	if r := runCLI(t, nil, "version"); r.code != 0 || !strings.HasPrefix(r.stdout, "kuticon ") {
		t.Fatalf("version = %+v", r)
	}
}

func TestDrawUnzips(t *testing.T) {
	f := clienttest.New()
	defer f.Close()
	dir := filepath.Join(t.TempDir(), "out")
	r := runCLI(t, f, "draw", "icons", "for", "a", "bakery", "--style", "neon", "--count", "4", "--formats", "svg,font", "--sizes", "24,64", "--out", dir)
	if r.code != 0 {
		t.Fatalf("draw = %+v", r)
	}
	p := f.Posts[0]
	if p["prompt"] != "icons for a bakery" || p["style"] != "neon" || p["count"] != float64(4) || len(p["sizes"].([]any)) != 2 || !strings.HasPrefix(f.Agents[0], "kuticon-cli/") {
		t.Fatalf("post = %v %v", p, f.Agents)
	}
	for _, want := range []string{"position 3", "position 2", "position 1", "drawing", "Ready: Bakery"} {
		if !strings.Contains(r.stderr, want) {
			t.Fatalf("progress missing %q:\n%s", want, r.stderr)
		}
	}
	if !strings.Contains(r.stdout, "Bakery — 2 icons") || !strings.Contains(r.stdout, "croissant") {
		t.Fatalf("stdout = %s", r.stdout)
	}
	b, err := os.ReadFile(filepath.Join(dir, "svg", "croissant.svg"))
	if err != nil || string(b) != clienttest.SVG("croissant") {
		t.Fatalf("unzipped = %q %v", b, err)
	}
}

func TestDrawJSONAndZip(t *testing.T) {
	f := clienttest.New()
	defer f.Close()
	dir := t.TempDir()
	r := runCLI(t, f, "draw", "bakery", "--json", "-q", "--zip", "--out", dir)
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("draw json = %+v", r)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &v); err != nil || v["id"] != clienttest.PackID || len(v["icons"].([]any)) != 2 {
		t.Fatalf("json = %s", r.stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "kuticon-bakery.zip")); err != nil {
		t.Fatal(err)
	}
}

func TestDrawRefusals(t *testing.T) {
	f := clienttest.New()
	defer f.Close()
	f.SetRefuse(429, "Eight packs is the limit for this day.")
	if r := runCLI(t, f, "draw", "x"); r.code != exitLimited || !strings.Contains(r.stderr, "limit for this day") {
		t.Fatalf("429 = %+v", r)
	}
	f.SetRefuse(503, "The queue is full. Try again in a few minutes.")
	if r := runCLI(t, f, "draw", "x", "--json"); r.code != exitLimited || !strings.Contains(r.stdout, `"exitCode": 4`) || !strings.Contains(r.stdout, "Retry after 120s") {
		t.Fatalf("503 = %+v", r)
	}
	f.SetRefuse(400, "Style must be outline, sticker, blueprint, neon, clay, or pixel.")
	if r := runCLI(t, f, "draw", "x", "--style", "chalk"); r.code != exitUsage || !strings.Contains(r.stderr, "Style must be") {
		t.Fatalf("400 = %+v", r)
	}
	if r := runCLI(t, f, "draw", "x", "--sizes", "big"); r.code != exitUsage {
		t.Fatalf("sizes = %+v", r)
	}
}

func TestDrawFailedAndTimeout(t *testing.T) {
	f := clienttest.New()
	defer f.Close()
	f.SetSteps([]map[string]any{{"status": "failed", "position": 0, "error": "The desk could not draw that pack."}})
	if r := runCLI(t, f, "draw", "x"); r.code != exitFailed || !strings.Contains(r.stderr, "could not draw") {
		t.Fatalf("failed = %+v", r)
	}
	f2 := clienttest.New()
	defer f2.Close()
	f2.SetSteps(f2.Steps[:1])
	r := runCLI(t, f2, "draw", "x", "--timeout", "30ms")
	if r.code != exitTimeout || !strings.Contains(r.stderr, "kuticon draw --resume "+clienttest.PackID) {
		t.Fatalf("timeout = %+v", r)
	}
	f2.SetSteps(clienttest.New().Steps)
	r = runCLI(t, f2, "draw", "--resume", clienttest.PackID, "--out", t.TempDir())
	if r.code != 0 || f2.PostCount() != 1 {
		t.Fatalf("resume = %+v posts=%d", r, len(f2.Posts))
	}
}

func TestSearch(t *testing.T) {
	f := clienttest.New()
	defer f.Close()
	r := runCLI(t, f, "search", "coffee")
	if r.code != 0 || !strings.Contains(r.stdout, "croissant") || !strings.Contains(r.stdout, "Bakery") {
		t.Fatalf("search = %+v", r)
	}
	if r := runCLI(t, f, "search", "zzz"); r.code != exitNotFound {
		t.Fatalf("no hits = %+v", r)
	}
	r = runCLI(t, f, "search", "--json", "zzz")
	if r.code != 0 || !strings.Contains(r.stdout, `"icons": []`) {
		t.Fatalf("json no hits = %+v", r)
	}
	if r := runCLI(t, f, "search"); r.code != exitUsage {
		t.Fatalf("empty = %+v", r)
	}
	// --base-url beats KUTICON_URL.
	if r := runCLI(t, f, "search", "coffee", "--base-url", "http://127.0.0.1:1"); r.code != exitError {
		t.Fatalf("base-url = %+v", r)
	}
}

func TestGet(t *testing.T) {
	f := clienttest.New()
	defer f.Close()
	r := runCLI(t, f, "get", "croissant", "--style", "neon")
	if r.code != 0 || r.stdout != clienttest.SVG("croissant-neon") {
		t.Fatalf("svg = %+v", r)
	}
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(cwd)
	r = runCLI(t, f, "get", "croissant", "--format", "png", "--size", "64")
	b, _ := os.ReadFile(filepath.Join(dir, "croissant-64.png"))
	if r.code != 0 || !bytes.Equal(b, clienttest.PNG) {
		t.Fatalf("png = %+v", r)
	}
	r = runCLI(t, f, "get", "croissant", "--json", "-o", "c.svg")
	if r.code != 0 || !strings.Contains(r.stdout, `"path": "c.svg"`) {
		t.Fatalf("json = %+v", r)
	}
	if r := runCLI(t, f, "get", "unicorn"); r.code != exitNotFound {
		t.Fatalf("404 = %+v", r)
	}
	if r := runCLI(t, f, "get", "croissant", "--format", "gif"); r.code != exitUsage {
		t.Fatalf("gif = %+v", r)
	}
	if r := runCLI(t, f, "get", "croissant", "--format", "png", "--size", "999"); r.code != exitUsage {
		t.Fatalf("size = %+v", r)
	}
}

func TestMeta(t *testing.T) {
	f := clienttest.New()
	defer f.Close()
	r := runCLI(t, f, "styles")
	if r.code != 0 || !strings.Contains(r.stdout, "neon       Glowing") || !strings.Contains(r.stdout, "weights") {
		t.Fatalf("styles = %+v", r)
	}
	r = runCLI(t, f, "formats", "--json")
	if r.code != 0 || !strings.Contains(r.stdout, `"font"`) {
		t.Fatalf("formats = %+v", r)
	}
}

func TestMCPCommand(t *testing.T) {
	f := clienttest.New()
	defer f.Close()
	var out bytes.Buffer
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_icon","arguments":{"id":"croissant"}}}` + "\n")
	e := env{stdin: in, stdout: &out, stderr: &bytes.Buffer{}, getenv: func(string) string { return f.URL }}
	if code := run(context.Background(), []string{"mcp"}, e); code != 0 {
		t.Fatal(code)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[1], "croissant-") {
		t.Fatalf("mcp = %s", out.String())
	}
}

func TestUnzipRefusesEscape(t *testing.T) {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, _ := zw.Create("../evil.txt")
	w.Write([]byte("x"))
	zw.Close()
	dir := t.TempDir()
	if _, err := unzip(b.Bytes(), filepath.Join(dir, "in")); err == nil {
		t.Fatal("zip slip accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.txt")); err == nil {
		t.Fatal("wrote outside")
	}
}

func TestTestToken(t *testing.T) {
	f := clienttest.New()
	defer f.Close()
	draw := func(vars map[string]string, args ...string) result {
		var out, errb bytes.Buffer
		e := env{
			stdin: strings.NewReader(""), stdout: &out, stderr: &errb,
			getenv: func(k string) string {
				if k == "KUTICON_URL" {
					return f.URL
				}
				return vars[k]
			},
			sleep: func(ctx context.Context, d time.Duration) error { return ctx.Err() },
		}
		code := run(context.Background(), append([]string{"draw", "bakery", "-q", "--zip", "--out", t.TempDir()}, args...), e)
		return result{code, out.String(), errb.String()}
	}
	const tok = "0123456789abcdef0123456789abcdef0123456789abcdef"
	if r := draw(nil); r.code != 0 {
		t.Fatal(r)
	}
	if r := draw(map[string]string{"KUTICON_TEST_TOKEN": tok}); r.code != 0 || strings.Contains(r.stdout+r.stderr, tok) {
		t.Fatal(r)
	}
	if r := draw(map[string]string{"KUTICON_TEST_TOKEN": "from-env"}, "--test-token", "from-flag"); r.code != 0 {
		t.Fatal(r)
	}
	want := []string{"", tok, "from-flag"}
	for i, w := range want {
		if f.TestTokens[i] != w {
			t.Fatalf("post %d test token %q, want %q", i, f.TestTokens[i], w)
		}
	}
	if r := runCLI(t, nil, "help"); strings.Contains(r.stdout, tok) || !strings.Contains(r.stdout, "--test-token") {
		t.Fatal("help")
	}
}
