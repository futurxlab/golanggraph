package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Yet-Another-AI-Project/kiwi-lib/logger"
	"github.com/Yet-Another-AI-Project/kiwi-lib/xerror"
	"github.com/futurxlab/golanggraph/checkpointer"
	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/futurxlab/golanggraph/edge"
	"github.com/futurxlab/golanggraph/flow"
	"github.com/futurxlab/golanggraph/prebuilt/edge/toolcondition"
	"github.com/futurxlab/golanggraph/prebuilt/node/model"
	"github.com/futurxlab/golanggraph/prebuilt/node/tools"
	"github.com/futurxlab/golanggraph/state"
	"github.com/tmc/langchaingo/llms"
)

type Option func(*Agent)

func WithName(name string) Option {
	return func(a *Agent) {
		a.name = name
	}
}

func WithModel(model llms.Model) Option {
	return func(a *Agent) {
		a.model = model
	}
}

func WithModelOptions(options ...llms.CallOption) Option {
	return func(a *Agent) {
		a.modelOptions = options
	}
}

func WithTools(tools []tools.ITool) Option {
	return func(a *Agent) {
		a.tools = tools
	}
}

func WithMaxToolCalls(n int) Option {
	return func(a *Agent) {
		a.maxToolCalls = n
	}
}

func WithResponseValidator(fn func(response string) error) Option {
	return func(a *Agent) {
		a.responseValidator = fn
	}
}

func WithContextWindow(n int) Option {
	return func(a *Agent) {
		a.contextWindow = n
	}
}

func WithSubAgent(name string, node flowcontract.Node) Option {
	return func(a *Agent) {
		a.subAgents[name] = node
	}
}

func WithBeforeModelHook(hooks ...HookFunc) Option {
	return func(a *Agent) {
		a.beforeModelHooks = append(a.beforeModelHooks, hooks...)
	}
}

func WithAfterModelHook(hooks ...HookFunc) Option {
	return func(a *Agent) {
		a.afterModelHooks = append(a.afterModelHooks, hooks...)
	}
}

func WithBeforeToolsHook(hooks ...HookFunc) Option {
	return func(a *Agent) {
		a.beforeToolsHooks = append(a.beforeToolsHooks, hooks...)
	}
}

func WithAfterToolsHook(hooks ...HookFunc) Option {
	return func(a *Agent) {
		a.afterToolsHooks = append(a.afterToolsHooks, hooks...)
	}
}

func WithLogger(l logger.ILogger) Option {
	return func(a *Agent) {
		a.logger = l
	}
}

type HookFunc func(ctx context.Context, s *state.State) *flowcontract.HookResult

type Agent struct {
	name              string
	model             llms.Model
	modelOptions      []llms.CallOption
	tools             []tools.ITool
	maxToolCalls      int
	contextWindow     int
	responseFormat    json.RawMessage
	responseValidator func(response string) error
	subAgents         map[string]flowcontract.Node
	logger            logger.ILogger

	beforeModelHooks []HookFunc
	afterModelHooks  []HookFunc
	beforeToolsHooks []HookFunc
	afterToolsHooks  []HookFunc
}

func (a *Agent) Name() string {
	return a.name
}

func (a *Agent) modelNodeName() string {
	return a.name + "/model"
}

func (a *Agent) toolsNodeName() string {
	return a.name + "/tools"
}

func (a *Agent) Run(ctx context.Context, parentState *state.State, streamFunc flowcontract.StreamFunc) error {
	modelNode, err := a.newModelNode()
	if err != nil {
		return xerror.Wrap(err)
	}
	toolsNode, err := a.newToolsNode()
	if err != nil {
		return xerror.Wrap(err)
	}

	tc := toolcondition.NewToolCondition(
		a.maxToolCalls,
		toolsNode.Name(),
		modelNode.Name(),
		flow.EndNode,
	)

	innerFlow, err := flow.NewFlowBuilder(a.logger).
		SetName(a.name).
		SetCheckpointer(checkpointer.NewInMemoryCheckpointer()).
		SetWorkerCount(1).
		AddNode(modelNode).
		AddNode(toolsNode).
		AddEdge(edge.Edge{From: flow.StartNode, To: modelNode.Name()}).
		AddEdge(edge.Edge{
			From:          modelNode.Name(),
			ConditionalTo: []string{flow.EndNode, toolsNode.Name()},
			ConditionFunc: tc.Condition,
		}).
		AddEdge(edge.Edge{From: toolsNode.Name(), To: modelNode.Name()}).
		Compile()
	if err != nil {
		return xerror.Wrap(err)
	}

	resultState, err := innerFlow.Exec(ctx, *parentState, streamFunc)
	if err != nil {
		return xerror.Wrap(err)
	}

	parentState.History = resultState.History
	parentState.Metadata = resultState.Metadata

	return nil
}

