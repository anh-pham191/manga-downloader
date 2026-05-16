package mcp

import (
	"context"
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
	if len(tools.Tools) != 0 {
		t.Fatalf("expected 0 tools at scaffold stage, got %d", len(tools.Tools))
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
