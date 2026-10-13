package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/nyancodex/kuticon-cli/internal/client"
	"github.com/nyancodex/kuticon-cli/internal/client/clienttest"
)

type session struct {
	t    *testing.T
	in   *io.PipeWriter
	out  *bufio.Scanner
	done chan error
}

func start(t *testing.T, f *clienttest.Fake) *session {
	c := client.New(f.URL, "test")
	c.Sleep = func(ctx context.Context, d time.Duration) error { return ctx.Err() }
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &session{t: t, in: inW, out: bufio.NewScanner(outR), done: make(chan error, 1)}
	s.out.Buffer(nil, 4<<20)
	srv := &Server{Tools: &Tools{Client: c}, Version: "1.0.0+1"}
	go func() { s.done <- srv.Serve(context.Background(), inR, outW); outW.Close() }()
	t.Cleanup(func() {
		inW.Close()
		go io.Copy(io.Discard, outR)
		if err := <-s.done; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	return s
}

func (s *session) send(line string) {
	s.t.Helper()
	if _, err := io.WriteString(s.in, line+"\n"); err != nil {
		s.t.Fatal(err)
	}
}

func (s *session) read() map[string]any {
	s.t.Helper()
	if !s.out.Scan() {
		s.t.Fatalf("no answer: %v", s.out.Err())
	}
	var m map[string]any
	if err := json.Unmarshal(s.out.Bytes(), &m); err != nil {
		s.t.Fatalf("bad json %q: %v", s.out.Text(), err)
	}
	return m
}

// result reads until the response with id, collecting notifications.
func (s *session) result(id float64) (map[string]any, []map[string]any) {
	s.t.Helper()
	var notes []map[string]any
	for {
		m := s.read()
		if m["method"] != nil {
			notes = append(notes, m)
			continue
		}
		if m["id"] != id {
			s.t.Fatalf("answer for %v, want %v: %v", m["id"], id, m)
		}
		return m, notes
	}
}

func TestInitializeAndList(t *testing.T) {
	f := clienttest.New()
	defer f.Close()
	s := start(t, f)
	s.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	m, _ := s.result(1)
	res := m["result"].(map[string]any)
	if res["protocolVersion"] != "2025-06-18" || res["serverInfo"].(map[string]any)["version"] != "1.0.0+1" {
		t.Fatalf("init = %v", res)
	}
	if _, ok := res["capabilities"].(map[string]any)["tools"]; !ok {
		t.Fatal("no tools capability")
	}
	s.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	s.send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	m, _ = s.result(2)
	tools := m["result"].(map[string]any)["tools"].([]any)
	names := map[string]map[string]any{}
	for _, x := range tools {
		tool := x.(map[string]any)
		names[tool["name"].(string)] = tool
		schema := tool["inputSchema"].(map[string]any)
		if schema["type"] != "object" || tool["description"] == "" {
			t.Fatalf("schema %v", tool)
		}
	}
	for _, n := range []string{"draw_icon_pack", "search_icons", "get_icon", "list_styles"} {
		if names[n] == nil {
			t.Fatalf("missing %s", n)
		}
	}
	props := names["draw_icon_pack"]["inputSchema"].(map[string]any)["properties"].(map[string]any)
	if len(props["style"].(map[string]any)["enum"].([]any)) != 6 {
		t.Fatal("style enum")
	}
	// An older client gets its own revision; an unknown one gets ours.
	s.send(`{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`)
	m, _ = s.result(3)
	if m["result"].(map[string]any)["protocolVersion"] != "2024-11-05" {
		t.Fatal("negotiate old")
	}
	s.send(`{"jsonrpc":"2.0","id":4,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
	m, _ = s.result(4)
	if m["result"].(map[string]any)["protocolVersion"] != ProtocolVersion {
		t.Fatal("negotiate unknown")
	}
}

func TestProtocolErrors(t *testing.T) {
	f := clienttest.New()
	defer f.Close()
	s := start(t, f)
	cases := []struct {
		line string
		code float64
	}{
		{`not json`, codeParse},
		{`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, codeInvalidRequest},
		{`{"jsonrpc":"2.0","id":5,"method":"resources/list"}`, codeNoMethod},
		{`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"rm_rf","arguments":{}}}`, codeInvalidParams},
		{`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"get_icon","arguments":[1]}}`, codeInvalidParams},
	}
	for _, c := range cases {
		s.send(c.line)
		m := s.read()
		e, ok := m["error"].(map[string]any)
		if !ok || e["code"] != c.code {
			t.Fatalf("%s -> %v", c.line, m)
		}
	}
	s.send(`{"jsonrpc":"2.0","id":"p","method":"ping"}`)
	if m := s.read(); m["id"] != "p" || m["error"] != nil {
		t.Fatalf("ping = %v", m)
	}
}

func call(s *session, id float64, name, args string) (map[string]any, []map[string]any) {
	s.send(`{"jsonrpc":"2.0","id":` + jsonNum(id) + `,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `,"_meta":{"progressToken":"tok"}}}`)
	m, notes := s.result(id)
	return m["result"].(map[string]any), notes
}

func jsonNum(f float64) string { b, _ := json.Marshal(f); return string(b) }

func text(res map[string]any, i int) string {
	return res["content"].([]any)[i].(map[string]any)["text"].(string)
}

func TestSearchAndGet(t *testing.T) {
	f := clienttest.New()
	defer f.Close()
	s := start(t, f)
	res, _ := call(s, 1, "search_icons", `{"query":"coffee","limit":5}`)
	if res["isError"] == true || !strings.Contains(text(res, 0), "croissant") {
		t.Fatalf("search = %v", res)
	}
	if res["structuredContent"].(map[string]any)["total"] != float64(1) {
		t.Fatal("structured")
	}
	res, _ = call(s, 2, "search_icons", `{"query":"zzz"}`)
	if res["isError"] == true || !strings.Contains(text(res, 0), "No library icons") {
		t.Fatalf("empty search = %v", res)
	}
	res, _ = call(s, 3, "search_icons", `{"query":""}`)
	if res["isError"] != true {
		t.Fatal("empty query must be a tool error")
	}
	res, _ = call(s, 4, "get_icon", `{"id":"croissant","style":"neon"}`)
	if res["isError"] == true || text(res, 0) != clienttest.SVG("croissant-neon") {
		t.Fatalf("get = %v", res)
	}
	res, _ = call(s, 5, "get_icon", `{"id":"unicorn"}`)
	if res["isError"] != true || !strings.Contains(text(res, 0), "search_icons") {
		t.Fatalf("missing icon = %v", res)
	}
	res, _ = call(s, 6, "list_styles", `{}`)
	if res["isError"] == true || !strings.Contains(text(res, 0), "neon: Glowing") {
		t.Fatalf("styles = %v", res)
	}
}

func TestDraw(t *testing.T) {
	f := clienttest.New()
	defer f.Close()
	s := start(t, f)
	res, notes := call(s, 1, "draw_icon_pack", `{"prompt":"icons for a bakery","style":"sticker","count":4,"formats":["svg","font"],"sizes":[64]}`)
	if res["isError"] == true {
		t.Fatalf("draw = %v", res)
	}
	post := f.Posts[0]
	if post["style"] != "sticker" || post["weight"] != "text" || post["cap"] != "round" || post["count"] != float64(4) || len(post["formats"].([]any)) != 2 {
		t.Fatalf("post = %v", post)
	}
	sc := res["structuredContent"].(map[string]any)
	icons := sc["icons"].([]any)
	if len(icons) != 2 || icons[0].(map[string]any)["svg"] != clienttest.SVG("croissant") {
		t.Fatalf("icons = %v", icons)
	}
	if !strings.HasSuffix(sc["downloadUrl"].(string), "/api/packs/"+clienttest.PackID+"/download") {
		t.Fatal(sc["downloadUrl"])
	}
	content := res["content"].([]any)
	if len(content) != 4 || content[3].(map[string]any)["type"] != "resource_link" {
		t.Fatalf("content = %v", content)
	}
	last := -1.0
	for _, n := range notes {
		if n["method"] != "notifications/progress" {
			t.Fatalf("note %v", n)
		}
		p := n["params"].(map[string]any)
		if p["progressToken"] != "tok" || p["progress"].(float64) <= last {
			t.Fatalf("progress must rise: %v", p)
		}
		last = p["progress"].(float64)
	}
	if len(notes) < 4 {
		t.Fatalf("notes = %d", len(notes))
	}
}

func TestDrawRefusedAndArgs(t *testing.T) {
	f := clienttest.New()
	defer f.Close()
	s := start(t, f)
	f.SetRefuse(429, "Eight packs is the limit for this day.")
	res, _ := call(s, 1, "draw_icon_pack", `{"prompt":"x"}`)
	if res["isError"] != true || !strings.Contains(text(res, 0), "limit for this day") {
		t.Fatalf("429 = %v", res)
	}
	for i, args := range []string{`{}`, `{"prompt":3}`, `{"prompt":"x","count":1.5}`, `{"prompt":"x","sizes":"64"}`, `{"prompt":"x","timeout_seconds":1}`} {
		res, _ := call(s, float64(10+i), "draw_icon_pack", args)
		if res["isError"] != true {
			t.Fatalf("%s accepted", args)
		}
	}
	if f.PostCount() != 1 {
		t.Fatalf("bad args posted: %d", len(f.Posts))
	}
}

func TestDrawTimeoutThenResume(t *testing.T) {
	f := clienttest.New()
	defer f.Close()
	c := client.New(f.URL, "test")
	c.Sleep = func(ctx context.Context, d time.Duration) error {
		select {
		case <-time.After(5 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	tools := &Tools{Client: c, MaxWait: 30 * time.Millisecond}
	f.SetSteps(f.Steps[:1])
	res := tools.draw(context.Background(), map[string]any{"prompt": "x"}, nil)
	if res.IsError || !strings.Contains(res.Content[0]["text"].(string), "job_id") {
		t.Fatalf("timeout = %+v", res)
	}
	f.SetSteps(clienttest.New().Steps)
	res = tools.draw(context.Background(), map[string]any{"job_id": clienttest.PackID, "include_svg": false}, nil)
	if res.IsError || f.PostCount() != 1 {
		t.Fatalf("resume = %+v posts=%d", res, len(f.Posts))
	}
	if res.StructuredContent.(map[string]any)["icons"].([]drawnIcon)[0].SVG != "" {
		t.Fatal("include_svg false")
	}
	res = tools.draw(context.Background(), map[string]any{"job_id": "ffffffffffffffffffffffffffffffff"}, nil)
	if !res.IsError {
		t.Fatal("unknown job")
	}
}

func TestCancel(t *testing.T) {
	f := clienttest.New()
	defer f.Close()
	f.SetSteps(f.Steps[:1])
	c := client.New(f.URL, "test")
	s := &session{t: t}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s.in, s.out = inW, bufio.NewScanner(outR)
	srv := &Server{Tools: &Tools{Client: c}}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(context.Background(), inR, outW); outW.Close() }()
	s.send(`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"draw_icon_pack","arguments":{"prompt":"x"}}}`)
	time.Sleep(50 * time.Millisecond)
	s.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":9,"reason":"user"}}`)
	s.send(`{"jsonrpc":"2.0","id":10,"method":"ping"}`)
	if m := s.read(); m["id"] != float64(10) {
		t.Fatalf("after cancel = %v", m)
	}
	inW.Close()
	go io.Copy(io.Discard, outR)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
