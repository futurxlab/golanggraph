# GoLangGraph Context Engineering 优化方案设计报告

## 一、现状分析

### 当前架构

```
contract/
  node.go        → Node interface { Name(); Run(ctx, *State, StreamFunc) error }
  edge.go        → ConditionEdgeFunc
  models.go      → StreamFunc, FlowStreamEvent
  checkpointer.go→ Checkpointer interface

state/state.go   → State { History []llms.MessageContent, Metadata map[string]any }

flow/
  flow.go        → Flow.Exec() worker queue pattern, processNode()
  flow_builder.go→ FlowBuilder: AddNode/AddEdge/Compile

edge/edge.go     → Edge { From, To, ConditionalTo, ConditionFunc }

prebuilt/
  node/chat/     → ChatNode: 调用LLM, 将response放入History
  node/tools/    → Tools node: 解析ToolCall, 并行执行tools, 返回ToolCallResponse
  node/mcptools/ → MCP工具节点: 对接MCP Server
  edge/toolcondition/ → ToolCondition: 根据tool_count和ToolCall presence决定路由
```

### 当前不足

| 问题 | 现状 | 影响 |
|------|------|------|
| **无输出验证** | ChatNode.Run() 调LLM后直接把response追加到History，无任何验证 | 格式错误、内容错误直接透传给用户 |
| **Tool call无错误处理** | tools.Run() 中tool执行失败只log，不构造error response返回给agent | agent不知道tool失败了，可能继续hallucinate |
| **无循环检测** | ToolCondition只有简单的`tool_count >= limit`就跳出 | 无法区分"合理的多轮tool"和"死循环"；limit是写死的 |
| **无hook机制** | Node接口只有Name()和Run()，没有前后钩子 | 无法在不修改node实现的情况下注入通用逻辑 |
| **无内置agent** | 用户需要自己手动组装 ChatNode→ToolCondition→ToolsNode 的ReAct loop | 每次都要写重复的boilerplate |

---

## 二、设计原则

### P1: 向后兼容 (Backward Compatible)
> 现有的 `Node` interface `{ Name(); Run() }` 是核心契约。**不能修改**，只能扩展。
> 
> 所有新功能通过 **可选接口 (optional interface)** + **组合 (composition)** 实现。用户现有代码零修改。

### P2: 轻量优先 (Lightweight First)
> 不照搬LangGraph的重量级middleware系统（4种hook + onion composition）。
> 
> Go的哲学是简单。用 **interface升级检测** 替代middleware注册。flow执行时检查node是否实现了hook接口，有就调用。

### P3: 错误即上下文 (Error as Context)
> tool执行失败、输出验证失败，这些错误不应该终止flow，而应该**构造成消息注入回History**，让agent自己修正。
> 
> 这是 context engineering 的核心思想：把错误变成agent的输入上下文。

### P4: 可组合的 Reflection (Composable Reflection)
> 验证、反思、压缩这些能力应该是**独立的、可组合的函数/接口**，用户可以自由选用。
> 
> 内置agent预装最佳实践组合，但用户可以自定义。

### P5: 不引入新依赖
> 优化只依赖现有的 `langchaingo/llms` 和标准库。不引入新的第三方包。

---

## 三、实现方案

### 3.1 Node Lifecycle Hooks（轻量hook机制）

**方案：可选接口升级，而非修改核心Node接口**

```go
// contract/hooks.go — 新文件

// BeforeRunHook 可选接口，Node可以选择实现
// 在 Node.Run() 执行前调用
// 返回的 State 会替换传入的 State（可修改上下文）
// 返回 error 则跳过该 Node 的执行
type BeforeRunHook interface {
    BeforeRun(ctx context.Context, state *state.State) error
}

// AfterRunHook 可选接口，Node可以选择实现
// 在 Node.Run() 成功执行后调用
// 可用于：日志记录、state后处理、metrics上报
type AfterRunHook interface {
    AfterRun(ctx context.Context, state *state.State) error
}
```

**Flow执行逻辑修改**（`flow.go` 的 `processNode`）：

```go
// processNode中，执行节点的部分改为：
if node != StartNode {
    fullState.SetNode(node)
    
    // === BeforeRun Hook ===
    if hook, ok := nodeEntry.node.(flowcontract.BeforeRunHook); ok {
        if err := hook.BeforeRun(ctx, &fullState); err != nil {
            return xerror.Wrap(err)
        }
    }
    
    // === Run ===
    if err := nodeEntry.node.Run(ctx, &fullState, streamFunc); err != nil {
        return xerror.Wrap(err)
    }
    
    // === AfterRun Hook ===
    if hook, ok := nodeEntry.node.(flowcontract.AfterRunHook); ok {
        if err := hook.AfterRun(ctx, &fullState); err != nil {
            return xerror.Wrap(err)
        }
    }
    
    // ... 后续逻辑不变
}
```

**设计决策**：
- 不修改 `Node` 接口，完全向后兼容
- Go惯用的 interface升级检测模式（和 `io.ReadCloser` 检测 `io.Closer` 一样）
- 比LangGraph的middleware链轻量得多
- 每个Node自己决定是否需要hook，不需要全局注册

