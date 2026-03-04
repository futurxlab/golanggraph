# GoLangGraph Human-in-the-Loop 功能设计方案

## 一、背景与动机

### 为什么需要 Human-in-the-Loop

在 AI Agent 工作流中，很多场景需要人类介入决策：

| 场景 | 说明 | 例子 |
|------|------|------|
| **Tool 调用前审批** | AI 要调用外部工具/API 前，让人类确认 | 执行 SQL、发送邮件、调用支付接口 |
| **关键操作审批** | 高风险操作前暂停等待人类确认 | 转账、删除数据、发布上线 |
| **AI 输出编辑** | 人类审查并可能修改 AI 的输出后再继续 | 编辑邮件草稿、修改报告内容 |
| **多轮信息收集** | 在流程中暂停收集用户输入信息 | 表单填写、确认身份信息 |

目前 golanggraph 没有内置的中断/恢复机制，用户无法在工作流执行过程中暂停等待人类输入。

### LangGraph 的做法

LangGraph (Python) 通过 3 个核心组件实现 human-in-the-loop：

```python
# 1. interrupt() — 在节点内任意位置暂停执行
from langgraph.types import interrupt
def approval_node(state):
    decision = interrupt({"question": "是否批准?", "details": state["action"]})
    if decision:
        return Command(goto="proceed")
    else:
        return Command(goto="cancel")

# 2. Checkpointer — 持久化暂停时的完整状态
checkpointer = MemorySaver()
graph = builder.compile(checkpointer=checkpointer)

# 3. Command(resume=...) — 传入人类决策恢复执行
result = graph.invoke(initial_state, config)     # 碰到 interrupt → 暂停
print(result["__interrupt__"])                    # 查看 interrupt payload
resumed = graph.invoke(Command(resume=True), config)  # 恢复执行
```

**关键特性**：
- `interrupt()` 是**动态的**，可以放在代码的任何位置、任何条件分支中
- payload 必须是 JSON 可序列化的
- 支持循环中反复 interrupt（如输入校验场景）
- `thread_id` 作为状态恢复的 key

---

## 二、设计原则

### P1: 向后兼容 (Backward Compatible)
> 现有的 `Node` interface `{ Name(); Run(ctx, *State, StreamFunc) error }` 是核心契约。**不能修改**。
> 
> InterruptError 作为 error 的子类型返回，完全符合现有接口契约。

### P2: Go 惯用风格 (Go Idiomatic)
> 不照搬 LangGraph Python 的 `interrupt()` 异常抛出机制。
> 
> 使用 Go 惯用的**特殊 error 返回**模式：节点 `Run()` 返回 `InterruptError` 类型的 error 表示中断。与 `io.EOF` 标记流结束的模式一致。

### P3: 复用现有基础设施
> 中断状态的持久化完全复用现有 Checkpointer 接口（InMemory / Redis），不引入新的存储接口。
> 
> 恢复逻辑参考现有 `Resume()` 方法的结构，新增 `ResumeWithValue()` 方法。

### P4: 节点重新执行 (Re-execution, Not Mid-function Resume)
> 恢复执行时，被中断的节点从头开始重新执行（而不是从中断点继续）。
> 
> 节点通过 `state.GetResumeValue()` 检查是否是 resume 执行，据此决定行为。这是最简单、最安全的方式。

### P5: 不引入新依赖
> 实现只依赖现有的标准库和项目内部包，不引入新的第三方包。

---

## 三、现状分析

### 当前架构

```
contract/
  node.go        → Node interface { Name(); Run(ctx, *State, StreamFunc) error }
  edge.go        → ConditionEdgeFunc
  models.go      → StreamFunc, FlowStreamEvent
  checkpointer.go→ Checkpointer interface (Save, SaveWrite, GetLatest, GetByID, List, GetPendingWrites)
  hooks.go       → BeforeRunHook, AfterRunHook (可选接口)

state/state.go   → State { History, Metadata, threadID, node, nextNodes }

flow/
  flow.go        → Flow.Exec() worker queue pattern, processNode(), Resume()
  flow_builder.go→ FlowBuilder: AddNode/AddEdge/Compile

checkpointer/
  entry.go       → CheckpointEntry { ID, ParentID, Step, State, Source }, PendingWrite { TaskID, NodeName, State }
  inmemory_checkpointer.go → InMemoryCheckpointer
  redis_checkpointer.go    → RedisCheckpointer
```

