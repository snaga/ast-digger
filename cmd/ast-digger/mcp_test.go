package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMcpCmd_Integration verifies the end-to-end MCP server lifecycle over stdio pipes,
// ensuring the initialize handshake and tools/list response return the expected 4 core tools.
func TestMcpCmd_Integration(t *testing.T) {
	origStdin := os.Stdin
	origStdout := os.Stdout
	defer func() {
		os.Stdin = origStdin
		os.Stdout = origStdout
	}()

	rStdin, wStdin, err := os.Pipe()
	require.NoError(t, err)
	defer rStdin.Close()

	rStdout, wStdout, err := os.Pipe()
	require.NoError(t, err)
	defer rStdout.Close()

	os.Stdin = rStdin
	os.Stdout = wStdout

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := newRootCmd()
	cmd.SetArgs([]string{"mcp"})

	cmdErrCh := make(chan error, 1)
	go func() {
		defer wStdout.Close()
		cmdErrCh <- cmd.ExecuteContext(ctx)
	}()

	scanner := bufio.NewScanner(rStdout)

	// 1. Send "initialize" request
	initReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "test-client",
				"version": "1.0.0",
			},
		},
	}
	initBytes, err := json.Marshal(initReq)
	require.NoError(t, err)
	_, err = wStdin.Write(append(initBytes, '\n'))
	require.NoError(t, err)

	require.True(t, scanner.Scan(), "Scanner should read response for initialize")
	initRespLine := scanner.Bytes()

	var initResp struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
			Capabilities map[string]interface{} `json:"capabilities"`
		} `json:"result"`
	}
	err = json.Unmarshal(initRespLine, &initResp)
	require.NoError(t, err, "Response should be valid JSON: %s", string(initRespLine))
	assert.Equal(t, "2.0", initResp.JSONRPC)
	assert.Equal(t, 1, initResp.ID)
	assert.Equal(t, "ast-digger", initResp.Result.ServerInfo.Name)
	assert.NotEmpty(t, initResp.Result.ProtocolVersion)

	// 2. Send "notifications/initialized" notification
	initializedNotif := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
		"params":  map[string]interface{}{},
	}
	notifBytes, err := json.Marshal(initializedNotif)
	require.NoError(t, err)
	_, err = wStdin.Write(append(notifBytes, '\n'))
	require.NoError(t, err)

	// 3. Send "tools/list" request
	toolsReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
		"params":  map[string]interface{}{},
	}
	toolsBytes, err := json.Marshal(toolsReq)
	require.NoError(t, err)
	_, err = wStdin.Write(append(toolsBytes, '\n'))
	require.NoError(t, err)

	require.True(t, scanner.Scan(), "Scanner should read response for tools/list")
	toolsRespLine := scanner.Bytes()

	var toolsResp struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	err = json.Unmarshal(toolsRespLine, &toolsResp)
	require.NoError(t, err, "Response should be valid JSON: %s", string(toolsRespLine))
	assert.Equal(t, "2.0", toolsResp.JSONRPC)
	assert.Equal(t, 2, toolsResp.ID)
	assert.Len(t, toolsResp.Result.Tools, 4, "Should have 4 core tools registered")

	toolNames := make([]string, 0, len(toolsResp.Result.Tools))
	for _, tool := range toolsResp.Result.Tools {
		toolNames = append(toolNames, tool.Name)
	}
	assert.ElementsMatch(t, []string{"ast_explore", "ast_symbol", "ast_trace", "ast_audit"}, toolNames)

	// 4. Shutdown cleanly by closing client input stream
	err = wStdin.Close()
	require.NoError(t, err)

	// Server should exit gracefully
	select {
	case cmdErr := <-cmdErrCh:
		assert.NoError(t, cmdErr)
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("Command did not terminate within timeout after client closed stdin")
	}
}

// TestMcpCmd_ArgsError verifies that unexpected positional arguments cause the command to fail.
func TestMcpCmd_ArgsError(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"mcp", "unexpected_arg"})
	err := cmd.Execute()
	assert.Error(t, err)
}