---

### 3.2 输出内容验证 + 重试循环 (Response Validation & Retry)

**方案：Validator函数 + 带重试的ChatNode包装**

```go
// contract/validation.go — 新文件

// ValidationResult 验证结果
type ValidationResult struct {
    Valid   bool   // 验证是否通过
    Error   string // 如果验证不通过，错误描述（会注入给agent）
}

// ResponseValidator 响应验证器接口
// 用户实现该接口来定义自己的验证逻辑
type ResponseValidator func(ctx context.Context, state *state.State) ValidationResult
```

**在内置agent（见3.4）中使用，核心逻辑：**

```
Chat → 得到response → 调用 validator(state)
  → Valid=true  → 继续正常flow
  → Valid=false → 构造system message注入error → 重新调用Chat → 递归验证
  → 达到最大重试次数 → 放弃，返回最后一次结果
```

**具体实现方式**：不是修改ChatNode，而是在 **内置Agent的ReAct循环** 中加入验证步骤。验证失败时：

```go
// 构造验证错误消息注入History
errorMessage := llms.MessageContent{
    Role: llms.ChatMessageTypeHuman,  // 或 System，取决于策略
    Parts: []llms.ContentPart{
        llms.TextContent{
            Text: fmt.Sprintf(
                "[VALIDATION ERROR] Your previous response failed validation: %s\n"+
                "Please correct your response and try again.",
                result.Error,
            ),
        },
    },
}
state.History = append(state.History, errorMessage)
// 然后重新执行ChatNode.Run()
```

**设计决策**：
- Validator是一个简单的函数类型，不是复杂的interface
- 用户完全控制验证逻辑（格式、内容、业务规则等）
- 错误变成context注入给agent（P3原则）
- 有maxRetry保护，不会无限循环

---

### 3.3 Tool Call 错误处理 + 死循环检测

#### 3.3.1 Tool执行错误 → Error Tool Response

**当前问题**：`tools.Run()` 中tool执行失败只是log warning，不返回error response给agent。

**修改方案**（修改 `prebuilt/node/tools/tools.go`）：

```go
// tool执行失败时，构造error tool response而不是静默丢弃
if err := tool.Run(ctx, state, streamFunc); err != nil {
    m.logger.Errorf(ctx, "tool run failed %s: %s", name, err)
    
    // 构造error tool response给agent
    for _, part := range message.Parts {
        if tc, ok := part.(llms.ToolCall); ok {
            currentState.History = append(currentState.History, llms.MessageContent{
                Role: llms.ChatMessageTypeTool,
                Parts: []llms.ContentPart{llms.ToolCallResponse{
                    ToolCallID: tc.ID,
                    Name:       tc.FunctionCall.Name,
                    Content:    fmt.Sprintf("[TOOL ERROR] %s", err.Error()),
                }},
            })
        }
    }
    return // 不终止flow，让agent看到error后自行决策
}
```

对 `mcptools` 同理：MCP调用失败时构造error response而非 `return xerror.Wrap(err)`。

#### 3.3.2 Tool参数错误

```go
// JSON解析tool参数失败时
err := json.Unmarshal([]byte(toolCallPart.FunctionCall.Arguments), &arguments)
if err != nil {
    // 不要 return error 终止flow！
    // 构造参数错误的tool response
    currentState.History = append(currentState.History, llms.MessageContent{
        Role: llms.ChatMessageTypeTool,
        Parts: []llms.ContentPart{llms.ToolCallResponse{
            ToolCallID: toolCallPart.ID,
            Content: fmt.Sprintf(
                "[TOOL ERROR] Invalid arguments for tool '%s': %s. "+
                "Expected valid JSON. Your input was: %s",
                toolCallPart.FunctionCall.Name, err.Error(),
                toolCallPart.FunctionCall.Arguments,
            ),
        }},
    })
    continue // 继续处理其他tool call
}
```

#### 3.3.3 死循环检测（增强ToolCondition）

**当前 ToolCondition 只有粗糙的 `tool_count >= limit`**。增强为：

```go
// contract/agent.go 或 prebuilt/edge/toolcondition 增强

type LoopDetectorConfig struct {
    MaxIterations      int           // 最大迭代次数（现有的limit）
    MaxConsecutiveSame int           // 连续调用同一tool的最大次数（检测死循环）
    Timeout            time.Duration // 总超时
}
```

**死循环检测逻辑**：在 state.Metadata 中记录最近N次tool调用的tool name序列：

```go
// 检测连续相同tool调用
toolHistory := state.Metadata["_tool_call_history"].([]string)
if len(toolHistory) >= config.MaxConsecutiveSame {
    lastN := toolHistory[len(toolHistory)-config.MaxConsecutiveSame:]
    allSame := true
    for _, name := range lastN {
        if name != lastN[0] {
            allSame = false
            break
        }
    }
    if allSame {
        // 死循环检测！注入警告消息给agent
        state.History = append(state.History, llms.MessageContent{
            Role: llms.ChatMessageTypeHuman,
            Parts: []llms.ContentPart{llms.TextContent{
                Text: fmt.Sprintf(
                    "[LOOP DETECTED] You have called tool '%s' %d times consecutively "+
                    "with similar results. Please try a different approach or provide "+
                    "your final answer.",
                    lastN[0], config.MaxConsecutiveSame,
                ),
            }},
        })
        return fanoutNode, nil  // 强制退出tool loop
    }
}
```

