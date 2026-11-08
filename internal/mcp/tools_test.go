package mcp

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/snaga/ast-digger/internal/outline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// propertyExpectation defines expected JSON schema property constraints.
type propertyExpectation struct {
	propType    string
	description string
	enumValues  []string
}

// toolTestCase defines test parameters for validating MCP tool schema.
type toolTestCase struct {
	name                string
	fullDescription     string
	expectedDescKeyword string
	requiredFields      []string
	properties          map[string]propertyExpectation
}

// TestCoreTools_SchemaDefinitions validates the JSON schema, negative affordance descriptions,
// required fields, and properties of all 4 core tools using the Detroit school approach.
func TestCoreTools_SchemaDefinitions(t *testing.T) {
	testCases := []toolTestCase{
		{
			name: ToolNameExplore,
			fullDescription: "Inspect structural code skeleton without reading full file bodies. " +
				"CRITICAL: Call this tool FIRST before calling read_file or editing files to inspect " +
				"class/function signatures and prevent context bloat. Accepts either a directory path " +
				"(for tree + symbol summaries) or a file path (for AST class/function outlines).",
			expectedDescKeyword: "CRITICAL: Call this tool FIRST before calling read_file or editing files",
			requiredFields:      []string{"path"},
			properties: map[string]propertyExpectation{
				"path": {
					propType:    "string",
					description: "Directory or file path to explore (relative or absolute)",
				},
				"recursive": {
					propType:    "boolean",
					description: "Whether to traverse subdirectories recursively (directory mode only, default: true)",
				},
				"limit": {
					propType:    "integer",
					description: "Maximum number of files to inspect (directory mode only, default: 50)",
				},
				"full": {
					propType:    "boolean",
					description: "Disable signature truncation to output complete parameter types and docstrings (default: false)",
				},
			},
		},
		{
			name: ToolNameSymbol,
			fullDescription: "Surgically retrieve a specific class, method, or function by symbol name/path " +
				"(e.g., 'UserService.createUser'). STRICT POLICY: NEVER read large source files entirely " +
				"with read_file when you only need to inspect or edit a specific function.",
			expectedDescKeyword: "STRICT POLICY: NEVER read large source files entirely with read_file",
			requiredFields:      []string{"file_path", "symbol"},
			properties: map[string]propertyExpectation{
				"file_path": {
					propType:    "string",
					description: "Path to the source code file",
				},
				"symbol": {
					propType:    "string",
					description: "Target symbol name or dot-separated path (e.g., 'OrderService.checkout' or 'parse_config')",
				},
				"follow_imports": {
					propType:    "boolean",
					description: "If the symbol is re-exported from another file, automatically trace and extract its real definition (default: false)",
				},
				"locate_only": {
					propType:    "boolean",
					description: "If true, returns only line range metadata (start_line, end_line) without the code body. Useful for edit_file preparation (default: false)",
				},
			},
		},
		{
			name: ToolNameTrace,
			fullDescription: "Trace symbol relationships across the codebase using AST precision. " +
				"Use action='definition' to jump to where an imported symbol is defined, or action='references' " +
				"to find all call sites project-wide. Far superior to generic text search/ripgrep as it filters " +
				"out comments, strings, and unrelated name collisions.",
			expectedDescKeyword: "Far superior to generic text search/ripgrep",
			requiredFields:      []string{"action", "symbol"},
			properties: map[string]propertyExpectation{
				"action": {
					propType:    "string",
					description: "'definition' resolves origin file and line of an import; 'references' searches all usage sites across the project",
					enumValues:  []string{"definition", "references"},
				},
				"symbol": {
					propType:    "string",
					description: "Target symbol name to trace",
				},
				"file_path": {
					propType:    "string",
					description: "Current file path (required when action='definition')",
				},
				"limit": {
					propType:    "integer",
					description: "Maximum reference results to return (references mode only, default: 30)",
				},
			},
		},
		{
			name: ToolNameAudit,
			fullDescription: "Analyze semantic impact and code health of uncommitted git diffs. " +
				"Audits cognitive complexity spikes, public API signature modifications, and missing docstrings. " +
				"MUST be run before marking code implementation tasks as completed in SDD workflows.",
			expectedDescKeyword: "MUST be run before marking code implementation tasks as completed",
			requiredFields:      nil,
			properties: map[string]propertyExpectation{
				"base": {
					propType:    "string",
					description: "Git revision or branch to compare against (default: 'HEAD')",
				},
				"require_doc": {
					propType:    "boolean",
					description: "Audit presence of docstrings on modified public functions (default: true)",
				},
				"max_complexity": {
					propType:    "integer",
					description: "Threshold for cognitive complexity statements per function before flagging warnings (default: 15)",
				},
			},
		},
	}

	// Detroit School: Test using the real MCPServer instance without mocks.
	server := NewServer("test-server", "1.0.0", nil)
	underlying := server.GetUnderlyingServer()
	registeredTools := underlying.ListTools()

	require.Len(t, registeredTools, len(testCases), "Server should have exactly 4 tools registered")

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			serverTool, exists := registeredTools[tc.name]
			require.True(t, exists, "Tool '%s' must be registered in the MCP server", tc.name)

			tool := serverTool.Tool
			assert.Equal(t, tc.name, tool.Name)
			assert.Equal(t, tc.fullDescription, tool.Description)
			assert.Contains(t, tool.Description, tc.expectedDescKeyword, "Negative affordance keyword must be present in description")

			// Validate required fields
			if len(tc.requiredFields) == 0 {
				assert.Empty(t, tool.InputSchema.Required)
			} else {
				assert.ElementsMatch(t, tc.requiredFields, tool.InputSchema.Required)
			}

			// Validate properties
			assert.Len(t, tool.InputSchema.Properties, len(tc.properties))
			for propName, expectedProp := range tc.properties {
				rawProp, propExists := tool.InputSchema.Properties[propName]
				require.True(t, propExists, "Property '%s' must exist on tool '%s'", propName, tc.name)

				propMap, ok := rawProp.(map[string]any)
				require.True(t, ok, "Property '%s' should be a map[string]any", propName)

				assert.Equal(t, expectedProp.propType, propMap["type"], "Property '%s' type mismatch", propName)
				assert.Equal(t, expectedProp.description, propMap["description"], "Property '%s' description mismatch", propName)

				if len(expectedProp.enumValues) > 0 {
					rawEnum, enumExists := propMap["enum"]
					require.True(t, enumExists, "Property '%s' must have enum values", propName)
					var actualEnums []string
					switch v := rawEnum.(type) {
					case []string:
						actualEnums = v
					case []any:
						for _, item := range v {
							actualEnums = append(actualEnums, item.(string))
						}
					default:
						t.Fatalf("Unexpected enum type: %T", rawEnum)
					}
					assert.Equal(t, expectedProp.enumValues, actualEnums)
				}
			}
		})
	}
}

