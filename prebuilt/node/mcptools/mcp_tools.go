package mcptools

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/futurxlab/golanggraph/state"

	"github.com/futurxlab/golanggraph/logger"
	"github.com/futurxlab/golanggraph/utils/cache"
	"github.com/futurxlab/golanggraph/xerror"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tmc/langchaingo/llms"
)

const (
	NodeName = "MCPToolsNode"
)

const (
	toolSplitter     = "___"
	defaultMCPResult = "no result"
)

type MCPToolEvent struct {
	ToolName   string `json:"tool_name"`
	ToolArgs   string `json:"tool_args"`
	ToolResult string `json:"tool_result"`
	Status     string `json:"status"`
}

func (e *MCPToolEvent) String() string {
	json, err := json.Marshal(e)
	if err != nil {
		return ""
	}
	return string(json)
}

type MCPServer struct {
	// sse server
	URL string

	// stdio server
	Command string
	Env     []string
	Args    []string
}

type Options struct {
	MCPServers              map[string]MCPServer
	MemCache                *cache.MemCache
	Logger                  logger.ILogger
	EnableParallelExecution bool
}

type Option func(*Options)

func WithMCPServers(servers map[string]MCPServer) Option {
	return func(o *Options) {
		o.MCPServers = servers
	}
}

func WithLogger(logger logger.ILogger) Option {
	return func(o *Options) {
		o.Logger = logger
	}
}

func WithMemCache(memcache *cache.MemCache) Option {
	return func(o *Options) {
		o.MemCache = memcache
	}
}

func WithParallelExecution(enable bool) Option {
	return func(o *Options) {
		o.EnableParallelExecution = enable
	}
}

type MCP struct {
	memcache                *cache.MemCache
	stdIOClients            map[string]*client.Client
	mcpServers              map[string]MCPServer
	logger                  logger.ILogger
	enableParallelExecution bool
}

func (m *MCP) initialize(ctx context.Context) (map[string]client.MCPClient, error) {
	m.logger.Infof(ctx, "Initializing MCP tools")
	sseMCPClients := make(map[string]client.MCPClient)
	for name, server := range m.mcpServers {
		// init MCP client
		if server.URL != "" {
			sseClient, err := client.NewSSEMCPClient(server.URL)
			if err != nil {
				return nil, xerror.Wrap(err)
			}

			// Start the client
			if err := sseClient.Start(ctx); err != nil {
				return nil, xerror.Wrap(err)
			}

			// Initialize the client
			request := mcp.InitializeRequest{}
			if _, err := sseClient.Initialize(ctx, request); err != nil {
				return nil, xerror.Wrap(err)
			}

			sseMCPClients[name] = sseClient
		} else if server.Command != "" {
			stdioClient, ok := m.stdIOClients[name]
			if !ok {
				stdioClient, err := client.NewStdioMCPClient(server.Command, server.Env, server.Args...)
				if err != nil {
					return nil, xerror.Wrap(err)
				}

				m.stdIOClients[name] = stdioClient
			}
			sseMCPClients[name] = stdioClient
		}
	}

	return sseMCPClients, nil
}

func (m *MCP) close(sseMCPClients map[string]client.MCPClient) error {
	for _, sseClient := range sseMCPClients {
		if sseClient.(*client.Client) == nil {
			continue
		}
		_ = sseClient.Close()
	}
	return nil
}

func (m *MCP) Tools(ctx context.Context) []llms.Tool {

	ctx = context.WithoutCancel(ctx)
	tools := make([]llms.Tool, 0)

	if m.memcache != nil {
		tools, ok := m.memcache.Get("mcp_tools")
		if ok {
			return tools.([]llms.Tool)
		}
	}

	sseMCPClients, err := m.initialize(ctx)
	if err != nil {
		m.logger.Warnf(ctx, "failed to initialize mcp tools %s", err)
		return nil
	}
	defer func(m *MCP, sseMCPClients map[string]client.MCPClient) {
		_ = m.close(sseMCPClients)
	}(m, sseMCPClients)

	request := mcp.ListToolsRequest{}
	for mapName, sseClient := range sseMCPClients {
		response, err := sseClient.ListTools(ctx, request)
		if err != nil {
			m.logger.Warnf(ctx, "failed to list tools %s", err)
			continue
		}

		for _, tool := range response.Tools {
			tools = append(tools, llms.Tool{
				Type: "function",
				Function: &llms.FunctionDefinition{
					Name:        mapName + toolSplitter + tool.Name,
					Description: tool.Description,
					Parameters:  tool.InputSchema,
				},
			})
		}
	}

	if m.memcache != nil {
		m.memcache.SetWithTTL("mcp_tools", tools, 0, time.Minute*5)
	}

	return tools
}