func (a *Agent) newModelNode() (*model.ModelNode, error) {
	allTools := make([]llms.Tool, 0)
	for _, tool := range a.tools {
		allTools = append(allTools, tool.Tools(context.Background())...)
	}
	if len(a.subAgents) > 0 {
		dt := newDelegateTaskTool(a.subAgents)
		allTools = append(allTools, dt.Tools(context.Background())...)
	}

	beforeHooks := make([]func(ctx context.Context, currentState *state.State) *flowcontract.HookResult, 0)
	afterHooks := make([]func(ctx context.Context, currentState *state.State) *flowcontract.HookResult, 0)

	if a.contextWindow > 0 {
		beforeHooks = append(beforeHooks, a.contextCompressHook())
	}
	for _, h := range a.beforeModelHooks {
		beforeHooks = append(beforeHooks, h)
	}

	if a.responseValidator != nil {
		afterHooks = append(afterHooks, a.responseValidationHook(a.modelNodeName()))
	}
	for _, h := range a.afterModelHooks {
		afterHooks = append(afterHooks, h)
	}

	modelOpts := []model.ModelOption{
		model.WithName(a.modelNodeName()),
		model.WithLLM(a.model),
		model.WithTools(allTools),
	}
	if len(beforeHooks) > 0 {
		modelOpts = append(modelOpts, model.WithBeforeRunHook(beforeHooks...))
	}
	if len(afterHooks) > 0 {
		modelOpts = append(modelOpts, model.WithAfterRunHook(afterHooks...))
	}

	if a.logger != nil {
		modelOpts = append(modelOpts, model.WithLogger(a.logger))
	}

	return model.NewModelNode(modelOpts...)
}

func (a *Agent) newToolsNode() (*tools.Tools, error) {
	allITools := a.tools
	if len(a.subAgents) > 0 {
		allITools = append(append([]tools.ITool{}, a.tools...), newDelegateTaskTool(a.subAgents))
	}

	beforeHooks := make([]func(ctx context.Context, currentState *state.State) *flowcontract.HookResult, 0)
	afterHooks := make([]func(ctx context.Context, currentState *state.State) *flowcontract.HookResult, 0)

	for _, h := range a.beforeToolsHooks {
		beforeHooks = append(beforeHooks, h)
	}

	if a.maxToolCalls > 0 {
		afterHooks = append(afterHooks, a.maxToolCallHook(a.modelNodeName()))
	}
	for _, h := range a.afterToolsHooks {
		afterHooks = append(afterHooks, h)
	}

	toolsOpts := []tools.Option{
		tools.WithNodeName(a.toolsNodeName()),
		tools.WithTools(allITools),
	}
	if len(beforeHooks) > 0 {
		toolsOpts = append(toolsOpts, tools.WithBeforeRunHook(beforeHooks...))
	}
	if len(afterHooks) > 0 {
		toolsOpts = append(toolsOpts, tools.WithAfterRunHook(afterHooks...))
	}

	if a.logger != nil {
		toolsOpts = append(toolsOpts, tools.WithLogger(a.logger))
	}

	return tools.NewTools(toolsOpts...)
}

func NewAgent(opts ...Option) (*Agent, error) {
	l, err := logger.NewLogger()
	if err != nil {
		return nil, xerror.Wrap(err)
	}

	a := &Agent{
		name:         "agent",
		maxToolCalls: 10,
		subAgents:    make(map[string]flowcontract.Node),
		logger:       l,
	}

	for _, opt := range opts {
		opt(a)
	}

	if a.model == nil {
		return nil, fmt.Errorf("model is required: use WithModel")
	}

	if a.tools == nil && len(a.subAgents) == 0 {
		return nil, fmt.Errorf("tools are required: use WithTools or WithSubAgent")
	}
	if a.tools == nil {
		a.tools = make([]tools.ITool, 0)
	}

	return a, nil
}
