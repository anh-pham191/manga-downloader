// Package mcp wraps the official Model Context Protocol Go SDK to
// expose downloader operations to Claude Desktop over stdio.
package mcp

import (
	"context"
	"log"
	"os"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Opts configures the server. Defaults are filled in by the CLI.
type Opts struct {
	Root        string // manga directory (.cbz live here)
	CookiesPath string // cookies.json
	Logger      *log.Logger
}

// Server is the long-lived MCP server. It owns the SDK server,
// the run-state singleton, and the logger.
type Server struct {
	opts     Opts
	sdk      *sdk.Server
	log      *log.Logger
	runState *RunState
	sync     *SyncExecutor
}

func New(opts Opts) (*Server, error) {
	if opts.Logger == nil {
		opts.Logger = log.New(os.Stderr, "mcp: ", log.LstdFlags)
	}
	s := &Server{opts: opts, log: opts.Logger}
	s.sdk = sdk.NewServer(&sdk.Implementation{
		Name:    "manga-downloader",
		Version: "0.1.0",
	}, nil)
	s.runState = &RunState{}
	s.sync = &SyncExecutor{
		Root:        opts.Root,
		CookiesPath: opts.CookiesPath,
		RunState:    s.runState,
	}
	s.register()
	return s, nil
}

// Serve runs the server on stdio until the context is cancelled or
// the peer disconnects.
func (s *Server) Serve(ctx context.Context) error {
	return s.sdk.Run(ctx, &sdk.StdioTransport{})
}