func (m *MCP) Name() string {
	return NodeName
}

func (m *MCP) Run(ctx context.Context, currentState *state.State, streamFunc flowcontract.StreamFunc) error {
	if len(currentState.History) == 0 {
		return nil
	}

	sseMCPClients, err := m.initialize(ctx)
	if err != nil {
		m.logger.Warnf(ctx, "failed to initialize mcp tools %s", err)
		return xerror.Wrap(err)
	}

	defer func(m *MCP, sseMCPClients map[string]client.MCPClient) {
		_ = m.close(sseMCPClients)
	}(m, sseMCPClients)

	lastHistory := currentState.History[len(currentState.History)-1]

	if m.enableParallelExecution {
		return m.runParallel(ctx, currentState, streamFunc, lastHistory, sseMCPClients)
	} else {
		return m.runSerial(ctx, currentState, streamFunc, lastHistory, sseMCPClients)
	}
}

func (m *MCP) runParallel(
	ctx context.Context,
	currentState *state.State,
	streamFunc flowcontract.StreamFunc,
	lastHistory llms.MessageContent,
	sseMCPClients map[string]client.MCPClient,
) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	messages := make([]llms.MessageContent, 0)

	for _, part := range lastHistory.Parts {
		if toolCallPart, ok := part.(llms.ToolCall); ok {
			wg.Add(1)
			go func(toolCallPart llms.ToolCall) {
				defer wg.Done()

				extensionName := toolCallPart.FunctionCall.Name

				toolNameParts := strings.Split(extensionName, toolSplitter)
				if len(toolNameParts) != 2 {
					mu.Lock()
					if firstErr == nil {
						firstErr = xerror.New("invalid tool name")
					}
					mu.Unlock()
					return
				}

				mcpName := toolNameParts[0]
				toolName := toolNameParts[1]

				mcpClient, ok := sseMCPClients[mcpName]
				if !ok {
					m.logger.Warnf(ctx, "mcp client not found %s", mcpName)
					return
				}

				message := llms.MessageContent{
					Role:  llms.ChatMessageTypeTool,
					Parts: []llms.ContentPart{},
				}

				defer func() {
					if len(message.Parts) == 0 {
						message.Parts = append(message.Parts, llms.ToolCallResponse{
							ToolCallID: toolCallPart.ID,
							Content:    defaultMCPResult,
						})
					}
					messages = append(messages, message)
				}()

				toolEvent := MCPToolEvent{
					ToolName: toolName,
					ToolArgs: toolCallPart.FunctionCall.Arguments,
					Status:   "processing",
				}

				m.logger.Infof(ctx, "calling mcp tool %s %s %s", mcpName, toolName, toolCallPart.FunctionCall.Arguments)

				_ = streamFunc(ctx, &flowcontract.FlowStreamEvent{
					FullState: currentState,
					Chunk:     toolEvent.String(),
				})

				// call mcp tool
				request := mcp.CallToolRequest{}
				request.Params.Name = toolName
				arguments := make(map[string]interface{})
				err := json.Unmarshal([]byte(toolCallPart.FunctionCall.Arguments), &arguments)
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = xerror.Wrap(err)
					}
					mu.Unlock()
					return
				}
				request.Params.Arguments = arguments

				response, err := mcpClient.CallTool(ctx, request)
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = xerror.Wrap(err)
					}
					mu.Unlock()
					return
				}

				jsonContent, err := json.Marshal(response.Content)
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = xerror.Wrap(err)
					}
					mu.Unlock()
					return
				}

				toolEvent.ToolResult = string(jsonContent)
				toolEvent.Status = "completed"

				_ = streamFunc(ctx, &flowcontract.FlowStreamEvent{
					FullState: currentState,
					Chunk:     toolEvent.String(),
				})

				mu.Lock()
				message.Parts = append(message.Parts, llms.ToolCallResponse{
					ToolCallID: toolCallPart.ID,
					Content:    string(jsonContent),
				})
				mu.Unlock()
			}(toolCallPart)
		}
	}

	wg.Wait()

	if firstErr != nil {
		return firstErr
	}

	currentState.History = append(currentState.History, messages...)

	return nil
}

