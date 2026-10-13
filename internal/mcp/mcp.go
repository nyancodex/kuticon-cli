// Package mcp is a Model Context Protocol server (spec 2025-06-18) for
// Kuticon over stdio: newline-delimited JSON-RPC 2.0. Every tool is a call
// to the public HTTP API through internal/client.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

// ProtocolVersion is the newest revision spoken. Older ones are accepted
// when a client asks for them.
const ProtocolVersion = "2025-06-18"

var supported = map[string]bool{"2025-06-18": true, "2025-03-26": true, "2024-11-05": true}

// JSON-RPC error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeNoMethod       = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// Server is one stdio session.
type Server struct {
	Tools   *Tools
	Version string

	out     *json.Encoder
	outMu   sync.Mutex
	mu      sync.Mutex
	running map[string]context.CancelFunc
	wg      sync.WaitGroup
}

// Serve reads requests from r until EOF or ctx ends, and answers on w.
// tools/call runs in its own goroutine, so ping and cancellation still flow.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	s.out = json.NewEncoder(w)
	s.out.SetEscapeHTML(false)
	s.running = map[string]context.CancelFunc{}
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		s.wg.Wait()
	}()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	lines := make(chan []byte)
	errc := make(chan error, 1)
	go func() {
		for sc.Scan() {
			b := append([]byte(nil), sc.Bytes()...)
			select {
			case lines <- b:
			case <-ctx.Done():
				return
			}
		}
		errc <- sc.Err()
		close(lines)
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-lines:
			if !ok {
				// stdin closed: let calls already running answer, then stop.
				s.wg.Wait()
				return <-errc
			}
			s.handle(ctx, line)
		}
	}
}

func (s *Server) send(v any) {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	s.out.Encode(v)
}

func (s *Server) reply(id json.RawMessage, result any) {
	s.send(response{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) fail(id json.RawMessage, code int, msg string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	s.send(response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func (s *Server) handle(ctx context.Context, line []byte) {
	line = []byte(strings.TrimSpace(string(line)))
	if len(line) == 0 {
		return
	}
	if line[0] == '[' {
		s.fail(nil, codeInvalidRequest, "Batches are not part of MCP 2025-06-18.")
		return
	}
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		s.fail(nil, codeParse, "Parse error.")
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		if len(req.ID) > 0 && req.Method == "" {
			return // a response to us; we send no requests, so ignore it
		}
		s.fail(req.ID, codeInvalidRequest, "Invalid request.")
		return
	}
	isNote := len(req.ID) == 0
	switch req.Method {
	case "initialize":
		s.initialize(req)
	case "notifications/initialized":
	case "notifications/cancelled":
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if json.Unmarshal(req.Params, &p) == nil {
			s.mu.Lock()
			if c := s.running[string(p.RequestID)]; c != nil {
				c()
			}
			s.mu.Unlock()
		}
	case "ping":
		s.reply(req.ID, struct{}{})
	case "tools/list":
		s.reply(req.ID, map[string]any{"tools": Definitions()})
	case "tools/call":
		s.call(ctx, req)
	default:
		if !isNote {
			s.fail(req.ID, codeNoMethod, "Method not found: "+req.Method)
		}
	}
}

func (s *Server) initialize(req request) {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	json.Unmarshal(req.Params, &p)
	v := ProtocolVersion
	if supported[p.ProtocolVersion] {
		v = p.ProtocolVersion
	}
	ver := s.Version
	if ver == "" {
		ver = "dev"
	}
	s.reply(req.ID, map[string]any{
		"protocolVersion": v,
		"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
		"serverInfo":      map[string]any{"name": "kuticon", "title": "Kuticon icons", "version": ver},
		"instructions": "Kuticon draws free CC0 icon packs from one sentence and searches its icon library. " +
			"Search the library first (search_icons, get_icon): it is instant and free. " +
			"draw_icon_pack queues a new pack, counts against a small daily limit per network, and can take a few minutes; " +
			"if it times out, call it again with job_id instead of drawing again.",
	})
}

func (s *Server) call(ctx context.Context, req request) {
	if len(req.ID) == 0 {
		return
	}
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Meta      struct {
			ProgressToken json.RawMessage `json:"progressToken"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.fail(req.ID, codeInvalidParams, "params must be {name, arguments}.")
		return
	}
	tool, ok := s.Tools.lookup(p.Name)
	if !ok {
		s.fail(req.ID, codeInvalidParams, "Unknown tool: "+p.Name)
		return
	}
	args := map[string]any{}
	if len(p.Arguments) > 0 && string(p.Arguments) != "null" {
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			s.fail(req.ID, codeInvalidParams, "arguments must be an object.")
			return
		}
	}
	cctx, cancel := context.WithCancel(ctx)
	key := string(req.ID)
	s.mu.Lock()
	s.running[key] = cancel
	s.mu.Unlock()
	var progress func(done, total float64, msg string)
	if tok := p.Meta.ProgressToken; len(tok) > 0 && string(tok) != "null" {
		progress = func(done, total float64, msg string) {
			params := map[string]any{"progressToken": tok, "progress": done, "message": msg}
			if total > 0 {
				params["total"] = total
			}
			s.send(notification{JSONRPC: "2.0", Method: "notifications/progress", Params: params})
		}
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.running, key)
			s.mu.Unlock()
			cancel()
		}()
		res := tool(cctx, args, progress)
		if errors.Is(cctx.Err(), context.Canceled) && ctx.Err() == nil {
			return // cancelled by the client: the spec says send nothing
		}
		s.reply(req.ID, res)
	}()
}

// Result is a CallToolResult.
type Result struct {
	Content           []map[string]any `json:"content"`
	StructuredContent any              `json:"structuredContent,omitempty"`
	IsError           bool             `json:"isError,omitempty"`
}

func textResult(text string, structured any) Result {
	return Result{Content: []map[string]any{{"type": "text", "text": text}}, StructuredContent: structured}
}

func errorResult(format string, a ...any) Result {
	return Result{Content: []map[string]any{{"type": "text", "text": fmt.Sprintf(format, a...)}}, IsError: true}
}
