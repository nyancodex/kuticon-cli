package mcp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/nyancodex/kuticon-cli/internal/catalog"
	"github.com/nyancodex/kuticon-cli/internal/client"
)

// Tools binds the tool set to one site.
type Tools struct {
	Client *client.Client
	// MaxWait caps timeout_seconds for draw_icon_pack. Zero is 15 minutes.
	MaxWait time.Duration
}

type toolFunc func(ctx context.Context, args map[string]any, progress func(done, total float64, msg string)) Result

func (t *Tools) lookup(name string) (toolFunc, bool) {
	switch name {
	case "draw_icon_pack":
		return t.draw, true
	case "search_icons":
		return t.search, true
	case "get_icon":
		return t.getIcon, true
	case "list_styles":
		return t.listStyles, true
	}
	return nil, false
}

func strEnum(desc string, values []string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": desc}
}

func intEnum(values []int) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

// Definitions is the tools/list answer.
func Definitions() []map[string]any {
	looks := strEnum("Drawing style. Default outline.", catalog.Looks)
	return []map[string]any{
		{
			"name":  "draw_icon_pack",
			"title": "Draw an icon pack",
			"description": "Draw a new pack of matching icons from one sentence (for example \"icons for a neighborhood bakery\"). " +
				"The pack is queued on kuticon.com and usually takes under a few minutes. Counts against a small daily limit per network, so prefer search_icons for single common icons. " +
				"Returns every icon's SVG inline, plus the zip download URL. If it times out, call again with job_id to keep waiting instead of drawing a second pack.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{"type": "string", "minLength": 1, "maxLength": 280, "description": "One sentence describing the set."},
					"style":  looks,
					"weight": strEnum("Stroke weight. Default text.", []string{"fine", "text", "bold"}),
					"corner": strEnum("Line caps and joins. Default round.", []string{"round", "sharp"}),
					"count":  map[string]any{"type": "integer", "enum": intEnum(catalog.Counts), "description": "Icons in the pack. Default 12."},
					"formats": map[string]any{"type": "array", "uniqueItems": true, "minItems": 1,
						"items":       map[string]any{"type": "string", "enum": catalog.Formats},
						"description": "Files in the zip. Default [\"svg\", \"png\"]."},
					"sizes": map[string]any{"type": "array", "uniqueItems": true, "minItems": 1,
						"items":       map[string]any{"type": "integer", "enum": intEnum(catalog.Sizes)},
						"description": "PNG sides in pixels. Default all."},
					"include_svg":     map[string]any{"type": "boolean", "description": "Return each icon's SVG text. Default true."},
					"timeout_seconds": map[string]any{"type": "integer", "minimum": 10, "maximum": 900, "description": "How long to wait for the pack. Default 300."},
					"job_id":          map[string]any{"type": "string", "pattern": "^(?:[a-f0-9]{32}|specimen-[a-z]+)$", "description": "Resume waiting for a pack an earlier call started. prompt is then ignored."},
				},
			},
			"annotations": map[string]any{"title": "Draw an icon pack", "readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": true},
		},
		{
			"name":        "search_icons",
			"title":       "Search icons",
			"description": "Search the Kuticon icon library (thousands of free CC0 icons on one 24-unit grid). One word also matches inside longer words; several words match any whole word. Returns ids, names, packs, and URLs. Use get_icon for the SVG.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":  map[string]any{"type": "string", "minLength": 1, "maxLength": 100, "description": "Words, for example \"coffee cup\"."},
					"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Most results. Default 20."},
					"offset": map[string]any{"type": "integer", "minimum": 0, "description": "Skip this many results, for paging."},
				},
				"required": []string{"query"},
			},
			"annotations": map[string]any{"title": "Search icons", "readOnlyHint": true, "openWorldHint": true},
		},
		{
			"name":        "get_icon",
			"title":       "Get an icon",
			"description": "Get one library icon as SVG text, in any style. Outline icons use currentColor, so they take the text colour where they are pasted.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":    map[string]any{"type": "string", "minLength": 1, "description": "Icon id from search_icons, for example \"cupcake\"."},
					"style": looks,
				},
				"required": []string{"id"},
			},
			"annotations": map[string]any{"title": "Get an icon", "readOnlyHint": true, "openWorldHint": true},
		},
		{
			"name":        "list_styles",
			"title":       "List styles and formats",
			"description": "List the styles, weights, corners, pack sizes, file formats, and PNG sizes draw_icon_pack accepts.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			"annotations": map[string]any{"title": "List styles and formats", "readOnlyHint": true, "openWorldHint": true},
		},
	}
}

// arg readers. ok is false for a present value of the wrong type.
func argString(args map[string]any, key string) (string, bool) {
	v, has := args[key]
	if !has || v == nil {
		return "", true
	}
	s, ok := v.(string)
	return strings.TrimSpace(s), ok
}

