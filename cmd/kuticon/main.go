// Command kuticon is the Kuticon CLI and MCP server. It is a client of the
// public HTTP API (default https://kuticon.com, or KUTICON_URL) and holds no
// server logic. The site server is cmd/kuticon; this binary ships as
// "kuticon" in the release archives built by scripts/release.sh.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], env{
		stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr,
		getenv: os.Getenv, tty: isTTY(os.Stderr), outTTY: isTTY(os.Stdout),
	})
	cancel()
	os.Exit(code)
}

func isTTY(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