### 现有能力与差距

| 组件 | 现状 | 差距 |
|------|------|------|
| **Checkpointer** | ✅ 已有 InMemory + Redis，支持 Save/GetLatest/GetPendingWrites | 够用，无需修改 |
| **State** | ✅ 已有 threadID, node, nextNodes, History, Metadata | 需要新增 interrupt/resume 相关字段 |
| **Resume** | ✅ 已有 `flow.Resume()`，用于 crash recovery | 需要新增 `ResumeWithValue()` 方法 |
| **InterruptError** | ❌ 不存在 | 核心缺失 — 需要新增 |
| **Exec 返回** | 现在只返回 `(State, error)`，error 只表示失败 | 需要区分 interrupt 和 failure |

---

## 四、实现方案

### 4.1 InterruptError 类型 + Interrupt() 辅助函数

**新文件：`contract/interrupt.go`**

```go
package flowcontract

import "fmt"

// InterruptError 表示节点请求中断执行以等待人类输入。
// 节点在 Run() 中返回此 error 即可暂停工作流。
// 与 io.EOF 标记流结束的模式一致 — 不是"错误"，而是"信号"。
type InterruptError struct {
    Payload interface{} // 传递给人类的信息（必须 JSON 可序列化）
}

func (e *InterruptError) Error() string {
    return fmt.Sprintf("interrupt: %v", e.Payload)
}

// Interrupt 创建一个 InterruptError。
// 在节点的 Run() 方法中调用：return flowcontract.Interrupt(payload)
func Interrupt(payload interface{}) error {
    return &InterruptError{Payload: payload}
}

// IsInterrupt 检查 error 是否为 InterruptError，并返回它。
// 用法：if ie, ok := flowcontract.IsInterrupt(err); ok { ... }
func IsInterrupt(err error) (*InterruptError, bool) {
    if err == nil {
        return nil, false
    }
    // 使用 errors.As 遍历 wrap chain
    var ie *InterruptError
    if errors.As(err, &ie) {
        return ie, true
    }
    return nil, false
}
```

**使用方式（节点内）**：

```go
func (n *ApprovalNode) Run(ctx context.Context, s *state.State, sf flowcontract.StreamFunc) error {
    // 检查是否是 resume 执行
    if resumeValue := s.GetResumeValue(); resumeValue != nil {
        approved, ok := resumeValue.(bool)
        if ok && approved {
            // 人类批准了，继续执行
            fmt.Println("Action approved, proceeding...")
            return nil
        }
        // 人类拒绝了
        return fmt.Errorf("action rejected by human")
    }

    // 首次执行：暂停等待人类审批
    return flowcontract.Interrupt(map[string]interface{}{
        "action":  "send_email",
        "to":      "customer@example.com",
        "subject": "Your order has been shipped",
        "message": "Approve sending this email?",
    })
}
```

**设计决策**：
- `InterruptError` 实现 `error` 接口，完全兼容现有 Node 接口
- `Payload` 用 `interface{}` 类型，用户自行保证 JSON 可序列化
- 提供 `IsInterrupt()` 辅助函数，避免用户直接做 type assertion
- 不做 payload 验证 — 保持简单

---

### 4.2 State 扩展 — interrupt/resume 字段

**修改文件：`state/state.go`**

