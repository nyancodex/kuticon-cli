package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nyancodex/kuticon-cli/internal/client"
	"github.com/nyancodex/kuticon-cli/internal/mcp"
	"github.com/nyancodex/kuticon-cli/internal/version"
)

// Exit codes.
const (
	exitOK       = 0
	exitError    = 1 // network, server, or file error
	exitUsage    = 2 // bad command line or a request the site refused as invalid
	exitNotFound = 3 // no such icon or pack
	exitLimited  = 4 // daily limit reached or queue full; try later
	exitFailed   = 5 // the pack was queued but failed to draw
	exitTimeout  = 6 // gave up waiting; the pack may still finish
)

type env struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	getenv         func(string) string
	tty, outTTY    bool
	// sleep replaces poll waits in tests.
	sleep func(ctx context.Context, d time.Duration) error
}

const usageText = `kuticon draws free CC0 icon packs from one sentence, and searches the Kuticon icon library.

Usage:
  kuticon <command> [arguments] [flags]

Commands:
  draw "sentence"   Draw a new pack, wait for it, download and unzip it
  search <query>    Search library icons
  get <id>          Download one library icon as SVG or PNG
  styles            List drawing styles, weights, and corners
  formats           List file formats and PNG sizes
  mcp               Run the MCP server on stdin/stdout (for AI assistants)
  version           Print the version

Global flags (any command):
  --base-url URL    Site to talk to (default $KUTICON_URL, else https://kuticon.com)
  --json            Print machine-readable JSON on stdout
  -q, --quiet       No progress on stderr

Examples:
  kuticon draw "icons for a neighborhood bakery" --style sticker --count 8 --out ./bakery
  kuticon search "coffee cup"
  kuticon get cupcake --style neon --format png --size 128 -o cupcake.png
  kuticon styles --json

Exit codes:
  0 ok · 1 error · 2 usage or invalid request · 3 not found
  4 daily limit or queue full · 5 pack failed · 6 timed out waiting

Run "kuticon <command> --help" for the flags of one command.
`

type globals struct {
	base  string
	json  bool
	quiet bool
}

func (g *globals) bind(fs *flag.FlagSet, e env) {
	def := e.getenv("KUTICON_URL")
	if def == "" {
		def = client.DefaultBaseURL
	}
	fs.StringVar(&g.base, "base-url", def, "site base URL (env KUTICON_URL)")
	fs.BoolVar(&g.json, "json", false, "print JSON on stdout")
	fs.BoolVar(&g.quiet, "quiet", false, "no progress on stderr")
	fs.BoolVar(&g.quiet, "q", false, "short for --quiet")
}

func (g *globals) client(e env) *client.Client {
	c := client.New(g.base, "kuticon-cli/"+version.Version)
	c.Sleep = e.sleep
	return c
}

// parse lets flags come before or after positional arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		if rest[0] == "--" {
			return append(pos, rest[1:]...), nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

func newFlags(name, usage string, e env) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	fs.Usage = func() {
		fmt.Fprint(e.stderr, usage)
		fmt.Fprintln(e.stderr, "\nFlags:")
		fs.PrintDefaults()
	}
	return fs
}

func run(ctx context.Context, args []string, e env) int {
	if len(args) == 0 {
		fmt.Fprint(e.stderr, usageText)
		return exitUsage
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		if len(rest) > 0 {
			return run(ctx, []string{rest[0], "--help"}, e)
		}
		fmt.Fprint(e.stdout, usageText)
		return exitOK
	case "version", "--version", "-version":
		fmt.Fprintf(e.stdout, "kuticon %s\n", version.String())
		return exitOK
	case "draw":
		return cmdDraw(ctx, rest, e)
	case "search":
		return cmdSearch(ctx, rest, e)
	case "get":
		return cmdGet(ctx, rest, e)
	case "styles":
		return cmdMeta(ctx, rest, e, true)
	case "formats":
		return cmdMeta(ctx, rest, e, false)
	case "mcp":
		return cmdMCP(ctx, rest, e)
	}
	fmt.Fprintf(e.stderr, "kuticon: unknown command %q\n\n%s", cmd, usageText)
	return exitUsage
}