func argInt(args map[string]any, key string) (int, bool, bool) {
	v, has := args[key]
	if !has || v == nil {
		return 0, false, true
	}
	f, ok := v.(float64)
	if !ok || f != math.Trunc(f) || math.Abs(f) > 1e9 {
		return 0, true, false
	}
	return int(f), true, true
}

func argBool(args map[string]any, key string, def bool) (bool, bool) {
	v, has := args[key]
	if !has || v == nil {
		return def, true
	}
	b, ok := v.(bool)
	return b, ok
}

func apiError(err error) Result {
	var e *client.Error
	if errors.As(err, &e) {
		msg := e.Message
		if e.RetryAfter > 0 {
			msg += fmt.Sprintf(" (retry after %ds)", int(e.RetryAfter.Seconds()))
		}
		return errorResult("Kuticon refused: %s", msg)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errorResult("Kuticon did not answer in time.")
	}
	return errorResult("Could not reach Kuticon: %v", err)
}

func (t *Tools) listStyles(ctx context.Context, _ map[string]any, _ func(float64, float64, string)) Result {
	m, err := t.Client.Meta(ctx)
	if err != nil {
		return apiError(err)
	}
	var b strings.Builder
	b.WriteString("Styles:\n")
	for _, s := range m.Styles {
		fmt.Fprintf(&b, "- %s: %s\n", s.ID, s.Description)
	}
	fmt.Fprintf(&b, "Weights: %s. Corners: %s.\nCounts: %v.\nFormats:\n", strings.Join(m.Weights, ", "), strings.Join(m.Corners, ", "), m.Counts)
	for _, f := range m.Formats {
		def := ""
		if f.Default {
			def = " (default)"
		}
		fmt.Fprintf(&b, "- %s%s: %s\n", f.ID, def, f.Description)
	}
	fmt.Fprintf(&b, "PNG sizes: %v.\nDaily limit: %d packs per network.", m.Sizes, m.DailyLimit)
	return textResult(b.String(), m)
}

func (t *Tools) search(ctx context.Context, args map[string]any, _ func(float64, float64, string)) Result {
	q, ok := argString(args, "query")
	if !ok || q == "" {
		return errorResult("query must be a non-empty string.")
	}
	limit, _, ok := argInt(args, "limit")
	if !ok || limit < 0 || limit > 100 {
		return errorResult("limit must be an integer from 1 to 100.")
	}
	offset, _, ok := argInt(args, "offset")
	if !ok || offset < 0 {
		return errorResult("offset must be 0 or more.")
	}
	res, err := t.Client.Search(ctx, q, limit, offset)
	if err != nil {
		return apiError(err)
	}
	if res.Icons == nil {
		res.Icons = []client.Hit{}
	}
	var b strings.Builder
	if res.Total == 0 {
		fmt.Fprintf(&b, "No library icons match %q. draw_icon_pack can draw a new set.", q)
	} else {
		fmt.Fprintf(&b, "%d icons match %q (showing %d from %d):\n", res.Total, q, len(res.Icons), res.Offset+1)
		for _, h := range res.Icons {
			fmt.Fprintf(&b, "- %s — %s (%s pack) %s\n", h.ID, h.Name, h.PackName, h.SVG)
		}
	}
	return textResult(strings.TrimRight(b.String(), "\n"), res)
}

func (t *Tools) getIcon(ctx context.Context, args map[string]any, _ func(float64, float64, string)) Result {
	id, ok := argString(args, "id")
	if !ok || id == "" {
		return errorResult("id must be a non-empty string.")
	}
	look, ok := argString(args, "style")
	if !ok {
		return errorResult("style must be a string.")
	}
	svg, err := t.Client.IconSVG(ctx, id, look)
	if err != nil {
		if client.NotFound(err) {
			return errorResult("No library icon has the id %q. Try search_icons.", id)
		}
		return apiError(err)
	}
	if look == "" {
		look = "outline"
	}
	return textResult(svg, map[string]any{"id": id, "style": look, "svg": svg, "page": t.Client.IconPage(id)})
}

type drawnIcon struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	SVGURL string `json:"svgUrl"`
	SVG    string `json:"svg,omitempty"`
}