---

### 3.4 内置Agent + 内置Hooks（核心新功能）

**这是最重要的新增。提供一个 `prebuilt/agent` 包，用户一行代码就能创建完整的ReAct agent。**

#### 3.4.1 Agent接口设计

```go
// prebuilt/agent/agent.go

type AgentConfig struct {
    // 基础配置
    Name                 string
    LLMConnectionStrings []string
    LLM                  llms.Model
    SystemPrompt         string
    Tools                []tools.ITool       // 支持普通tools
    MCPServers           map[string]mcptools.MCPServer  // 支持MCP tools
    
    // Reflection配置
    ResponseValidator    flowcontract.ResponseValidator  // 输出验证器（可选）
    MaxValidationRetries int                             // 验证重试次数，默认3
    
    // Loop控制
    MaxIterations        int           // 最大tool loop次数，默认10
    MaxConsecutiveSame   int           // 连续相同tool最大次数，默认3
    Timeout              time.Duration // 总超时，默认5分钟
    
    // Context Engineering
    EnableToolReflection     bool  // 是否启用tool call reflection
    EnableResponseReflection bool  // 是否启用response content reflection
    EnableContextCompression bool  // 是否启用context compression
    CompressionThreshold     int   // 触发压缩的History消息数阈值
    
    // Hook
    BeforeModelFunc func(ctx context.Context, state *state.State) error
    AfterModelFunc  func(ctx context.Context, state *state.State) error
    
    Logger logger.ILogger
}

// CreateAgent 一行创建完整agent
// 返回的是一个编译好的 *flow.Flow
func CreateAgent(config AgentConfig) (*flow.Flow, error)
```

#### 3.4.2 CreateAgent 内部构建的Flow拓扑

```
                    ┌──────────────────────────────┐
                    │                              │
START ──► [ChatNode] ──► [ToolCondition Edge] ──► [ToolsNode] ──┘
              │                                      │
              │ (no tool call)                       │ (tool error → 
              ▼                                      │  error response)
      [ResponseValidator]                            │
              │                                      │
         ┌────┴────┐                                 │
      valid    invalid                               │
         │      │                                    │
         ▼      └── inject error ──► [ChatNode] ◄───┘
       END
```

**关键设计决策**：**不需要把validation做成一个图中的node**。

更优雅的方式是：**ChatNode wrapper** — 创建一个 `AgentChatNode` 包装原始 `ChatNode`，在内部实现 validation loop：

```go
// prebuilt/agent/agent_chat_node.go

type AgentChatNode struct {
    chatNode         *chat.ChatNode
    validator        flowcontract.ResponseValidator
    maxRetries       int
    beforeModelFunc  func(ctx context.Context, state *state.State) error
    afterModelFunc   func(ctx context.Context, state *state.State) error
    logger           logger.ILogger
}

func (n *AgentChatNode) Name() string {
    return n.chatNode.Name()
}

func (n *AgentChatNode) Run(ctx context.Context, s *state.State, streamFunc flowcontract.StreamFunc) error {
    // BeforeModel hook
    if n.beforeModelFunc != nil {
        if err := n.beforeModelFunc(ctx, s); err != nil {
            return err
        }
    }
    
    // 带验证重试的LLM调用
    for attempt := 0; attempt <= n.maxRetries; attempt++ {
        if err := n.chatNode.Run(ctx, s, streamFunc); err != nil {
            return err
        }
        
        // AfterModel hook
        if n.afterModelFunc != nil {
            if err := n.afterModelFunc(ctx, s); err != nil {
                return err
            }
        }
        
        // 如果有validator且当前response不是tool call，则验证
        if n.validator != nil && !hasToolCalls(s) {
            result := n.validator(ctx, s)
            if result.Valid {
                return nil // 验证通过
            }
            
            if attempt < n.maxRetries {
                n.logger.Warnf(ctx, "validation failed (attempt %d/%d): %s", 
                    attempt+1, n.maxRetries, result.Error)
                
                // 注入错误消息，让agent重试
                s.History = append(s.History, llms.MessageContent{
                    Role: llms.ChatMessageTypeHuman,
                    Parts: []llms.ContentPart{llms.TextContent{
                        Text: fmt.Sprintf(
                            "[VALIDATION ERROR] Your response did not pass validation: %s\n"+
                            "Please review and provide a corrected response.",
                            result.Error,
                        ),
                    }},
                })
            }
        } else {
            return nil // 无validator或是tool call，直接通过
        }
    }
    
    n.logger.Warnf(ctx, "validation exhausted max retries, accepting last response")
    return nil
}
```

#### 3.4.3 内置Hook实现

**Tool Call Reflection Hook**（AfterModel时机）：