// fail prints err and maps it to an exit code.
func fail(e env, g *globals, err error) int {
	code := exitError
	var ae *client.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		code = exitTimeout
	case errors.Is(err, context.Canceled):
		code = exitError
	case client.NotFound(err):
		code = exitNotFound
	case client.Limited(err), client.Busy(err):
		code = exitLimited
	case errors.As(err, &ae) && ae.Status == 400:
		code = exitUsage
	}
	msg := err.Error()
	if errors.As(err, &ae) && ae.RetryAfter > 0 {
		msg += fmt.Sprintf(" Retry after %ds.", int(ae.RetryAfter.Seconds()))
	}
	if g != nil && g.json {
		writeJSON(e.stdout, map[string]any{"error": msg, "exitCode": code})
	} else {
		fmt.Fprintln(e.stderr, "kuticon: "+msg)
	}
	return code
}

func usageFail(e env, fs *flag.FlagSet, msg string) int {
	fmt.Fprintln(e.stderr, "kuticon: "+msg)
	fmt.Fprintf(e.stderr, "Run \"kuticon %s --help\" for usage.\n", fs.Name())
	return exitUsage
}

func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		out = append(out, strings.ToLower(p))
	}
	return out
}

// ---- draw ----

const drawUsage = `Usage: kuticon draw "sentence" [flags]

Queue a new pack on the site, show its place in the queue, then download the
zip and unzip it into --out. Each draw counts against the site's daily limit
per network. A pack is never posted twice: if waiting times out, run
"kuticon draw --resume <id>" to keep waiting for the same pack.
`

func cmdDraw(ctx context.Context, args []string, e env) int {
	var g globals
	fs := newFlags("draw", drawUsage, e)
	g.bind(fs, e)
	look := fs.String("style", "outline", "outline, sticker, blueprint, neon, clay, or pixel")
	weight := fs.String("weight", "text", "fine, text, or bold")
	corner := fs.String("corner", "round", "round or sharp")
	count := fs.Int("count", 12, "icons: 1, 4, 6, 8, 12, 16, or 24")
	formats := fs.String("formats", "svg,png", "comma list of svg, png, font, react, vue, sprite, app")
	sizes := fs.String("sizes", "", "comma list of PNG sides from 16,24,32,48,64,128,256 (default all)")
	out := fs.String("out", "", "directory to unzip into (default ./kuticon-{name})")
	zipOnly := fs.Bool("zip", false, "save the zip into --out instead of unzipping it")
	resume := fs.String("resume", "", "wait for an existing pack id instead of drawing a new one")
	timeout := fs.Duration("timeout", 10*time.Minute, "how long to wait for the pack")
	pos, err := parse(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil {
		return exitUsage
	}
	prompt := strings.TrimSpace(strings.Join(pos, " "))
	if prompt == "" && *resume == "" {
		return usageFail(e, fs, "draw needs a sentence, for example: kuticon draw \"icons for a bakery\"")
	}
	req := client.DrawRequest{Prompt: prompt, Weight: *weight, Cap: *corner, Style: *look, Count: *count, Formats: splitList(*formats)}
	for _, s := range splitList(*sizes) {
		n, err := strconv.Atoi(s)
		if err != nil {
			return usageFail(e, fs, "--sizes must be numbers, for example 24,64,256")
		}
		req.Sizes = append(req.Sizes, n)
	}
	c := g.client(e)
	p := newProgress(e, g.quiet)
	defer p.stop()

	id := *resume
	if id == "" {
		job, err := c.Create(ctx, req)
		if err != nil {
			p.stop()
			return fail(e, &g, err)
		}
		id = job.ID
		p.log("Queued pack %s.", id)
		p.set(job)
	} else {
		p.log("Waiting for pack %s.", id)
	}
	wctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	job, err := c.Wait(wctx, id, p.set)
	if err != nil {
		p.stop()
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			fmt.Fprintf(e.stderr, "kuticon: still %s after %s. Keep waiting with: kuticon draw --resume %s\n", orStr(job.Status, "queued"), *timeout, id)
			if g.json {
				writeJSON(e.stdout, map[string]any{"id": id, "status": orStr(job.Status, "queued"), "exitCode": exitTimeout})
			}
			return exitTimeout
		}
		return fail(e, &g, err)
	}
	if job.Status == "failed" {
		p.stop()
		msg := orStr(job.Error, "The desk could not draw that pack.")
		if g.json {
			writeJSON(e.stdout, map[string]any{"id": id, "status": "failed", "error": msg, "exitCode": exitFailed})
		} else {
			fmt.Fprintln(e.stderr, "kuticon: the pack failed: "+msg)
		}
		return exitFailed
	}
	p.log("Ready: %s, %d icons. Downloading.", job.Title, len(job.Icons))
	var buf bytes.Buffer
	name, size, err := c.Download(ctx, id, &buf)
	if err != nil {
		p.stop()
		return fail(e, &g, err)
	}
	name = filepath.Base(name)
	dir := *out
	if dir == "" {
		dir = strings.TrimSuffix(name, ".zip")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		p.stop()
		return fail(e, &g, err)
	}
	var files []string
	if *zipOnly {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			p.stop()
			return fail(e, &g, err)
		}
		files = []string{path}
	} else if files, err = unzip(buf.Bytes(), dir); err != nil {
		p.stop()
		return fail(e, &g, err)
	}
	p.stop()
	if g.json {
		icons := job.Icons
		if icons == nil {
			icons = []client.Item{}
		}
		writeJSON(e.stdout, map[string]any{
			"id": id, "status": job.Status, "title": job.Title, "icons": icons,
			"dir": dir, "files": len(files), "zipBytes": size,
			"downloadUrl": c.DownloadURL(id), "pageUrl": c.PackPage(id),
		})
		return exitOK
	}
	fmt.Fprintf(e.stdout, "%s — %d icons\n", job.Title, len(job.Icons))
	for _, it := range job.Icons {
		fmt.Fprintf(e.stdout, "  %-22s %s\n", it.ID, it.Name)
	}
	what := fmt.Sprintf("%d files", len(files))
	if *zipOnly {
		what = files[0]
	}
	fmt.Fprintf(e.stdout, "Saved %s (%s zip) in %s\nPage: %s\n", what, humanBytes(size), dir, c.PackPage(id))
	return exitOK
}

func orStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// unzip writes every file of the zip under dir and refuses paths that leave it.
func unzip(b []byte, dir string) ([]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, fmt.Errorf("the download is not a zip: %w", err)
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	var total int64
	for _, f := range zr.File {
		clean := filepath.Clean(filepath.FromSlash(f.Name))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("the zip has an unsafe path %q", f.Name)
		}
		dest := filepath.Join(root, clean)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return nil, err
			}
			continue
		}
		if !f.Mode().IsRegular() {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, err
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		w, err := os.Create(dest)
		if err != nil {
			rc.Close()
			return nil, err
		}
		n, err := io.Copy(w, io.LimitReader(rc, 1<<30-total))
		total += n
		rc.Close()
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, err
		}
		files = append(files, filepath.Join(dir, clean))
	}
	return files, nil
}

// progress draws one live line on a terminal, or one line per change otherwise.
type progress struct {
	e       env
	quiet   bool
	mu      sync.Mutex
	start   time.Time
	last    string
	done    chan struct{}
	stopped bool
	frame   int
}

func newProgress(e env, quiet bool) *progress {
	p := &progress{e: e, quiet: quiet, start: time.Now(), done: make(chan struct{})}
	if e.tty && !quiet {
		go func() {
			t := time.NewTicker(120 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					p.mu.Lock()
					p.frame++
					p.paint()
					p.mu.Unlock()
				case <-p.done:
					return
				}
			}
		}()
	}
	return p
}

var spin = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (p *progress) paint() {
	if p.last == "" || p.stopped {
		return
	}
	el := time.Since(p.start).Truncate(time.Second)
	fmt.Fprintf(p.e.stderr, "\r\033[K%s %s · %s", spin[p.frame%len(spin)], p.last, el)
}

