package mcp

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/mark3labs/mcp-go/server"
	"github.com/snaga/ast-digger/internal/outline"
)

// MCPServer wraps the mark3labs MCP server and integrates with ast-digger internals.
// It manages the server lifecycle, tool handlers, and stdio communication stream.
type MCPServer struct {
	server   *server.MCPServer
	outliner *outline.Outliner
}

// NewServer creates a new MCPServer instance configured for ast-digger.
// If name is empty, it defaults to "ast-digger".
// If version is empty, it defaults to "dev".
func NewServer(name, version string, outliner *outline.Outliner) *MCPServer {
	if name == "" {
		name = "ast-digger"
	}
	if version == "" {
		version = "dev"
	}

	mcpSrv := server.NewMCPServer(
		name,
		version,
		server.WithRecovery(),
		server.WithToolCapabilities(false),
	)

	s := &MCPServer{
		server:   mcpSrv,
		outliner: outliner,
	}

	s.registerCoreTools()

	return s
}

// GetUnderlyingServer returns the internal *server.MCPServer instance for registering tools or hooks.
func (s *MCPServer) GetUnderlyingServer() *server.MCPServer {
	return s.server
}

// Outliner returns the associated *outline.Outliner instance.
func (s *MCPServer) Outliner() *outline.Outliner {
	return s.outliner
}

// Serve starts listening for JSON-RPC messages on the provided input reader and writes responses to the output writer.
// It runs until the context is cancelled, the reader reaches EOF, or a critical error occurs.
// It provides panic recovery to ensure unexpected failures do not crash the host process.
func (s *MCPServer) Serve(ctx context.Context, in io.Reader, out io.Writer) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic recovered in MCP server: %v", r)
		}
	}()

	stdioSrv := server.NewStdioServer(s.server)
	// StdioServer by default discards error logs; redirect error logs to os.Stderr
	stdioSrv.SetErrorLogger(log.New(os.Stderr, "[ast-digger-mcp] ", log.LstdFlags))

	return stdioSrv.Listen(ctx, in, out)
}

// ServeStdio starts the MCP server over standard input and standard output.
// It isolates standard output by redirecting default logging to stderr,
// and gracefully shuts down on context cancellation, SIGINT, or SIGTERM.
func (s *MCPServer) ServeStdio(ctx context.Context) error {
	// Isolate standard output: redirect default logger output to stderr
	log.SetOutput(os.Stderr)

	// Combine context with SIGINT and SIGTERM for graceful shutdown
	sigCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return s.Serve(sigCtx, os.Stdin, os.Stdout)
}