```go
// prebuilt/agent/hooks/tool_reflection.go

// ToolReflectionHook 在tool执行结果返回后，检查：
// 1. tool是否返回了error
// 2. tool返回的内容是否为空或异常
// 如果有问题，注入反思提示让agent重新决策
func ToolReflectionHook(ctx context.Context, state *state.State) error {
    // 检查最后一条消息是否是tool response
    if len(state.History) == 0 {
        return nil
    }
    last := state.History[len(state.History)-1]
    if last.Role != llms.ChatMessageTypeTool {
        return nil
    }
    
    for _, part := range last.Parts {
        if resp, ok := part.(llms.ToolCallResponse); ok {
            if strings.HasPrefix(resp.Content, "[TOOL ERROR]") {
                // tool执行失败，不需要额外处理
                // 因为error response已经在History里了，agent会看到
                // 但我们可以记录metrics
                state.Metadata["_last_tool_error"] = resp.Content
            }
        }
    }
    return nil
}
```

**Context Compression Hook**（BeforeModel时机）：

```go
// prebuilt/agent/hooks/context_compression.go

// ContextCompressionConfig 上下文压缩配置
type ContextCompressionConfig struct {
    Threshold      int        // 消息数超过此值触发压缩
    LLM            llms.Model // 用于生成摘要的LLM
    KeepLastN      int        // 保留最近N条消息不压缩
    SummaryPrompt  string     // 摘要提示词模板
}

// ContextCompressionHook 在调用LLM前检查History长度
// 如果超过阈值，将旧消息压缩为一条摘要消息
func NewContextCompressionHook(config ContextCompressionConfig) func(ctx context.Context, s *state.State) error {
    return func(ctx context.Context, s *state.State) error {
        if len(s.History) <= config.Threshold {
            return nil
        }
        
        // 分离：保留的消息 vs 需要压缩的消息
        cutoff := len(s.History) - config.KeepLastN
        toCompress := s.History[:cutoff]
        toKeep := s.History[cutoff:]
        
        // 调用LLM生成摘要
        summaryPrompt := fmt.Sprintf(
            "%s\n\nConversation to summarize:\n%s",
            config.SummaryPrompt,
            formatMessagesForSummary(toCompress),
        )
        
        summary, err := llms.GenerateFromSinglePrompt(ctx, config.LLM, summaryPrompt)
        if err != nil {
            // 压缩失败不应阻断主流程，只是保持原样
            return nil
        }
        
        // 用摘要替换旧消息
        compressedHistory := []llms.MessageContent{
            {
                Role: llms.ChatMessageTypeSystem,
                Parts: []llms.ContentPart{llms.TextContent{
                    Text: fmt.Sprintf("[CONVERSATION SUMMARY]\n%s", summary),
                }},
            },
        }
        s.History = append(compressedHistory, toKeep...)
        
        return nil
    }
}
```

---

### 3.5 Sub-Flow / Sub-Agent（子流程 / 子Agent支持）

**需求**：Flow中的节点可以嵌入另一个Flow，支持两种模式：
- **Mode A: SubFlowNode** — 子Flow作为DAG节点，与父Flow共享上下文
- **Mode B: DelegateAgent** — 子Flow作为Tool，通过tool call触发，上下文隔离

这两种模式覆盖了multi-agent协作的两大核心场景。

#### 3.5.1 Mode A: SubFlowNode（共享上下文的子流程节点）

**用途**：将一个编译好的`*Flow`包装为`contract.Node`，嵌入父Flow的DAG拓扑中执行。父子Flow通过`InputMapper`/`OutputMapper`控制State的流转。

```go
// prebuilt/subflow/subflow_node.go

package subflow

import (
    "context"
    "fmt"

    flowcontract "github.com/futurxlab/golanggraph/contract"
    "github.com/futurxlab/golanggraph/flow"
    "github.com/futurxlab/golanggraph/logger"
    "github.com/futurxlab/golanggraph/state"
)

// InputMapper 将父State映射为子Flow的输入State
// 返回的State会作为子Flow.Exec()的输入
// 默认行为（nil）：直接传递父State的完整拷贝
type InputMapper func(parentState *state.State) *state.State

// OutputMapper 将子Flow执行结果合并回父State
// childState是子Flow执行后的最终State
// 默认行为（nil）：将子Flow的History追加到父State.History，合并Metadata
type OutputMapper func(parentState *state.State, childState *state.State)

// SubFlowNode 将 *flow.Flow 包装为 contract.Node
// 实现了 Node 接口，可以直接通过 FlowBuilder.AddNode() 加入父Flow
type SubFlowNode struct {
    name         string
    flow         *flow.Flow
    inputMapper  InputMapper
    outputMapper OutputMapper
    logger       logger.ILogger
}

// SubFlowOption 配置选项
type SubFlowOption func(*SubFlowNode)

// WithInputMapper 自定义输入映射
func WithInputMapper(mapper InputMapper) SubFlowOption {
    return func(n *SubFlowNode) {
        n.inputMapper = mapper
    }
}

// WithOutputMapper 自定义输出映射
func WithOutputMapper(mapper OutputMapper) SubFlowOption {
    return func(n *SubFlowNode) {
        n.outputMapper = mapper
    }
}

// NewSubFlowNode 创建子流程节点
func NewSubFlowNode(name string, f *flow.Flow, logger logger.ILogger, opts ...SubFlowOption) *SubFlowNode {
    node := &SubFlowNode{
        name:   name,
        flow:   f,
        logger: logger,
    }
    for _, opt := range opts {
        opt(node)
    }
    return node
}

func (n *SubFlowNode) Name() string {
    return n.name
}

func (n *SubFlowNode) Run(ctx context.Context, s *state.State, streamFunc flowcontract.StreamFunc) error {
    // 1. 准备子Flow的输入State
    var childState *state.State
    if n.inputMapper != nil {
        childState = n.inputMapper(s)
    } else {
        // 默认：深拷贝父State
        childState = s.DeepCopy()
    }

    // 2. 执行子Flow
    resultState, err := n.flow.Exec(ctx, *childState, streamFunc)
    if err != nil {
        return fmt.Errorf("subflow '%s' execution failed: %w", n.name, err)
    }

    // 3. 将子Flow结果合并回父State
    if n.outputMapper != nil {
        n.outputMapper(s, &resultState)
    } else {
        // 默认：用子Flow的History替换父State的History，合并Metadata
        s.History = resultState.History
        for k, v := range resultState.Metadata {
            s.Metadata[k] = v
        }
    }

    return nil
}
```

