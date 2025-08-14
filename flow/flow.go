package flow

import (
	"context"
	"fmt"
	"sync"
	"time"

	flowcontract "golanggraph/contract"
	"golanggraph/edge"
	"golanggraph/state"

	"golanggraph/logger"
	libutils "golanggraph/utils"
	"golanggraph/xerror"

	"github.com/google/uuid"
)

var (
	FlowWorkerCount = 2
)

var (
	StartNode = "__start__"
	EndNode   = "__end__"
)

type workItem struct {
	node  string
	state state.State
}

type nodeEntry struct {
	executing    bool
	node         flowcontract.Node
	dependencies []string
	completion   []state.State
}

type Flow struct {
	sync.Mutex
	name         string
	logger       logger.ILogger
	checkpointer flowcontract.Checkpointer
	graph        map[string][]edge.Edge
	nodes        map[string]*nodeEntry
}

func (f *Flow) Name() string {
	return f.name
}

func (f *Flow) Exec(ctx context.Context, initState state.State, streamFunc flowcontract.StreamFunc) (state.State, error) {
	if initState.GetThreadID() == "" {
		initState.SetThreadID(uuid.New().String())
	}

	if streamFunc == nil {
		streamFunc = func(ctx context.Context, event *flowcontract.FlowStreamEvent) error {
			f.logger.Infof(ctx, "flow processing event streamFunc empty %+v", event)
			return nil
		}
	}

	queue := make(chan workItem, FlowWorkerCount*10)
	var wg sync.WaitGroup

	var fullState state.State

	// 错误处理
	var firstErr error
	var errOnce sync.Once

	// copy nodes
	copiedNodes := make(map[string]*nodeEntry)
	for k, v := range f.nodes {
		copiedNodes[k] = &nodeEntry{
			node:         v.node,
			dependencies: v.dependencies,
			completion:   make([]state.State, 0),
		}
	}

	// 启动工作处理函数
	worker := func() {
		for {
			select {
			case <-ctx.Done():
				f.logger.Infof(ctx, "context canceled, queue closed")
				return
			case work, ok := <-queue:
				if !ok {
					f.logger.Infof(ctx, "manually queue closed")
					return
				}

				if work.node == EndNode {
					fullState = work.state
				}

				if err := f.processNode(ctx, work.node, copiedNodes, work.state, queue, &wg, streamFunc); err != nil {
					errOnce.Do(func() {
						firstErr = err
						close(queue)
					})
				}
			}
		}
	}

	// 启动工作线程
	for i := 0; i < FlowWorkerCount; i++ {
		libutils.SafeGo(ctx, f.logger, worker)
	}

	// 添加起始节点到队列
	wg.Add(1)
	queue <- workItem{node: StartNode, state: initState}

	// 等待所有工作完成或出错
	wg.Wait()

	if firstErr != nil {
		return state.State{}, xerror.Wrap(firstErr)
	}

	f.logger.Infof(ctx, "flow finished")

	close(queue)

	return fullState, nil
}

func (f *Flow) Resume(ctx context.Context, lastState state.State, streamFunc flowcontract.StreamFunc) error {
	panic("not implemented")
}

// processNode 处理单个节点，替代原来的递归execNode方法
func (f *Flow) processNode(ctx context.Context, node string, copiedNodes map[string]*nodeEntry, fullState state.State, queue chan<- workItem, wg *sync.WaitGroup, streamFunc flowcontract.StreamFunc) error {
	defer wg.Done()
	nodeEntry, ok := copiedNodes[node]
	if !ok {
		return xerror.New(fmt.Sprintf("node %s not found", node))
	}

	f.Lock()
	if nodeEntry.executing {
		f.logger.Warnf(ctx, "node already executing %s", node)
		f.Unlock()
		return nil
	}

	f.logger.Infof(ctx, "executing node %s", node)
	nodeEntry.executing = true
	f.Unlock()

	// 如果有依赖节点，等待前置节点完成
	if len(nodeEntry.dependencies) > 0 {
		f.logger.Infof(ctx, "waiting for dependencies %s, %+v", node, nodeEntry.dependencies)
		states, err := f.waitDependencies(ctx, copiedNodes, nodeEntry.dependencies)
		if err != nil {
			return xerror.Wrap(err)
		}

		for _, state := range states {
			fullState.Merge(&state)
		}
	}

	if node == EndNode {
		f.logger.Infof(ctx, "reached end node %s", node)
		return nil
	}

	if node != StartNode {
		// 执行节点
		if err := nodeEntry.node.Run(ctx, &fullState, streamFunc); err != nil {
			return xerror.Wrap(err)
		}

		fullState.SetNode(node)
		nodeEntry.completion = append(nodeEntry.completion, fullState)

		if streamFuncErr := streamFunc(ctx, &flowcontract.FlowStreamEvent{
			FullState: &fullState,
		}); streamFuncErr != nil {
			f.logger.Errorf(ctx, "streaming failed state: %+v, error: %s", fullState, streamFuncErr)
		}

	}

	nodeEntry.executing = false

	nextNodes := make([]string, 0)

	// 处理所有边缘，并发添加下一个节点到队列
	for _, edge := range f.graph[node] {
		nextNode := edge.To

		if len(edge.ConditionalTo) > 0 {
			condition, err := edge.ConditionFunc(ctx, fullState)
			if err != nil {
				return xerror.Wrap(err)
			}

			if condition != "" {
				nextNode = condition
			}
		}

		if nextNode == "" {
			return xerror.New(fmt.Sprintf("no next node found for edge %s", edge.To))
		}

		nextNodes = append(nextNodes, nextNode)

		wg.Add(1)
		queue <- workItem{node: nextNode, state: fullState}
	}

	// 保存检查点
	namespace := fullState.GetThreadID()
	fullState.SetNextNodes(nextNodes)
	if _, err := f.checkpointer.Save(ctx, namespace, &fullState); err != nil {
		return xerror.Wrap(err)
	}

	return nil
}

func (f *Flow) waitDependencies(ctx context.Context, copiedNodes map[string]*nodeEntry, dependencies []string) ([]state.State, error) {
	wg := sync.WaitGroup{}
	wg.Add(len(dependencies))

	timer := time.NewTimer(time.Minute * 2)

	var err error
	var states []state.State

	for _, dependency := range dependencies {
		libutils.SafeGo(ctx, f.logger, func() {
			defer wg.Done()
			f.logger.Infof(ctx, "waiting for dependency %s", dependency)
			for {
				select {
				case <-ctx.Done():
					err = xerror.New("context canceled")
					return
				case <-timer.C:
					err = xerror.New(fmt.Sprintf("dependency node %s timeout", dependency))
					return
				default:
				}

				if len(copiedNodes[dependency].completion) > 0 {
					firstState := copiedNodes[dependency].completion[0]
					states = append(states, firstState)
					copiedNodes[dependency].completion = copiedNodes[dependency].completion[1:]
					return
				}

				time.Sleep(time.Second * 2)
			}
		})
	}

	wg.Wait()

	if err != nil {
		return nil, err
	}

	return states, nil
}

func (f *Flow) Draw(ctx context.Context) {
	panic("not implemented")
}