func (m *MCP) runSerial(
	ctx context.Context,
	currentState *state.State,
	streamFunc flowcontract.StreamFunc,
	lastHistory llms.MessageContent,
	sseMCPClients map[string]client.MCPClient,
) error {
	messages := make([]llms.MessageContent, 0)

	for _, part := range lastHistory.Parts {
		if toolCallPart, ok := part.(llms.ToolCall); ok {
			extensionName := toolCallPart.FunctionCall.Name

			toolNameParts := strings.Split(extensionName, toolSplitter)
			if len(toolNameParts) != 2 {
				return xerror.New("invalid tool name")
			}

			mcpName := toolNameParts[0]
			toolName := toolNameParts[1]

			mcpClient, ok := sseMCPClients[mcpName]
			if !ok {
				m.logger.Warnf(ctx, "mcp client not found %s", mcpName)
				continue
			}

			toolEvent := MCPToolEvent{
				ToolName: toolName,
				ToolArgs: toolCallPart.FunctionCall.Arguments,
				Status:   "processing",
			}

			_ = streamFunc(ctx, &flowcontract.FlowStreamEvent{
				FullState: currentState,
				Chunk:     toolEvent.String(),
			})

			message := llms.MessageContent{
				Role:  llms.ChatMessageTypeTool,
				Parts: []llms.ContentPart{},
			}

			// call mcp tool
			request := mcp.CallToolRequest{}
			request.Params.Name = toolName
			arguments := make(map[string]interface{})
			err := json.Unmarshal([]byte(toolCallPart.FunctionCall.Arguments), &arguments)
			if err != nil {
				return xerror.Wrap(err)
			}
			request.Params.Arguments = arguments

			response, err := mcpClient.CallTool(ctx, request)
			if err != nil {
				return xerror.Wrap(err)
			}

			jsonContent, err := json.Marshal(response.Content)
			if err != nil {
				return xerror.Wrap(err)
			}

			toolEvent.ToolResult = string(jsonContent)
			toolEvent.Status = "completed"

			_ = streamFunc(ctx, &flowcontract.FlowStreamEvent{
				FullState: currentState,
				Chunk:     toolEvent.String(),
			})

			message.Parts = append(
				message.Parts,
				llms.ToolCallResponse{
					ToolCallID: toolCallPart.ID,
					Content:    string(jsonContent),
				},
			)

			messages = append(messages, message)
		}
	}

	currentState.History = append(currentState.History, messages...)

	return nil
}

func NewMCPTools(opts ...Option) (*MCP, error) {
	defaultLogger, err := logger.NewLogger()
	if err != nil {
		return nil, xerror.Wrap(err)
	}

	options := &Options{
		Logger:                  defaultLogger,
		EnableParallelExecution: false,
	}

	for _, opt := range opts {
		opt(options)
	}

	// initialize StdIO clients
	var stdIDClients = make(map[string]*client.Client)
	for name, srv := range options.MCPServers {
		if srv.Command != "" {
			stdioClient, err := client.NewStdioMCPClient(srv.Command, srv.Env, srv.Args...)
			if err != nil {
				return nil, xerror.Wrap(err)
			}

			stdIDClients[name] = stdioClient
		}
	}

	return &MCP{
		logger:                  options.Logger,
		mcpServers:              options.MCPServers,
		memcache:                options.MemCache,
		stdIOClients:            stdIDClients,
		enableParallelExecution: options.EnableParallelExecution,
	}, nil
}