**设计决策**：
- `SubFlowNode`实现`contract.Node`接口，完全兼容`FlowBuilder.AddNode()`
- `InputMapper`/`OutputMapper`给用户完全控制State流转的能力
- 默认行为是全量传递+合并，适用于大多数场景
- 子Flow的错误直接传播到父Flow（符合DAG语义）
- 支持子Flow的Streaming事件透传到父Flow

#### 3.5.2 Mode B: DelegateAgent（隔离上下文的委派Agent）

**用途**：将一个编译好的`*Flow`包装为`tools.ITool`，父Agent通过tool call来委派任务给子Agent。子Agent运行在完全隔离的上下文中，只接收task字符串，返回结果作为tool response。

```go
// prebuilt/subflow/delegate_agent.go

package subflow

import (
    "context"
    "encoding/json"
    "fmt"

    flowcontract "github.com/futurxlab/golanggraph/contract"
    "github.com/futurxlab/golanggraph/flow"
    "github.com/futurxlab/golanggraph/logger"
    "github.com/futurxlab/golanggraph/state"
    "github.com/tmc/langchaingo/llms"
)

// DelegateAgent 将 *flow.Flow 包装为 tools.ITool
// 父Agent通过 delegate_to_{name} 工具调用来触发子Agent
type DelegateAgent struct {
    name         string
    description  string
    flow         *flow.Flow
    systemPrompt string
    logger       logger.ILogger
}

// DelegateOption 配置选项
type DelegateOption func(*DelegateAgent)

// WithSystemPrompt 设置子Agent的system prompt
func WithSystemPrompt(prompt string) DelegateOption {
    return func(d *DelegateAgent) {
        d.systemPrompt = prompt
    }
}

// NewDelegateAgent 创建委派Agent
func NewDelegateAgent(name, description string, f *flow.Flow, logger logger.ILogger, opts ...DelegateOption) *DelegateAgent {
    agent := &DelegateAgent{
        name:        name,
        description: description,
        flow:        f,
        logger:      logger,
    }
    for _, opt := range opts {
        opt(agent)
    }
    return agent
}

// Tools 返回工具定义，让父Agent可以通过tool call触发
func (d *DelegateAgent) Tools(ctx context.Context) []llms.Tool {
    return []llms.Tool{{
        Type: "function",
        Function: &llms.FunctionDefinition{
            Name:        fmt.Sprintf("delegate_to_%s", d.name),
            Description: d.description,
            Parameters: map[string]any{
                "type": "object",
                "properties": map[string]any{
                    "task": map[string]any{
                        "type":        "string",
                        "description": "The task to delegate to the sub-agent",
                    },
                },
                "required": []string{"task"},
            },
        },
    }}
}

// Run 执行委派：创建隔离State，运行子Flow，将结果作为tool response返回
func (d *DelegateAgent) Run(ctx context.Context, s *state.State, streamFunc flowcontract.StreamFunc) error {
    // 1. 从父State的最后一条AI消息中提取tool call参数
    task, toolCallID, err := d.extractTaskFromToolCall(s)
    if err != nil {
        return fmt.Errorf("delegate agent '%s': %w", d.name, err)
    }

    d.logger.Infof(ctx, "delegating task to sub-agent '%s': %s", d.name, task)

    // 2. 创建隔离的子State
    childState := state.State{
        Metadata: make(map[string]any),
    }

    // 注入system prompt（如果有）
    if d.systemPrompt != "" {
        childState.History = append(childState.History, llms.MessageContent{
            Role: llms.ChatMessageTypeSystem,
            Parts: []llms.ContentPart{llms.TextContent{
                Text: d.systemPrompt,
            }},
        })
    }

    // 注入task作为user message
    childState.History = append(childState.History, llms.MessageContent{
        Role: llms.ChatMessageTypeHuman,
        Parts: []llms.ContentPart{llms.TextContent{
            Text: task,
        }},
    })

    // 3. 执行子Flow
    resultState, err := d.flow.Exec(ctx, childState, streamFunc)
    if err != nil {
        // 子Flow执行失败 → 构造error tool response（P3原则：错误即上下文）
        s.History = append(s.History, llms.MessageContent{
            Role: llms.ChatMessageTypeTool,
            Parts: []llms.ContentPart{llms.ToolCallResponse{
                ToolCallID: toolCallID,
                Name:       fmt.Sprintf("delegate_to_%s", d.name),
                Content:    fmt.Sprintf("[DELEGATE ERROR] Sub-agent '%s' failed: %s", d.name, err.Error()),
            }},
        })
        return nil // 不终止父Flow，让父Agent看到error后决策
    }

    // 4. 提取子Agent的最终响应
    response := d.extractFinalResponse(&resultState)

    // 5. 构造tool response返回给父Agent
    s.History = append(s.History, llms.MessageContent{
        Role: llms.ChatMessageTypeTool,
        Parts: []llms.ContentPart{llms.ToolCallResponse{
            ToolCallID: toolCallID,
            Name:       fmt.Sprintf("delegate_to_%s", d.name),
            Content:    response,
        }},
    })

    return nil
}

// extractTaskFromToolCall 从State的History中提取最后一个tool call的task参数
func (d *DelegateAgent) extractTaskFromToolCall(s *state.State) (task string, toolCallID string, err error) {
    toolName := fmt.Sprintf("delegate_to_%s", d.name)
    
    // 从后往前找最后一条AI消息中的tool call
    for i := len(s.History) - 1; i >= 0; i-- {
        msg := s.History[i]
        if msg.Role != llms.ChatMessageTypeAI {
            continue
        }
        for _, part := range msg.Parts {
            tc, ok := part.(llms.ToolCall)
            if !ok || tc.FunctionCall.Name != toolName {
                continue
            }
            
            var args map[string]string
            if err := json.Unmarshal([]byte(tc.FunctionCall.Arguments), &args); err != nil {
                return "", "", fmt.Errorf("failed to parse tool call arguments: %w", err)
            }
            
            taskStr, ok := args["task"]
            if !ok {
                return "", "", fmt.Errorf("tool call missing 'task' parameter")
            }
            
            return taskStr, tc.ID, nil
        }
    }
    
    return "", "", fmt.Errorf("no tool call found for '%s'", toolName)
}

// extractFinalResponse 从子Flow的最终State中提取最后一条AI消息的文本
func (d *DelegateAgent) extractFinalResponse(s *state.State) string {
    for i := len(s.History) - 1; i >= 0; i-- {
        msg := s.History[i]
        if msg.Role != llms.ChatMessageTypeAI {
            continue
        }
        for _, part := range msg.Parts {
            if text, ok := part.(llms.TextContent); ok && text.Text != "" {
                return text.Text
            }
        }
    }
    return "[No response from sub-agent]"
}
```

