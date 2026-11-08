package main

import (
	"github.com/spf13/cobra"

	"github.com/snaga/ast-digger/internal/mcp"
	"github.com/snaga/ast-digger/internal/outline"
)

// newMcpCmd builds the `mcp` subcommand: it starts the Model Context Protocol (MCP)
// server over standard input and output (stdio). Exposes 4 consolidated core tools
// (ast_explore, ast_symbol, ast_trace, ast_audit) to AI agent harnesses (Claude Desktop,
// Antigravity, Cursor, etc.).
func newMcpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start Model Context Protocol (MCP) server over stdio",
		Long: "Start the built-in MCP server over standard input and output (stdio). Exposes 4 consolidated core tools (ast_explore, ast_symbol, ast_trace, ast_audit) to AI agent harnesses (Claude Desktop, Antigravity, Cursor, etc.).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			outliner := outline.NewOutliner()
			srv := mcp.NewServer("ast-digger", version, outliner)
			return srv.ServeStdio(cmd.Context())
		},
	}
	return cmd
}