func (p *progress) set(j client.Job) {
	if p.quiet {
		return
	}
	var s string
	switch j.Status {
	case "queued":
		if j.Position > 0 {
			s = fmt.Sprintf("queued · position %d", j.Position)
		} else {
			s = "queued · next up"
		}
	case "drawing":
		s = "drawing"
	default:
		s = j.Status
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if s == p.last {
		return
	}
	p.last = s
	if p.e.tty {
		p.paint()
	} else {
		fmt.Fprintf(p.e.stderr, "  %s\n", s)
	}
}

func (p *progress) log(format string, a ...any) {
	if p.quiet {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.e.tty && p.last != "" && !p.stopped {
		fmt.Fprint(p.e.stderr, "\r\033[K")
	}
	fmt.Fprintf(p.e.stderr, format+"\n", a...)
	if p.e.tty {
		p.paint()
	}
}

func (p *progress) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	p.stopped = true
	close(p.done)
	if p.e.tty && p.last != "" && !p.quiet {
		fmt.Fprint(p.e.stderr, "\r\033[K")
	}
}

// ---- search ----

const searchUsage = `Usage: kuticon search <query> [flags]

Search the library. One word also matches inside longer words ("bit" finds
bitcoin); several words match any whole word.
`

func cmdSearch(ctx context.Context, args []string, e env) int {
	var g globals
	fs := newFlags("search", searchUsage, e)
	g.bind(fs, e)
	limit := fs.Int("limit", 20, "most results, 1 to 100")
	offset := fs.Int("offset", 0, "skip this many results")
	pos, err := parse(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil {
		return exitUsage
	}
	q := strings.TrimSpace(strings.Join(pos, " "))
	if q == "" {
		return usageFail(e, fs, "search needs a query, for example: kuticon search coffee")
	}
	if *limit < 1 || *limit > 100 {
		return usageFail(e, fs, "--limit must be 1 to 100")
	}
	res, err := g.client(e).Search(ctx, q, *limit, *offset)
	if err != nil {
		return fail(e, &g, err)
	}
	if g.json {
		if res.Icons == nil {
			res.Icons = []client.Hit{}
		}
		writeJSON(e.stdout, res)
		return exitOK
	}
	if res.Total == 0 {
		fmt.Fprintf(e.stderr, "No library icons match %q. Try kuticon draw.\n", q)
		return exitNotFound
	}
	w := 4
	for _, h := range res.Icons {
		if len(h.ID) > w {
			w = len(h.ID)
		}
	}
	fmt.Fprintf(e.stdout, "%-*s  %-24s %s\n", w, "ID", "NAME", "PACK")
	for _, h := range res.Icons {
		fmt.Fprintf(e.stdout, "%-*s  %-24s %s\n", w, h.ID, clip(h.Name, 24), h.PackName)
	}
	if shown := res.Offset + len(res.Icons); shown < res.Total {
		fmt.Fprintf(e.stderr, "%d of %d. More with --offset %d.\n", shown, res.Total, shown)
	} else if !g.quiet {
		fmt.Fprintf(e.stderr, "%d match%s.\n", res.Total, map[bool]string{true: "", false: "es"}[res.Total == 1])
	}
	return exitOK
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// ---- get ----

const getUsage = `Usage: kuticon get <id> [flags]

Download one library icon. SVG goes to stdout unless -o is set; PNG goes to
{id}-{size}.png unless -o is set ("-o -" writes to stdout).
`

func cmdGet(ctx context.Context, args []string, e env) int {
	var g globals
	fs := newFlags("get", getUsage, e)
	g.bind(fs, e)
	look := fs.String("style", "outline", "outline, sticker, blueprint, neon, clay, or pixel")
	format := fs.String("format", "svg", "svg or png")
	size := fs.Int("size", 256, "PNG side: 16, 24, 32, 48, 64, 128, or 256")
	out := fs.String("o", "", "output file, or - for stdout")
	fs.StringVar(out, "out", "", "same as -o")
	pos, err := parse(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil {
		return exitUsage
	}
	if len(pos) != 1 {
		return usageFail(e, fs, "get needs one icon id, for example: kuticon get cupcake")
	}
	id := strings.ToLower(pos[0])
	c := g.client(e)
	var data []byte
	notFound := func(err error) int {
		if client.NotFound(err) {
			return fail(e, &g, &client.Error{Status: 404, Message: fmt.Sprintf("No library icon has the id %q. Try: kuticon search %s", id, id)})
		}
		return fail(e, &g, err)
	}
	switch strings.ToLower(*format) {
	case "svg":
		s, err := c.IconSVG(ctx, id, *look)
		if err != nil {
			return notFound(err)
		}
		data = []byte(s)
	case "png":
		if *out == "" {
			name := id
			if *look != "" && *look != "outline" {
				name += "-" + *look
			}
			*out = fmt.Sprintf("%s-%d.png", name, *size)
		}
		data, err = c.IconPNG(ctx, id, *look, *size)
		if err != nil {
			return notFound(err)
		}
	default:
		return usageFail(e, fs, "--format must be svg or png")
	}
	path := *out
	if path != "" && path != "-" {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return fail(e, &g, err)
		}
	}
	if g.json {
		v := map[string]any{"id": id, "style": orStr(*look, "outline"), "format": strings.ToLower(*format), "bytes": len(data), "page": c.IconPage(id)}
		if *format == "png" {
			v["size"] = *size
		}
		if path != "" && path != "-" {
			v["path"] = path
		} else if strings.ToLower(*format) == "svg" {
			v["svg"] = string(data)
		}
		writeJSON(e.stdout, v)
		return exitOK
	}
	if path == "" || path == "-" {
		if strings.ToLower(*format) == "png" && e.outTTY {
			return usageFail(e, fs, "refusing to write PNG bytes to a terminal; use -o file.png")
		}
		e.stdout.Write(data)
		return exitOK
	}
	if !g.quiet {
		fmt.Fprintf(e.stderr, "Saved %s (%s)\n", path, humanBytes(int64(len(data))))
	}
	return exitOK
}

// ---- styles, formats ----

func cmdMeta(ctx context.Context, args []string, e env, styles bool) int {
	var g globals
	name, usage := "formats", "Usage: kuticon formats [flags]\n\nList the file formats a pack can carry, and the PNG sizes.\n"
	if styles {
		name, usage = "styles", "Usage: kuticon styles [flags]\n\nList the drawing styles, weights, corners, and pack sizes.\n"
	}
	fs := newFlags(name, usage, e)
	g.bind(fs, e)
	pos, err := parse(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil {
		return exitUsage
	}
	if len(pos) > 0 {
		return usageFail(e, fs, name+" takes no arguments")
	}
	m, err := g.client(e).Meta(ctx)
	if err != nil {
		return fail(e, &g, err)
	}
	if styles {
		if g.json {
			writeJSON(e.stdout, map[string]any{"styles": m.Styles, "weights": m.Weights, "corners": m.Corners, "counts": m.Counts})
			return exitOK
		}
		for _, s := range m.Styles {
			fmt.Fprintf(e.stdout, "%-10s %s\n", s.ID, s.Description)
		}
		fmt.Fprintf(e.stdout, "\nweights    %s (default text)\ncorners    %s (default round)\ncounts     %s (default 12)\n",
			strings.Join(m.Weights, ", "), strings.Join(m.Corners, ", "), joinInts(m.Counts))
		return exitOK
	}
	if g.json {
		writeJSON(e.stdout, map[string]any{"formats": m.Formats, "sizes": m.Sizes})
		return exitOK
	}
	for _, f := range m.Formats {
		def := ""
		if f.Default {
			def = " (default)"
		}
		fmt.Fprintf(e.stdout, "%-8s %s%s\n", f.ID, f.Description, def)
	}
	fmt.Fprintf(e.stdout, "\nsizes    %s px (PNG, default all)\n", joinInts(m.Sizes))
	return exitOK
}

func joinInts(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ", ")
}

// ---- mcp ----

const mcpUsage = `Usage: kuticon mcp [flags]

Run a Model Context Protocol server (2025-06-18) on stdin and stdout for an
AI assistant. Tools: draw_icon_pack, search_icons, get_icon, list_styles.
Logs go to stderr only. See the README for Claude Desktop, Cursor, and VS Code.
`

func cmdMCP(ctx context.Context, args []string, e env) int {
	var g globals
	fs := newFlags("mcp", mcpUsage, e)
	g.bind(fs, e)
	maxWait := fs.Duration("max-wait", 15*time.Minute, "longest draw_icon_pack wait")
	pos, err := parse(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil || len(pos) > 0 {
		return exitUsage
	}
	c := g.client(e)
	c.UserAgent = "kuticon-mcp/" + version.Version
	srv := &mcp.Server{Tools: &mcp.Tools{Client: c, MaxWait: *maxWait}, Version: version.String()}
	if err := srv.Serve(ctx, e.stdin, e.stdout); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(e.stderr, "kuticon mcp: "+err.Error())
		return exitError
	}
	return exitOK
}