**设计决策**：
- `DelegateAgent`实现`tools.ITool`接口，可以直接作为tool传给`Tools Node`或`CreateAgent`
- 父Agent通过`delegate_to_{name}(task="...")`来触发，与普通tool call体验一致
- 子Agent运行在完全隔离的State中（只有system prompt + task），避免上下文污染
- 子Flow失败时构造error tool response而非终止父Flow（P3原则）
- 子Flow的Streaming事件透传到父Flow

#### 3.5.3 使用示例

**Mode A: SubFlowNode — RAG Pipeline作为子流程节点**

```go
// 1. 构建RAG子流程
ragFlow, _ := flow.NewFlowBuilder(logger).
    SetName("rag_pipeline").
    AddNode(retrieverNode).    // 检索相关文档
    AddNode(rerankNode).       // 重排序
    AddNode(contextBuilderNode). // 构建增强上下文
    AddEdge(edge.Edge{From: flow.StartNode, To: retrieverNode.Name()}).
    AddEdge(edge.Edge{From: retrieverNode.Name(), To: rerankNode.Name()}).
    AddEdge(edge.Edge{From: rerankNode.Name(), To: contextBuilderNode.Name()}).
    AddEdge(edge.Edge{From: contextBuilderNode.Name(), To: flow.EndNode}).
    Compile()

// 2. 将RAG子流程包装为节点，嵌入主流程
ragSubNode := subflow.NewSubFlowNode("rag", ragFlow, logger,
    subflow.WithInputMapper(func(parentState *state.State) *state.State {
        // 只传递最后一条用户消息给RAG
        return &state.State{
            History: []llms.MessageContent{parentState.History[len(parentState.History)-1]},
            Metadata: map[string]any{"query": parentState.GetLastUserMessage()},
        }
    }),
    subflow.WithOutputMapper(func(parentState *state.State, childState *state.State) {
        // 将RAG结果写入Metadata，供后续ChatNode使用
        parentState.Metadata["rag_context"] = childState.Metadata["retrieved_context"]
    }),
)

// 3. 构建主流程：Start → RAG子流程 → Chat → End
mainFlow, _ := flow.NewFlowBuilder(logger).
    SetName("rag_chat").
    AddNode(ragSubNode).
    AddNode(chatNode).
    AddEdge(edge.Edge{From: flow.StartNode, To: ragSubNode.Name()}).
    AddEdge(edge.Edge{From: ragSubNode.Name(), To: chatNode.Name()}).
    AddEdge(edge.Edge{From: chatNode.Name(), To: flow.EndNode}).
    Compile()
```