```go
type State struct {
    History  []llms.MessageContent
    Metadata map[string]interface{}

    // internal parameters
    threadID  string
    node      string
    nextNodes []string

    // interrupt/resume 相关（新增）
    interruptPayload interface{} // interrupt 时传递的 payload
    resumeValue      interface{} // resume 时人类传入的值
    interrupted      bool        // 是否处于中断状态
}

// --- 新增方法 ---

func (s *State) SetInterruptPayload(payload interface{}) {
    s.interruptPayload = payload
}

func (s *State) GetInterruptPayload() interface{} {
    return s.interruptPayload
}

func (s *State) SetResumeValue(value interface{}) {
    s.resumeValue = value
}

func (s *State) GetResumeValue() interface{} {
    return s.resumeValue
}

func (s *State) IsInterrupted() bool {
    return s.interrupted
}

func (s *State) SetInterrupted(v bool) {
    s.interrupted = v
}

// ClearInterrupt 清除所有 interrupt 相关字段（但保留 resumeValue）
func (s *State) ClearInterrupt() {
    s.interruptPayload = nil
    s.interrupted = false
}
```

**Serialize/Deserialize 更新**：

```go
func (s *State) Serialize() ([]byte, error) {
    m := make(map[string]interface{})
    m["threadID"] = s.threadID
    m["node"] = s.node
    m["nextNodes"] = s.nextNodes
    m["history"] = s.History
    m["metadata"] = s.Metadata
    // 新增
    m["interruptPayload"] = s.interruptPayload
    m["resumeValue"] = s.resumeValue
    m["interrupted"] = s.interrupted
    // ...
}
```

**Merge 行为**：interrupt 相关字段**不参与 Merge**（与 threadID, node 一样属于 flow 控制字段）。

**设计决策**：
- 遵循现有 State 的私有字段 + getter/setter 模式
- interrupt 字段不参与 Merge — 它们是 flow 层面的控制信息，不是业务数据
- `ClearInterrupt()` 不清除 `resumeValue`，因为 resume 执行时节点需要读取它

---

### 4.3 Flow 引擎中断处理

**修改文件：`flow/flow.go` — `processNode` 方法**

在现有的 `nodeEntry.node.Run()` 返回 error 之后，插入 InterruptError 检测：

```go
// processNode 中，Run() 之后的处理：
if err := nodeEntry.node.Run(ctx, &fullState, streamFunc); err != nil {
    // === 新增：InterruptError 检测 ===
    if interruptErr, ok := flowcontract.IsInterrupt(err); ok {
        // 1. 将 interrupt 信息写入 state
        fullState.SetInterruptPayload(interruptErr.Payload)
        fullState.SetInterrupted(true)
        fullState.SetNode(node)
        fullState.SetNextNodes([]string{node}) // resume 时重新执行此节点

        // 2. 保存 interrupt checkpoint
        f.Lock()
        f.step++
        step := f.step
        f.Unlock()
        entry := &checkpointer.CheckpointEntry{
            ID:        uuid.New().String(),
            Step:      step,
            State:     &fullState,
            Source:    "interrupt", // 特殊 source 标记
            Timestamp: time.Now(),
        }
        if saveErr := f.checkpointer.Save(ctx, fullState.GetThreadID(), entry); saveErr != nil {
            return xerror.Wrap(saveErr)
        }
        f.Lock()
        f.lastCheckpointID = entry.ID
        f.Unlock()

        // 3. 返回 InterruptError（不用 xerror.Wrap，直接返回）
        return interruptErr
    }
    // === 原有逻辑：普通 error 处理 ===
    return xerror.Wrap(err)
}
```

**修改文件：`flow/flow.go` — `Exec` 方法**

在 worker 的 error 处理中，区分 InterruptError 和普通 error：

```go
worker := func() {
    for {
        select {
        case work, ok := <-queue:
            if !ok { return }

            if err := f.processNode(ctx, work.node, copiedNodes, work.state, queue, &wg, streamFunc); err != nil {
                // === 新增：区分 InterruptError ===
                if interruptErr, ok := flowcontract.IsInterrupt(err); ok {
                    // Interrupt 不是 fatal error，优雅停止
                    errOnce.Do(func() {
                        errMu.Lock()
                        firstErr = interruptErr
                        errMu.Unlock()
                        // 保存 interrupt 时的 state
                        fullStateMu.Lock()
                        fullState = work.state
                        fullStateMu.Unlock()
                        closeQueue()
                    })
                } else {
                    // 普通 error：原有逻辑
                    errOnce.Do(func() {
                        errMu.Lock()
                        firstErr = err
                        errMu.Unlock()
                        closeQueue()
                    })
                }
            }
        }
    }
}

// Exec 返回时：
// - 普通 error → 返回 (State{}, err)
// - InterruptError → 返回 (state_with_payload, interruptErr)
if err != nil {
    if _, ok := flowcontract.IsInterrupt(err); ok {
        // Interrupt：返回包含 payload 的 state
        return fullState, err
    }
    return state.State{}, xerror.Wrap(err)
}
```

