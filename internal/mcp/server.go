package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/metavert-io/flipbook/internal/config"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    serverCapabilities `json:"capabilities"`
	ServerInfo      serverInfo         `json:"serverInfo"`
}

type serverCapabilities struct {
	Tools *toolsCap `json:"tools,omitempty"`
}

type toolsCap struct{}

type tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema inputSchema `json:"inputSchema"`
}

type inputSchema struct {
	Type                 string                 `json:"type"`
	Properties           map[string]interface{} `json:"properties,omitempty"`
	Required             []string               `json:"required,omitempty"`
	AdditionalProperties bool                   `json:"additionalProperties"`
}

type toolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

type toolResult struct {
	Content           []contentBlock `json:"content"`
	IsError           bool           `json:"isError,omitempty"`
	StructuredContent interface{}    `json:"structuredContent,omitempty"`
}

type contentBlock struct {
	Type string `json:"text"`
	Text string `json:"text"`
}

func Run() {
	log.SetOutput(os.Stderr)
	log.SetPrefix("[flipbook-mcp] ")
	cfg := config.Load()
	s := &server{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		client:  &http.Client{Timeout: 2 * time.Minute},
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 200*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			log.Printf("Invalid JSON: %v", err)
			continue
		}
		if resp := s.handle(req); resp != nil {
			data, _ := json.Marshal(resp)
			_, _ = fmt.Fprintf(os.Stdout, "%s\n", data)
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("Scanner error: %v", err)
	}
}

type server struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func (s *server) handle(req request) *response {
	switch req.Method {
	case "initialize":
		return &response{
			JSONRPC: "2.0", ID: req.ID,
			Result: initResult{
				ProtocolVersion: "2024-11-05", Capabilities: serverCapabilities{Tools: &toolsCap{}},
				ServerInfo: serverInfo{Name: "flipbook", Version: "2.0.0"},
			},
		}
	case "notifications/initialized":
		return nil
	case "tools/list":
		return &response{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{"tools": s.toolDefinitions()}}
	case "tools/call":
		var params toolCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return s.errorResp(req.ID, -32602, "Invalid params")
		}
		return &response{JSONRPC: "2.0", ID: req.ID, Result: s.callTool(params)}
	case "shutdown":
		return &response{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{}}
	default:
		return s.errorResp(req.ID, -32601, "Method not found: "+req.Method)
	}
}

func (s *server) errorResp(id json.RawMessage, code int, msg string) *response {
	return &response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}
