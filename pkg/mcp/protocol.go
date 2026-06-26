package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"calculator-server/internal/types"
)

const (
	ProtocolVersion = "2025-11-25"

	// Standard JSON-RPC 2.0 error codes
	ErrorCodeInvalidRequest = -32600
	ErrorCodeMethodNotFound = -32601
	ErrorCodeInvalidParams  = -32602
	ErrorCodeInternalError  = -32603

	// Application-specific error code ranges for semantic HTTP status mapping
	// Authentication errors (-1000 to -1099) → HTTP 401 Unauthorized
	ErrorCodeAuthenticationRequired = -1000
	ErrorCodeInvalidCredentials     = -1001
	ErrorCodeTokenExpired           = -1002
	ErrorCodeTokenInvalid           = -1003

	// Authorization errors (-1100 to -1199) → HTTP 403 Forbidden
	ErrorCodeAccessDenied           = -1100
	ErrorCodeInsufficientPrivileges = -1101
	ErrorCodeResourceForbidden      = -1102

	// Validation errors (-1200 to -1299) → HTTP 422 Unprocessable Entity
	ErrorCodeValidationFailed     = -1200
	ErrorCodeInvalidFormat        = -1201
	ErrorCodeMissingRequiredField = -1202
	ErrorCodeValueOutOfRange      = -1203

	// Resource not found errors (-1300 to -1399) → HTTP 404 Not Found
	ErrorCodeResourceNotFound = -1300
	ErrorCodeEndpointNotFound = -1301
	ErrorCodeToolNotFound     = -1302

	// Conflict errors (-1400 to -1499) → HTTP 409 Conflict
	ErrorCodeResourceConflict    = -1400
	ErrorCodeDuplicateResource   = -1401
	ErrorCodeConcurrencyConflict = -1402

	// Rate limiting errors (-1500 to -1599) → HTTP 429 Too Many Requests
	ErrorCodeRateLimitExceeded = -1500
	ErrorCodeQuotaExceeded     = -1501
	ErrorCodeTooManyRequests   = -1502

	// Business logic errors (-2000 to -2999) → HTTP 400 Bad Request
	ErrorCodeBusinessRuleViolation = -2000
	ErrorCodeInvalidOperation      = -2001
	ErrorCodePreconditionFailed    = -2002
	ErrorCodeInvalidState          = -2003

	// Configuration and setup errors (-3000 to -3999) → HTTP 500 Internal Server Error
	ErrorCodeConfigurationError = -3000
	ErrorCodeServiceUnavailable = -3001
	ErrorCodeDependencyFailure  = -3002
)

var supportedProtocolVersions = map[string]struct{}{
	ProtocolVersion: {},
	"2025-06-18":    {},
	"2025-03-26":    {},
	"2024-11-05":    {},
}

type Server struct {
	tools   map[string]ToolHandler
	schemas map[string]ToolSchema
}

type ToolSchema struct {
	Name        string
	Description string
	InputSchema map[string]interface{}
}

type ToolHandler func(params map[string]interface{}) (interface{}, error)

// Transport defines the interface for different transport mechanisms
type Transport interface {
	Start() error
	Stop(ctx context.Context) error
}

// StdioTransport implements stdio transport for MCP protocol
type StdioTransport struct {
	server *Server
}

// NewStdioTransport creates a new stdio transport instance
func NewStdioTransport(server *Server) *StdioTransport {
	return &StdioTransport{server: server}
}

func NewServer() *Server {
	return &Server{
		tools:   make(map[string]ToolHandler),
		schemas: make(map[string]ToolSchema),
	}
}

func (s *Server) RegisterTool(name string, description string, inputSchema map[string]interface{}, handler ToolHandler) {
	s.tools[name] = handler
	s.schemas[name] = ToolSchema{
		Name:        name,
		Description: description,
		InputSchema: inputSchema,
	}
}

func IsNotification(req types.MCPRequest) bool {
	return req.HasMethod && !req.HasID
}

func IsSupportedNotification(req types.MCPRequest) bool {
	return strings.HasPrefix(req.Method, "notifications/")
}

func IsJSONRPCResponse(req types.MCPRequest) bool {
	return !req.HasMethod && req.HasID && (req.HasResult || req.HasError)
}