**Mode B: DelegateAgent — 多Agent协作**

```go
// 1. 构建专家子Agent（例如：代码审查Agent）
reviewerAgent, _ := agent.CreateAgent(agent.AgentConfig{
    Name:                 "code-reviewer",
    LLMConnectionStrings: []string{"openai;...;gpt-4o"},
    SystemPrompt:         "You are an expert code reviewer. Review the given code and provide detailed feedback.",
    MaxIterations:        5,
})

// 2. 将子Agent包装为DelegateAgent工具
reviewerTool := subflow.NewDelegateAgent(
    "code_reviewer",
    "Delegate code review tasks to a specialized code review agent. "+
        "Use this when you need thorough code review feedback.",
    reviewerAgent,
    logger,
    subflow.WithSystemPrompt(
        "You are an expert code reviewer. Analyze the code carefully and provide: "+
        "1) Bugs found, 2) Performance issues, 3) Suggestions for improvement.",
    ),
)

// 3. 构建主Agent，包含delegate工具
mainAgent, _ := agent.CreateAgent(agent.AgentConfig{
    Name:                 "orchestrator",
    LLMConnectionStrings: []string{"openai;...;gpt-4o"},
    SystemPrompt:         "You are a senior developer. Use the code_reviewer tool to review code when needed.",
    Tools:                []tools.ITool{reviewerTool},  // DelegateAgent作为工具
    MaxIterations:        10,
})
```

---

## 四、文件变更清单

| 操作 | 文件 | 说明 |
|------|------|------|
| **新建** | `contract/hooks.go` | `BeforeRunHook`、`AfterRunHook` 可选接口 |
| **新建** | `contract/validation.go` | `ResponseValidator` 类型、`ValidationResult` 结构 |
| **修改** | `flow/flow.go` | `processNode()` 中加入 hook 接口检测调用 |
| **修改** | `prebuilt/node/tools/tools.go` | tool执行失败时构造error response而非静默丢弃 |
| **修改** | `prebuilt/node/mcptools/mcp_tools.go` | 同上，MCP调用失败构造error response |
| **修改** | `prebuilt/edge/toolcondition/tool_condition.go` | 增强死循环检测（连续相同tool调用检测） |
| **新建** | `prebuilt/agent/agent.go` | `AgentConfig` + `CreateAgent()` |
| **新建** | `prebuilt/agent/agent_chat_node.go` | `AgentChatNode` — 带validation loop的ChatNode包装 |
| **新建** | `prebuilt/agent/hooks/tool_reflection.go` | Tool Reflection hook |
| **新建** | `prebuilt/agent/hooks/context_compression.go` | Context Compression hook |
| **新建** | `prebuilt/subflow/subflow_node.go` | `SubFlowNode` — 将`*Flow`包装为`Node`（共享上下文DAG节点） |
| **新建** | `prebuilt/subflow/delegate_agent.go` | `DelegateAgent` — 将`*Flow`包装为`ITool`（隔离上下文委派） |

---

## 五、使用示例

### 5.1 最简agent（一行创建）

```go
agent, err := agent.CreateAgent(agent.AgentConfig{
    Name:                 "my-agent",
    LLMConnectionStrings: []string{"openai;https://api.openai.com/v1;sk-xxx;gpt-4o"},
    SystemPrompt:         "You are a helpful assistant.",
    MCPServers: map[string]mcptools.MCPServer{
        "fetch": {URL: "https://remote.mcpservers.org/fetch"},
    },
    MaxIterations: 10,
})
```

### 5.2 带输出验证的agent

```go
agent, err := agent.CreateAgent(agent.AgentConfig{
    Name: "structured-agent",
    LLMConnectionStrings: []string{"openai;...;gpt-4o"},
    SystemPrompt: "Always respond in JSON format with fields: answer, confidence.",
    
    ResponseValidator: func(ctx context.Context, s *state.State) flowcontract.ValidationResult {
        response := s.GetLastResponse()
        var result map[string]interface{}
        if err := json.Unmarshal([]byte(response), &result); err != nil {
            return flowcontract.ValidationResult{
                Valid: false,
                Error: fmt.Sprintf("Response is not valid JSON: %s", err),
            }
        }
        if _, ok := result["answer"]; !ok {
            return flowcontract.ValidationResult{
                Valid: false,
                Error: "Response missing required field 'answer'",
            }
        }
        return flowcontract.ValidationResult{Valid: true}
    },
    MaxValidationRetries: 3,
})
```

### 5.3 带Context Compression的agent

