package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// Tool name constants for the 4 core AST-Digger MCP tools.
const (
	ToolNameExplore = "ast_explore"
	ToolNameSymbol  = "ast_symbol"
	ToolNameTrace   = "ast_trace"
	ToolNameAudit   = "ast_audit"
)

// NewExploreTool creates the "ast_explore" tool definition with negative affordance.
// Integrates `directory` and `outline` functionalities.
func NewExploreTool() mcp.Tool {
	return mcp.NewTool(
		ToolNameExplore,
		mcp.WithDescription("Inspect structural code skeleton without reading full file bodies. CRITICAL: Call this tool FIRST before calling read_file or editing files to inspect class/function signatures and prevent context bloat. Accepts either a directory path (for tree + symbol summaries) or a file path (for AST class/function outlines)."),
		mcp.WithString("path",
			mcp.Description("Directory or file path to explore (relative or absolute)"),
			mcp.Required(),
		),
		mcp.WithBoolean("recursive",
			mcp.Description("Whether to traverse subdirectories recursively (directory mode only, default: true)"),
		),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of files to inspect (directory mode only, default: 50)"),
		),
		mcp.WithBoolean("full",
			mcp.Description("Disable signature truncation to output complete parameter types and docstrings (default: false)"),
		),
	)
}

// NewSymbolTool creates the "ast_symbol" tool definition with negative affordance.
// Integrates `symbol` and `locate` functionalities.
func NewSymbolTool() mcp.Tool {
	return mcp.NewTool(
		ToolNameSymbol,
		mcp.WithDescription("Surgically retrieve a specific class, method, or function by symbol name/path (e.g., 'UserService.createUser'). STRICT POLICY: NEVER read large source files entirely with read_file when you only need to inspect or edit a specific function."),
		mcp.WithString("file_path",
			mcp.Description("Path to the source code file"),
			mcp.Required(),
		),
		mcp.WithString("symbol",
			mcp.Description("Target symbol name or dot-separated path (e.g., 'OrderService.checkout' or 'parse_config')"),
			mcp.Required(),
		),
		mcp.WithBoolean("follow_imports",
			mcp.Description("If the symbol is re-exported from another file, automatically trace and extract its real definition (default: false)"),
		),
		mcp.WithBoolean("locate_only",
			mcp.Description("If true, returns only line range metadata (start_line, end_line) without the code body. Useful for edit_file preparation (default: false)"),
		),
	)
}

// NewTraceTool creates the "ast_trace" tool definition with negative affordance.
// Integrates `resolve-import` and `references` functionalities.
func NewTraceTool() mcp.Tool {
	return mcp.NewTool(
		ToolNameTrace,
		mcp.WithDescription("Trace symbol relationships across the codebase using AST precision. Use action='definition' to jump to where an imported symbol is defined, or action='references' to find all call sites project-wide. Far superior to generic text search/ripgrep as it filters out comments, strings, and unrelated name collisions."),
		mcp.WithString("action",
			mcp.Description("'definition' resolves origin file and line of an import; 'references' searches all usage sites across the project"),
			mcp.Required(),
			mcp.Enum("definition", "references"),
		),
		mcp.WithString("symbol",
			mcp.Description("Target symbol name to trace"),
			mcp.Required(),
		),
		mcp.WithString("file_path",
			mcp.Description("Current file path (required when action='definition')"),
		),
		mcp.WithInteger("limit",
			mcp.Description("Maximum reference results to return (references mode only, default: 30)"),
		),
	)
}

// NewAuditTool creates the "ast_audit" tool definition with negative affordance.
// Integrates `change-impact` functionality.
func NewAuditTool() mcp.Tool {
	return mcp.NewTool(
		ToolNameAudit,
		mcp.WithDescription("Analyze semantic impact and code health of uncommitted git diffs. Audits cognitive complexity spikes, public API signature modifications, and missing docstrings. MUST be run before marking code implementation tasks as completed in SDD workflows."),
		mcp.WithString("base",
			mcp.Description("Git revision or branch to compare against (default: 'HEAD')"),
		),
		mcp.WithBoolean("require_doc",
			mcp.Description("Audit presence of docstrings on modified public functions (default: true)"),
		),
		mcp.WithInteger("max_complexity",
			mcp.Description("Threshold for cognitive complexity statements per function before flagging warnings (default: 15)"),
		),
	)
}

// CoreTools returns all 4 core MCP tools supported by AST-Digger.
func CoreTools() []mcp.Tool {
	return []mcp.Tool{
		NewExploreTool(),
		NewSymbolTool(),
		NewTraceTool(),
		NewAuditTool(),
	}
}

// registerCoreTools registers all 4 core tools to the underlying MCP server with stub handlers.
// Real handlers will be attached in Tasks 50 and 51.
func (s *MCPServer) registerCoreTools() {
	tools := CoreTools()
	for _, tool := range tools {
		toolName := tool.Name
		s.server.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultError(fmt.Sprintf("tool '%s' is not implemented yet", toolName)), nil
		})
	}
}