// TestCoreTools_List validates that CoreTools returns all 4 expected tools.
func TestCoreTools_List(t *testing.T) {
	tools := CoreTools()
	require.Len(t, tools, 4)

	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}

	assert.Contains(t, names, ToolNameExplore)
	assert.Contains(t, names, ToolNameSymbol)
	assert.Contains(t, names, ToolNameTrace)
	assert.Contains(t, names, ToolNameAudit)
}

// TestCoreTools_RealHandlersRegistered validates that all 4 registered core tools
// have non-nil real handlers attached rather than stubs.
func TestCoreTools_RealHandlersRegistered(t *testing.T) {
	server := NewServer("test-server", "1.0.0", outline.NewOutliner())
	underlying := server.GetUnderlyingServer()
	coreTools := []string{ToolNameExplore, ToolNameSymbol, ToolNameTrace, ToolNameAudit}

	for _, toolName := range coreTools {
		t.Run("real_"+toolName, func(t *testing.T) {
			st := underlying.GetTool(toolName)
			require.NotNil(t, st)
			require.NotNil(t, st.Handler)

			// Invoke handler with empty request to verify it's a real handler, not returning "not implemented yet"
			req := mcp.CallToolRequest{}
			req.Params.Name = toolName
			result, err := st.Handler(context.Background(), req)
			assert.NoError(t, err)
			require.NotNil(t, result)
			if result.IsError && len(result.Content) > 0 {
				textContent, ok := result.Content[0].(mcp.TextContent)
				require.True(t, ok)
				assert.NotContains(t, textContent.Text, "not implemented yet")
			}
		})
	}
}
