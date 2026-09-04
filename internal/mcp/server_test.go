package mcp

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestServer_Initialize asserts the server starts, advertises its
// implementation name, and lists the registered tools.
//
// Initialization happens inside ClientSession.Connect — there is no
// separate Initialize() call. ServerInfo is reachable via
// sess.InitializeResult().
func TestServer_Initialize(t *testing.T) {
	srv, sess := newTestPair(t)

	init := sess.InitializeResult()
	if init == nil {
		t.Fatal("no InitializeResult after Connect")
	}
	if init.ServerInfo.Name != "manga-downloader" {
		t.Fatalf("server name = %q, want %q", init.ServerInfo.Name, "manga-downloader")
	}
	tools, err := sess.ListTools(context.Background(), &sdk.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 9 {
		t.Fatalf("expected 9 tools after Task 7 (update_all), got %d", len(tools.Tools))
	}
	_ = srv
}

// newTestPair wires an in-memory client to a fresh server using the
// SDK's documented Connect/Connect pair. Both sides are non-blocking;
// no goroutine to manage.
func newTestPair(t *testing.T) (*Server, *sdk.ClientSession) {
	t.Helper()
	opts := Opts{
		Root:        t.TempDir(),
		CookiesPath: t.TempDir() + "/cookies.json",
	}
	srv, err := New(opts)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	clientT, serverT := sdk.NewInMemoryTransports()
	if _, err := srv.sdk.Connect(context.Background(), serverT, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	c := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil)
	sess, err := c.Connect(context.Background(), clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return srv, sess
}

// TestStdoutStaysClean asserts that constructing the server AND
// invoking every read-only tool never writes to stdout. Tool
// handlers must route logs through the configured stderr logger.
//
// Sync tools are excluded because they need a fully-seeded cookie
// file + an injected runner; the integration tests in
// integration_test.go already exercise their handler bodies.
func TestStdoutStaysClean(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldStdout })

	_, sess := newTestPair(t)
	for _, name := range []string{"get_cookie_status", "list_manga"} {
		if _, err := sess.CallTool(context.Background(), &sdk.CallToolParams{Name: name}); err != nil {
			t.Fatalf("call %s: %v", name, err)
		}
	}

	w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	if buf.Len() != 0 {
		t.Fatalf("stdout polluted with %d bytes: %q", buf.Len(), buf.String())
	}
}