**调用方使用**：

```go
resultState, err := myFlow.Exec(ctx, initState, streamFunc)
if err != nil {
    if ie, ok := flowcontract.IsInterrupt(err); ok {
        // 工作流被中断，等待人类输入
        fmt.Printf("Waiting for human decision: %v\n", ie.Payload)
        fmt.Printf("Thread ID: %s\n", resultState.GetThreadID())
        // ... 等待人类决策 ...
    } else {
        // 真正的错误
        log.Fatalf("Flow failed: %v", err)
    }
}
```

**设计决策**：
- InterruptError 不经过 `xerror.Wrap` — 保持原始类型，方便 `errors.As` 提取
- interrupt 时返回有效的 state（包含 payload），而非空 state
- 并发场景下，interrupt 通过 `errOnce` + `closeQueue` 自然停止其他 worker

---

### 4.4 ResumeWithValue 方法

**新增方法：`flow/flow.go`**

```go
// ResumeWithValue 恢复被 interrupt 暂停的工作流。
// resumeValue 会注入到 state 中，节点通过 state.GetResumeValue() 获取。
// 被中断的节点会从头重新执行。
func (f *Flow) ResumeWithValue(ctx context.Context, threadID string, resumeValue interface{}, streamFunc flowcontract.StreamFunc) (state.State, error) {
    // 1. 获取最新 checkpoint
    latest, err := f.checkpointer.GetLatest(ctx, threadID)
    if err != nil {
        return state.State{}, xerror.Wrap(fmt.Errorf("failed to get latest checkpoint: %w", err))
    }

    // 2. 验证是 interrupted 状态
    if !latest.State.IsInterrupted() {
        return state.State{}, fmt.Errorf("thread %s is not interrupted, cannot resume with value", threadID)
    }

    // 3. 获取 PendingWrites（可能有已完成的并行节点）
    pendingWrites, err := f.checkpointer.GetPendingWrites(ctx, threadID, latest.ID)
    if err != nil {
        return state.State{}, xerror.Wrap(fmt.Errorf("failed to get pending writes: %w", err))
    }

    // 4. 恢复 state
    resumeState := *latest.State
    f.step = latest.Step

    // 5. 合并 PendingWrites
    for _, pw := range pendingWrites {
        resumeState.Merge(pw.State)
    }

    // 6. 注入 resume value + 清除 interrupt 状态
    resumeState.SetResumeValue(resumeValue)
    resumeState.ClearInterrupt() // 清除 interrupted 和 payload，保留 resumeValue

    // 7. nextNodes 为被中断的节点（从 checkpoint 获取）
    nextNodes := latest.State.GetNextNodes()
    resumeState.SetNextNodes(nextNodes)

    // 8. 保存 resume checkpoint
    f.step++
    resumeEntry := &checkpointer.CheckpointEntry{
        ID:        uuid.New().String(),
        ParentID:  latest.ID,
        Step:      f.step,
        State:     &resumeState,
        Source:    "resume_with_value",
        Timestamp: time.Now(),
    }
    if err := f.checkpointer.Save(ctx, threadID, resumeEntry); err != nil {
        return state.State{}, xerror.Wrap(err)
    }

    // 9. 重新执行
    return f.Exec(ctx, resumeState, streamFunc)
}
```

**调用方使用**：

```go
// 第一步：执行 flow，碰到 interrupt
resultState, err := myFlow.Exec(ctx, initState, nil)
if ie, ok := flowcontract.IsInterrupt(err); ok {
    fmt.Printf("Review needed: %v\n", ie.Payload)
    threadID := resultState.GetThreadID()

    // 第二步：人类做出决策后，恢复执行
    finalState, err := myFlow.ResumeWithValue(ctx, threadID, true, nil) // true = 批准
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println("Flow completed after human approval!")
}
```

