package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewServer_Defaults(t *testing.T) {
	s := NewServer("", "", nil)
	require.NotNil(t, s)
	assert.NotNil(t, s.GetUnderlyingServer())
	assert.Nil(t, s.Outliner())

	// Test with explicit name and version
	s2 := NewServer("custom-name", "1.2.3", nil)
	require.NotNil(t, s2)
	assert.NotNil(t, s2.GetUnderlyingServer())
}

// TestMCPServer_Serve_InitializeAndToolsList tests MCP lifecycle initialization and tools/list response.
func TestMCPServer_Serve_InitializeAndToolsList(t *testing.T) {
	s := NewServer("test-digger", "0.3.1", nil)

	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- s.Serve(ctx, serverIn, serverOut)
	}()

	scanner := bufio.NewScanner(clientIn)

	// 1. Send "initialize" request
	initReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": mcp.LATEST_PROTOCOL_VERSION,
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "test-client",
				"version": "1.0.0",
			},
		},
	}
	initReqBytes, err := json.Marshal(initReq)
	require.NoError(t, err)

	_, err = clientOut.Write(append(initReqBytes, '\n'))
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
	assert.Equal(t, "test-digger", initResp.Result.ServerInfo.Name)
	assert.Equal(t, "0.3.1", initResp.Result.ServerInfo.Version)
	assert.NotEmpty(t, initResp.Result.ProtocolVersion)

	// 2. Send "notifications/initialized" notification
	initializedNotif := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
		"params":  map[string]interface{}{},
	}
	notifBytes, err := json.Marshal(initializedNotif)
	require.NoError(t, err)
	_, err = clientOut.Write(append(notifBytes, '\n'))
	require.NoError(t, err)

	// 3. Send "tools/list" request
	toolsListReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
		"params":  map[string]interface{}{},
	}
	toolsListBytes, err := json.Marshal(toolsListReq)
	require.NoError(t, err)
	_, err = clientOut.Write(append(toolsListBytes, '\n'))
	require.NoError(t, err)

	require.True(t, scanner.Scan(), "Scanner should read response for tools/list")
	toolsListRespLine := scanner.Bytes()

	var toolsListResp struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			Tools []interface{} `json:"tools"`
		} `json:"result"`
	}
	err = json.Unmarshal(toolsListRespLine, &toolsListResp)
	require.NoError(t, err, "Response should be valid JSON: %s", string(toolsListRespLine))

	assert.Equal(t, "2.0", toolsListResp.JSONRPC)
	assert.Equal(t, 2, toolsListResp.ID)
	assert.Len(t, toolsListResp.Result.Tools, 4, "Should have 4 core tools registered")

	// 4. Shutdown cleanly by closing client input stream
	err = clientOut.Close()
	require.NoError(t, err)

	// Server should exit gracefully
	select {
	case srvErr := <-serverErrCh:
		assert.NoError(t, srvErr)
	case <-time.After(2 * time.Second):
		t.Fatal("Server did not stop within timeout after client closed stream")
	}
}

func TestMCPServer_Serve_ContextCancellation(t *testing.T) {
	s := NewServer("test-digger", "0.3.1", nil)

	serverIn, clientOut := io.Pipe()
	_, serverOut := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- s.Serve(ctx, serverIn, serverOut)
	}()

	// Cancel context immediately
	cancel()

	select {
	case <-serverErrCh:
		// Succeeded in handling cancellation
	case <-time.After(2 * time.Second):
		t.Fatal("Server did not stop upon context cancellation")
	}

	_ = clientOut.Close()
}