```go
compressionLLM, _ := native.NewChatLLM([]string{"openai;...;gpt-4o-mini"})

agent, err := agent.CreateAgent(agent.AgentConfig{
    Name: "long-conversation-agent",
    LLMConnectionStrings: []string{"openai;...;gpt-4o"},
    
    EnableContextCompression: true,
    CompressionThreshold:     20,
    
    BeforeModelFunc: hooks.NewContextCompressionHook(hooks.ContextCompressionConfig{
        Threshold:     20,
        LLM:           compressionLLM,
        KeepLastN:     5,
        SummaryPrompt: "Summarize the following conversation concisely:",
    }),
})
```

### 5.4 自定义Node使用Hook（不用内置agent也能用）

```go
type MySmartNode struct {
    name string
}

func (n *MySmartNode) Name() string { return n.name }

func (n *MySmartNode) Run(ctx context.Context, s *state.State, sf flowcontract.StreamFunc) error {
    // 业务逻辑
    return nil
}

// 选择性实现BeforeRunHook
func (n *MySmartNode) BeforeRun(ctx context.Context, s *state.State) error {
    log.Printf("About to run node %s with %d messages", n.name, len(s.History))
    return nil
}

// 选择性实现AfterRunHook
func (n *MySmartNode) AfterRun(ctx context.Context, s *state.State) error {
    log.Printf("Node %s finished, response: %s", n.name, s.GetLastResponse()[:50])
    return nil
}
```

---

## 六、实现优先级建议

| 优先级 | 任务 | 原因 |
|--------|------|------|
| **P0** | Node Lifecycle Hooks (3.1) | 基础设施，其他feature都依赖 |
| **P0** | Tool error response (3.3.1 + 3.3.2) | 现在tool失败是静默的，这是bug级别的问题 |
| **P1** | 内置Agent CreateAgent() (3.4) | 用户最大的痛点是每次手动组装flow |
| **P1** | Response Validation + Retry (3.2) | context engineering的核心能力 |
| **P2** | 死循环检测 (3.3.3) | 增强可靠性 |
| **P2** | Context Compression Hook (3.4.3) | 长对话场景需要 |
| **P2** | SubFlowNode (3.5.1) | 支持子流程嵌套，复杂pipeline拆分 |
| **P2** | DelegateAgent (3.5.2) | 支持multi-agent协作，职责分离 |
| **P3** | Tool Reflection Hook | 锦上添花，tool error response已经cover了大部分场景 |

---

## 七、与LangGraph设计的对比

| 维度 | LangGraph (Python) | GoLangGraph 本方案 |
|------|---------------------|---------------------|
| Hook机制 | `AgentMiddleware` 5种hook + onion composition | 可选接口 `BeforeRunHook`/`AfterRunHook`，interface升级检测 |
| 验证 | `generate_structured_response` 额外LLM调用 | `ResponseValidator` 函数 + 内建重试循环 |
| Tool Error | 自动构造 `ToolMessage(error=...)` | 手动构造 `ToolCallResponse{Content: "[TOOL ERROR]..."}` |
| 死循环 | `recursion_limit` 全局配置 | `MaxIterations` + `MaxConsecutiveSame` 双重检测 |
| 压缩 | 需要自定义middleware | 内置 `ContextCompressionHook` |
| Agent创建 | `create_agent()` / `create_react_agent()` | `CreateAgent(AgentConfig{...})` |
| 复杂度 | 重（middleware链、策略模式、大量抽象） | **轻**（函数类型、可选接口、组合模式） |
| 子流程/子Agent | Subgraph + `send_to_agent` / `SubAgentMiddleware` | `SubFlowNode`（Node适配器） + `DelegateAgent`（ITool适配器） |

---

## 八、SubFlowNode vs DelegateAgent 对比

| 维度 | SubFlowNode (Mode A) | DelegateAgent (Mode B) |
|------|---------------------|----------------------|
| **实现接口** | `contract.Node` | `tools.ITool` |
| **触发方式** | DAG中按拓扑顺序执行 | Agent通过tool call触发 |
| **上下文关系** | 共享（通过InputMapper/OutputMapper控制） | 隔离（只传task字符串） |
| **State传递** | 父子State双向映射 | 单向：task→子，result→父 |
| **使用场景** | 复杂子工作流（如RAG pipeline作为子节点） | 专家委派（如让coding agent写代码） |
| **错误处理** | 子Flow error直接传播到父Flow | 子Flow error转换为tool error response（P3原则） |
| **Streaming** | 子Flow的stream事件透传到父Flow | 子Flow的stream事件透传到父Flow |
| **适用于** | 需要细粒度state控制的场景 | 需要职责分离的multi-agent场景 |
| **类比** | 函数调用（共享作用域） | RPC/微服务（隔离作用域） |
| **LangGraph对应** | Subgraph | `send_to_agent` / `SubAgentMiddleware` |

### 选择指南

```
你需要子流程共享父流程的上下文吗？
├── 是 → SubFlowNode（Mode A）
│        适用：RAG pipeline、数据处理pipeline、多步骤工作流
│        特点：InputMapper/OutputMapper精确控制State流转
│
└── 否 → DelegateAgent（Mode B）
         适用：专家Agent委派、multi-agent协作、任务分发
         特点：完全隔离，通过tool call语义触发
```