**设计决策**：
- 不修改现有 `Resume()` 方法 — 它用于 crash recovery，职责不同
- 验证 `IsInterrupted()` — 防止对非中断 thread 调用 resume
- `ClearInterrupt()` 清除 interrupted 和 payload，但**保留 resumeValue** — 节点需要读取它
- checkpoint Source = `"resume_with_value"` — 区分于 crash recovery 的 `"resume"`

---

## 五、完整工作流时序

### 正常 Interrupt → Resume 流程

```
调用方                        Flow Engine                      节点 (Node)                    Checkpointer
  │                              │                                │                              │
  │── Exec(initState) ──────────►│                                │                              │
  │                              │── Save(checkpoint, "input") ──►│                              │
  │                              │                                │◄─────────────────────────────│
  │                              │── processNode("nodeA") ───────►│                              │
  │                              │                                │── Run(ctx, state, sf)        │
  │                              │                                │    │                         │
  │                              │                                │    │ 检查 GetResumeValue()   │
  │                              │                                │    │ → nil (首次执行)         │
  │                              │                                │    │                         │
  │                              │                                │◄── return Interrupt(payload) │
  │                              │                                │                              │
  │                              │  检测到 InterruptError         │                              │
  │                              │  state.SetInterruptPayload()   │                              │
  │                              │  state.SetInterrupted(true)    │                              │
  │                              │── Save(checkpoint, "interrupt")│──────────────────────────────►│
  │                              │                                │                              │
  │◄── return (state, InterruptError) ──│                         │                              │
  │                              │                                │                              │
  │  读取 state.GetInterruptPayload()                             │                              │
  │  展示给人类审批...                                              │                              │
  │                              │                                │                              │
  │── ResumeWithValue(threadID, "approved") ─►│                   │                              │
  │                              │── GetLatest(threadID) ─────────│──────────────────────────────►│
  │                              │  验证 IsInterrupted() == true  │◄─────────────────────────────│
  │                              │  state.SetResumeValue("approved")                             │
  │                              │  state.ClearInterrupt()        │                              │
  │                              │── Save(checkpoint, "resume_with_value") ─────────────────────►│
  │                              │                                │                              │
  │                              │── Exec(resumeState) ──────────►│                              │
  │                              │── processNode("nodeA") ───────►│                              │
  │                              │                                │── Run(ctx, state, sf)        │
  │                              │                                │    │                         │
  │                              │                                │    │ 检查 GetResumeValue()   │
  │                              │                                │    │ → "approved" (resume)    │
  │                              │                                │    │ 执行审批后逻辑...         │
  │                              │                                │◄── return nil                │
  │                              │                                │                              │
  │                              │── processNode("__end__") ──────│                              │
  │◄── return (finalState, nil) ─│                                │                              │
```

---

## 六、文件变更清单

| 操作 | 文件 | 说明 |
|------|------|------|
| **新建** | `contract/interrupt.go` | `InterruptError` 类型、`Interrupt()` 函数、`IsInterrupt()` 辅助函数 |
| **修改** | `state/state.go` | 新增 `interruptPayload`、`resumeValue`、`interrupted` 字段 + getter/setter + Serialize/Deserialize 更新 |
| **修改** | `flow/flow.go` | `processNode()` 中插入 InterruptError 检测、`Exec()` 区分 interrupt/error 返回、新增 `ResumeWithValue()` 方法 |
| **新建** | `flow/interrupt_test.go` | 完整 TDD 测试套件（单元测试 + 集成测试） |
| **新建** | `examples/humaninloop/main.go` | 使用示例：审批流程演示 |

---

## 七、使用示例

### 7.1 基本审批流程

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/futurxlab/golanggraph/checkpointer"
    flowcontract "github.com/futurxlab/golanggraph/contract"
    "github.com/futurxlab/golanggraph/edge"
    "github.com/futurxlab/golanggraph/flow"
    "github.com/futurxlab/golanggraph/state"
)