func (t *Tools) draw(ctx context.Context, args map[string]any, progress func(float64, float64, string)) Result {
	var req client.DrawRequest
	var ok bool
	jobID, ok := argString(args, "job_id")
	if !ok {
		return errorResult("job_id must be a string.")
	}
	if req.Prompt, ok = argString(args, "prompt"); !ok {
		return errorResult("prompt must be a string.")
	}
	if jobID == "" && req.Prompt == "" {
		return errorResult("Give a prompt sentence, or a job_id to keep waiting for.")
	}
	for key, dst := range map[string]*string{"style": &req.Style, "weight": &req.Weight, "corner": &req.Cap} {
		if *dst, ok = argString(args, key); !ok {
			return errorResult("%s must be a string.", key)
		}
	}
	if req.Weight == "" {
		req.Weight = "text"
	}
	if req.Cap == "" {
		req.Cap = "round"
	}
	n, _, ok := argInt(args, "count")
	if !ok {
		return errorResult("count must be an integer.")
	}
	req.Count = n
	if v, has := args["formats"]; has && v != nil {
		list, ok := v.([]any)
		if !ok {
			return errorResult("formats must be a list of strings.")
		}
		for _, x := range list {
			s, ok := x.(string)
			if !ok {
				return errorResult("formats must be a list of strings.")
			}
			req.Formats = append(req.Formats, s)
		}
	}
	if v, has := args["sizes"]; has && v != nil {
		list, ok := v.([]any)
		if !ok {
			return errorResult("sizes must be a list of integers.")
		}
		for _, x := range list {
			f, ok := x.(float64)
			if !ok || f != math.Trunc(f) {
				return errorResult("sizes must be a list of integers.")
			}
			req.Sizes = append(req.Sizes, int(f))
		}
	}
	withSVG, ok := argBool(args, "include_svg", true)
	if !ok {
		return errorResult("include_svg must be true or false.")
	}
	secs, has, ok := argInt(args, "timeout_seconds")
	if !ok || has && (secs < 10 || secs > 900) {
		return errorResult("timeout_seconds must be 10 to 900.")
	}
	wait := 300 * time.Second
	if has {
		wait = time.Duration(secs) * time.Second
	}
	if max := t.MaxWait; max > 0 && wait > max || max == 0 && wait > 15*time.Minute {
		if max == 0 {
			max = 15 * time.Minute
		}
		wait = max
	}

	step := 0.0
	note := func(msg string) {
		if progress != nil {
			step++
			progress(step, 0, msg)
		}
	}
	if jobID == "" {
		job, err := t.Client.Create(ctx, req)
		if err != nil {
			return apiError(err)
		}
		jobID = job.ID
		note(fmt.Sprintf("Queued as %s, position %d.", jobID, job.Position))
	}
	wctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	job, err := t.Client.Wait(wctx, jobID, func(j client.Job) {
		switch j.Status {
		case "queued":
			note(fmt.Sprintf("Queued, position %d.", j.Position))
		case "drawing":
			note("Drawing the pack.")
		default:
			note("Status " + j.Status + ".")
		}
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			st := job.Status
			if st == "" {
				st = "pending"
			}
			return textResult(
				fmt.Sprintf("The pack %s is still %s after %ds. Call draw_icon_pack again with job_id %q to keep waiting; do not start a new pack.", jobID, st, int(wait.Seconds()), jobID),
				map[string]any{"id": jobID, "status": st, "position": job.Position, "pageUrl": t.Client.PackPage(jobID)})
		}
		if client.NotFound(err) {
			return errorResult("No pack has the id %q.", jobID)
		}
		return apiError(err)
	}
	if job.Status == "failed" {
		msg := job.Error
		if msg == "" {
			msg = "The desk could not draw that pack."
		}
		return errorResult("The pack %s failed: %s", jobID, msg)
	}
	icons := make([]drawnIcon, 0, len(job.Icons))
	for i, it := range job.Icons {
		d := drawnIcon{ID: it.ID, Name: it.Name, SVGURL: t.Client.PackSVGURL(jobID, it.ID)}
		if withSVG {
			note(fmt.Sprintf("Fetching SVG %d of %d.", i+1, len(job.Icons)))
			svg, err := t.Client.PackSVG(ctx, jobID, it.ID)
			if err != nil {
				return apiError(err)
			}
			d.SVG = svg
		}
		icons = append(icons, d)
	}
	out := map[string]any{
		"id": jobID, "status": job.Status, "title": job.Title, "icons": icons,
		"downloadUrl": t.Client.DownloadURL(jobID), "pageUrl": t.Client.PackPage(jobID),
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d icons. Zip: %s\nPage: %s\n", job.Title, len(icons), t.Client.DownloadURL(jobID), t.Client.PackPage(jobID))
	for _, ic := range icons {
		fmt.Fprintf(&b, "- %s (%s)\n", ic.ID, ic.Name)
	}
	res := textResult(strings.TrimRight(b.String(), "\n"), out)
	for _, ic := range icons {
		if ic.SVG != "" {
			res.Content = append(res.Content, map[string]any{"type": "text", "text": "<!-- " + ic.ID + ".svg -->\n" + ic.SVG})
		}
	}
	res.Content = append(res.Content, map[string]any{
		"type": "resource_link", "uri": t.Client.DownloadURL(jobID), "name": "kuticon-" + jobID + ".zip",
		"title": job.Title + " (zip)", "mimeType": "application/zip",
	})
	return res
}