func HasValidRequestID(req types.MCPRequest) bool {
	return req.HasID && req.ID != nil
}

func IsSupportedProtocolVersion(version string) bool {
	_, ok := supportedProtocolVersions[version]
	return ok
}

func negotiateProtocolVersion(params json.RawMessage) string {
	if len(params) == 0 {
		return ProtocolVersion
	}

	var initializeParams struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(params, &initializeParams); err != nil {
		return ProtocolVersion
	}

	if IsSupportedProtocolVersion(initializeParams.ProtocolVersion) {
		return initializeParams.ProtocolVersion
	}

	return ProtocolVersion
}

func (s *Server) HandleRequest(req types.MCPRequest) types.MCPResponse {
	response := types.MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
	}

	switch req.Method {
	case "initialize":
		protocolVersion := negotiateProtocolVersion(req.Params)
		response.Result = map[string]interface{}{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{
					"listChanged": false,
				},
			},
			"serverInfo": map[string]interface{}{
				"name":    "calculator-server",
				"version": "1.2.0",
			},
		}
	case "ping":
		response.Result = map[string]interface{}{}
	case "tools/list":
		tools := []types.Tool{}
		for _, schema := range s.schemas {
			tool := types.Tool{
				Name:        schema.Name,
				Description: schema.Description,
				InputSchema: schema.InputSchema,
			}
			tools = append(tools, tool)
		}
		response.Result = types.ListToolsResult{Tools: tools}
	case "tools/call":
		var params types.CallToolParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			response.Error = &types.MCPError{
				Code:    ErrorCodeInvalidParams,
				Message: "Invalid parameters",
				Data:    err.Error(),
			}
			return response
		}

		handler, exists := s.tools[params.Name]
		if !exists {
			response.Error = &types.MCPError{
				Code:    ErrorCodeMethodNotFound,
				Message: "Tool not found",
				Data:    params.Name,
			}
			return response
		}

		result, err := handler(params.Arguments)
		if err != nil {
			response.Result = types.CallToolResult{
				Content: []types.ContentBlock{
					{
						Type: "text",
						Text: err.Error(),
					},
				},
				IsError: true,
			}
			return response
		}

		resultJSON, _ := json.Marshal(result)
		response.Result = types.CallToolResult{
			Content: []types.ContentBlock{
				{
					Type: "text",
					Text: string(resultJSON),
				},
			},
			StructuredContent: result,
		}
	default:
		response.Error = &types.MCPError{
			Code:    ErrorCodeMethodNotFound,
			Message: "Method not found",
			Data:    req.Method,
		}
	}

	return response
}

// Run starts the stdio transport (maintained for backward compatibility)
func (s *Server) Run() error {
	transport := NewStdioTransport(s)
	return transport.Start()
}

// Start implements the Transport interface for stdio transport
func (st *StdioTransport) Start() error {
	scanner := bufio.NewScanner(os.Stdin)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var req types.MCPRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			// Try to extract ID from the raw JSON for better error reporting
			var rawMap map[string]interface{}
			var responseID interface{}
			if json.Unmarshal([]byte(line), &rawMap) == nil {
				if id, exists := rawMap["id"]; exists {
					responseID = id
				}
			}

			response := types.MCPResponse{
				JSONRPC: "2.0",
				ID:      responseID, // Include ID if we could extract it
				Error: &types.MCPError{
					Code:    ErrorCodeInvalidRequest,
					Message: "Parse error",
					Data:    err.Error(),
				},
			}
			st.writeResponse(response)
			continue
		}

		if IsNotification(req) && IsSupportedNotification(req) {
			continue
		}

		response := st.server.HandleRequest(req)
		st.writeResponse(response)
	}

	return scanner.Err()
}

// Stop implements the Transport interface for stdio transport
func (st *StdioTransport) Stop(ctx context.Context) error {
	// Stdio transport doesn't need explicit stopping
	return nil
}

// writeResponse is now part of the StdioTransport
func (st *StdioTransport) writeResponse(response types.MCPResponse) {
	responseJSON, err := json.Marshal(response)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling response: %v\n", err)
		return
	}

	fmt.Println(string(responseJSON))
}