// ApprovalNode 需要人类审批的节点
type ApprovalNode struct{}

func (n *ApprovalNode) Name() string { return "approval" }

func (n *ApprovalNode) Run(ctx context.Context, s *state.State, sf flowcontract.StreamFunc) error {
    // 检查是否是 resume 执行
    if rv := s.GetResumeValue(); rv != nil {
        approved, _ := rv.(bool)
        if approved {
            fmt.Println("✅ Action approved by human!")
            return nil
        }
        return fmt.Errorf("action rejected by human")
    }

    // 首次执行：暂停等待人类审批
    return flowcontract.Interrupt(map[string]interface{}{
        "action":  "transfer_money",
        "amount":  500,
        "message": "Do you approve this transfer?",
    })
}

func main() {
    ctx := context.Background()

    cp := checkpointer.NewInMemoryCheckpointer()
    approvalNode := &ApprovalNode{}

    f, err := flow.NewFlowBuilder(myLogger).
        SetName("approval_flow").
        SetCheckpointer(cp).
        AddNode(approvalNode).
        AddEdge(edge.Edge{From: flow.StartNode, To: approvalNode.Name()}).
        AddEdge(edge.Edge{From: approvalNode.Name(), To: flow.EndNode}).
        Compile()
    if err != nil {
        log.Fatal(err)
    }

    // 第一步：执行 flow
    initState := state.State{
        Metadata: map[string]interface{}{"user": "alice"},
    }
    resultState, err := f.Exec(ctx, initState, nil)

    // 第二步：处理 interrupt
    if ie, ok := flowcontract.IsInterrupt(err); ok {
        fmt.Printf("⏸️  Human review needed: %v\n", ie.Payload)
        threadID := resultState.GetThreadID()

        // 模拟人类批准
        finalState, err := f.ResumeWithValue(ctx, threadID, true, nil)
        if err != nil {
            log.Fatal(err)
        }
        fmt.Printf("🎉 Flow completed! Final state: %+v\n", finalState)
    }
}
```

### 7.2 Tool 调用前审批

```go
type ToolApprovalNode struct{}

func (n *ToolApprovalNode) Name() string { return "tool_approval" }

func (n *ToolApprovalNode) Run(ctx context.Context, s *state.State, sf flowcontract.StreamFunc) error {
    if rv := s.GetResumeValue(); rv != nil {
        response, ok := rv.(map[string]interface{})
        if !ok {
            return fmt.Errorf("invalid resume value type")
        }

        switch response["type"] {
        case "accept":
            // 执行原始 tool call
            fmt.Println("Executing tool call...")
            return nil
        case "edit":
            // 用修改后的参数执行
            fmt.Printf("Executing with edited args: %v\n", response["args"])
            return nil
        case "reject":
            fmt.Println("Tool call rejected")
            return nil
        }
    }

    // 首次执行：展示 tool call 详情，等待审批
    return flowcontract.Interrupt(map[string]interface{}{
        "action": "execute_sql",
        "args":   map[string]interface{}{"query": "DELETE FROM users WHERE id = 42"},
        "message": "Review this SQL query before execution",
    })
}
```

### 7.3 条件边 + Interrupt 组合

```go
// 审批节点根据 resume value 路由到不同分支
type DecisionNode struct{}

func (n *DecisionNode) Name() string { return "decision" }

func (n *DecisionNode) Run(ctx context.Context, s *state.State, sf flowcontract.StreamFunc) error {
    if rv := s.GetResumeValue(); rv != nil {
        // 将决策写入 metadata，供条件边使用
        s.Metadata["decision"] = rv
        return nil
    }
    return flowcontract.Interrupt(map[string]interface{}{
        "question": "Approve or reject this action?",
        "options":  []string{"approve", "reject"},
    })
}

// 条件边函数
func decisionCondition(ctx context.Context, s state.State) (string, error) {
    decision, ok := s.Metadata["decision"].(string)
    if !ok {
        return "", fmt.Errorf("no decision found")
    }
    if decision == "approve" {
        return "approve_node", nil
    }
    return "reject_node", nil
}

// 构建 flow
f, _ := flow.NewFlowBuilder(logger).
    SetName("conditional_approval").
    SetCheckpointer(cp).
    AddNode(decisionNode).
    AddNode(approveNode).
    AddNode(rejectNode).
    AddEdge(edge.Edge{From: flow.StartNode, To: decisionNode.Name()}).
    AddEdge(edge.Edge{
        From:          decisionNode.Name(),
        ConditionalTo: []string{approveNode.Name(), rejectNode.Name()},
        ConditionFunc: decisionCondition,
    }).
    AddEdge(edge.Edge{From: approveNode.Name(), To: flow.EndNode}).
    AddEdge(edge.Edge{From: rejectNode.Name(), To: flow.EndNode}).
    Compile()
```

---

## 八、与 LangGraph 的对比

| 维度 | LangGraph (Python) | GoLangGraph 本方案 |
|------|---------------------|---------------------|
| 中断方式 | `interrupt(payload)` 函数 — 抛异常暂停 | `return Interrupt(payload)` — error 返回暂停 |
| 恢复方式 | `Command(resume=value)` 传入 Exec | `ResumeWithValue(threadID, value)` 独立方法 |
| 中断信息传递 | `result["__interrupt__"]` 特殊字段 | `Exec()` 返回 `(state, InterruptError)` |
| 状态持久化 | 编译时指定 checkpointer | 编译时指定 checkpointer（相同） |
| 节点重执行 | 从头开始（相同） | 从头开始（相同） |
| 类型安全 | 运行时检测 | 编译时 error 类型检测 |
| 多重 interrupt | 支持 | v1 不支持（一次执行一个 interrupt） |
| mid-function resume | 不支持 | 不支持（相同） |
| 复杂度 | 中（需要理解 Command 对象） | **低**（标准 Go error 模式） |

---

## 九、边界与约束

### 范围内 (In Scope)
- InterruptError 类型 + Interrupt() / IsInterrupt() 辅助函数
- State 扩展（interruptPayload, resumeValue, interrupted 字段）
- Flow 引擎中断处理（processNode + Exec 修改）
- ResumeWithValue 方法
- TDD 测试套件
- examples/humaninloop 示例

### 范围外 (Out of Scope)
- ❌ 不修改 Node 接口签名
- ❌ 不修改 Checkpointer 接口
- ❌ 不修改现有 Resume() 方法
- ❌ 不在 hooks（BeforeRun/AfterRun）中支持 interrupt
- ❌ 不做多重 interrupt 链（v1 只支持一次执行一个 interrupt）
- ❌ 不做 interrupt 超时/过期机制
- ❌ 不做 mid-function resume（节点始终从头重新执行）
- ❌ 不修复 Redis 序列化 bug（如果存在）

### 已知限制
1. **节点重执行**：被中断的节点 resume 后从头开始执行。如果节点有副作用（如写数据库），需要节点自行处理幂等性。
2. **单 interrupt**：一次 `Exec()` 调用最多触发一个 interrupt。如果有多个节点需要 interrupt，需要在 flow 设计中串行安排。
3. **并发节点**：当并行节点中某个 interrupt 时，其他正在执行的节点会通过 queue 关闭自然停止。已完成节点的 PendingWrite 不会丢失。

---

## 十、实施计划

| 阶段 | 任务 | 预估工作量 |
|------|------|-----------|
| **Wave 1** | xerror.Wrap 兼容性验证（spike） | 小 |
| **Wave 1** | InterruptError 类型定义 + Interrupt() 函数 | 小 |
| **Wave 1** | State 扩展（interrupt/resume 字段） | 小 |
| **Wave 2** | Flow 引擎中断处理（processNode + Exec 修改） | 中 |
| **Wave 2** | ResumeWithValue 方法实现 | 中 |
| **Wave 3** | 端到端集成测试 | 中 |
| **Wave 3** | examples/humaninloop 示例 | 小 |

**总预估**：中等工作量，3 个并行 wave 执行。
