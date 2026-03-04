package flow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/futurxlab/golanggraph/checkpointer"
	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/futurxlab/golanggraph/edge"
	"github.com/futurxlab/golanggraph/state"
	"github.com/tmc/langchaingo/llms"

	"github.com/Yet-Another-AI-Project/kiwi-lib/logger"
	"github.com/Yet-Another-AI-Project/kiwi-lib/xerror"
	libutils "github.com/futurxlab/golanggraph/utils"

	"github.com/google/uuid"
)

const (
	DefaultWorkerCount = 2
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
	mu           sync.Mutex
	executing    bool
	node         flowcontract.Node
	dependencies []string
	completion   []state.State
}

// cloneStateForWorkItem creates a shallow copy of the state with a deep-copied Metadata map
// to prevent concurrent map access when multiple goroutines process different nodes.
func cloneStateForWorkItem(s state.State) state.State {
	if s.Metadata == nil {
		return s
	}
	m := make(map[string]interface{}, len(s.Metadata))
	for k, v := range s.Metadata {
		m[k] = v
	}
	s.Metadata = m
	return s
}

type Flow struct {
	sync.Mutex
	name             string
	logger           logger.ILogger
	checkpointer     flowcontract.Checkpointer
	graph            map[string][]edge.Edge
	nodes            map[string]*nodeEntry
	workerCount      int
	step             int    // current superstep number
	lastCheckpointID string // 最近一次checkpoint的ID，用于关联PendingWrite
}

func (f *Flow) Name() string {
	return f.name
}

func (f *Flow) Exec(ctx context.Context, initState state.State, streamFunc flowcontract.StreamFunc) (state.State, error) {
	if initState.GetThreadID() == "" {
		initState.SetThreadID(uuid.New().String())
	}

	if initState.History == nil {
		initState.History = make([]llms.MessageContent, 0)
	}

	if initState.Metadata == nil {
		initState.Metadata = make(map[string]interface{})
	}

	if streamFunc == nil {
		streamFunc = func(ctx context.Context, event *flowcontract.FlowStreamEvent) error {
			return nil
		}
	}

	f.step = 0
	initEntry := &checkpointer.CheckpointEntry{
		ID:        uuid.New().String(),
		Step:      f.step,
		State:     &initState,
		Source:    "input",
		Timestamp: time.Now(),
	}
	if err := f.checkpointer.Save(ctx, initState.GetThreadID(), initEntry); err != nil {
		return state.State{}, xerror.Wrap(err)
	}
	f.lastCheckpointID = initEntry.ID

	return f.execFromNodes(ctx, initState, []string{StartNode}, streamFunc)
}

// execFromNodes runs the flow execution loop starting from the given entry nodes.
// For normal Exec, startNodes is [__start__]. For ResumeWithValue, startNodes is the interrupted node(s).
func (f *Flow) execFromNodes(ctx context.Context, initState state.State, startNodes []string, streamFunc flowcontract.StreamFunc) (state.State, error) {
	if streamFunc == nil {
		streamFunc = func(ctx context.Context, event *flowcontract.FlowStreamEvent) error {
			return nil
		}
	}

	queue := make(chan workItem, f.workerCount*10)
	var wg sync.WaitGroup

	var fullState state.State
	var fullStateMu sync.Mutex

	var firstErr error
	var errMu sync.Mutex
	var errOnce sync.Once

	var closeOnce sync.Once
	closeQueue := func() { closeOnce.Do(func() { close(queue) }) }

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
					fullStateMu.Lock()
					fullState = work.state
					fullStateMu.Unlock()
				}

				if err := f.processNode(ctx, work.node, copiedNodes, work.state, queue, &wg, streamFunc); err != nil {
					if _, ok := flowcontract.IsInterrupt(err); ok {
						errOnce.Do(func() {
							errMu.Lock()
							firstErr = err
							errMu.Unlock()
							closeQueue()
						})
					} else {
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

	// 启动工作线程
	for i := 0; i < f.workerCount; i++ {
		libutils.SafeGo(ctx, f.logger, worker)
	}

	// 添加起始节点到队列
	for _, startNode := range startNodes {
		wg.Add(1)
		queue <- workItem{node: startNode, state: cloneStateForWorkItem(initState)}
	}

	// 等待所有工作完成或出错
	wg.Wait()

	errMu.Lock()
	err := firstErr
	errMu.Unlock()
	if err != nil {
		if _, ok := flowcontract.IsInterrupt(err); ok {
			closeQueue()
			fullStateMu.Lock()
			interruptState := fullState
			fullStateMu.Unlock()

			if interruptState.GetThreadID() == "" {
				latest, cpErr := f.checkpointer.GetLatest(ctx, initState.GetThreadID())
				if cpErr == nil && latest != nil && latest.State != nil {
					interruptState = *latest.State
				} else {
					interruptState = initState
				}
			}

			return interruptState, err
		}
		return state.State{}, xerror.Wrap(err)
	}

	f.logger.Infof(ctx, "flow finished")

	closeQueue()

	fullStateMu.Lock()
	result := fullState
	fullStateMu.Unlock()

	return result, nil
}

func (f *Flow) Resume(ctx context.Context, threadID string, streamFunc flowcontract.StreamFunc) (state.State, error) {
	latest, err := f.checkpointer.GetLatest(ctx, threadID)
	if err != nil {
		return state.State{}, xerror.Wrap(fmt.Errorf("failed to get latest checkpoint: %w", err))
	}

	pendingWrites, err := f.checkpointer.GetPendingWrites(ctx, threadID, latest.ID)
	if err != nil {
		return state.State{}, xerror.Wrap(fmt.Errorf("failed to get pending writes: %w", err))
	}

	completedNodes := make(map[string]*state.State)
	completedTaskIDs := make(map[string]struct{})
	for _, pw := range pendingWrites {
		completedNodes[pw.NodeName] = pw.State
		completedTaskIDs[pw.TaskID] = struct{}{}
	}

	resumeState := *latest.State
	f.step = latest.Step

	nextNodes := resumeState.GetNextNodes()
	if len(nextNodes) == 0 {
		return resumeState, nil
	}

	for _, pw := range pendingWrites {
		resumeState.Merge(pw.State)
	}

	remainingNodes := make([]string, 0, len(nextNodes))
	for _, nextNode := range nextNodes {
		taskID := generateTaskID(latest.ID, nextNode, f.step)
		if _, ok := completedTaskIDs[taskID]; ok {
			continue
		}
		if _, ok := completedNodes[nextNode]; ok {
			continue
		}
		remainingNodes = append(remainingNodes, nextNode)
	}
	resumeState.SetNextNodes(remainingNodes)
	if len(remainingNodes) == 0 {
		return resumeState, nil
	}

	f.step++
	resumeEntry := &checkpointer.CheckpointEntry{
		ID:        uuid.New().String(),
		ParentID:  latest.ID,
		Step:      f.step,
		State:     &resumeState,
		Source:    "resume",
		Timestamp: time.Now(),
	}
	if err := f.checkpointer.Save(ctx, threadID, resumeEntry); err != nil {
		return state.State{}, xerror.Wrap(err)
	}

	return f.Exec(ctx, resumeState, streamFunc)
}

func (f *Flow) ResumeWithValue(ctx context.Context, threadID string, resumeValue interface{}, streamFunc flowcontract.StreamFunc) (state.State, error) {
	latest, err := f.checkpointer.GetLatest(ctx, threadID)
	if err != nil {
		return state.State{}, xerror.Wrap(fmt.Errorf("failed to get latest checkpoint: %w", err))
	}

	if !latest.State.IsInterrupted() {
		return state.State{}, xerror.New(fmt.Sprintf("thread %s is not interrupted", threadID))
	}

	pendingWrites, err := f.checkpointer.GetPendingWrites(ctx, threadID, latest.ID)
	if err != nil {
		return state.State{}, xerror.Wrap(fmt.Errorf("failed to get pending writes: %w", err))
	}

	resumeState := *latest.State
	f.step = latest.Step

	for _, pw := range pendingWrites {
		resumeState.Merge(pw.State)
	}

	resumeState.SetResumeValue(resumeValue)
	resumeState.SetInterrupted(false)
	resumeState.SetInterruptPayload(nil)

	startNodes := resumeState.GetNextNodes()
	if len(startNodes) == 0 {
		return state.State{}, xerror.New("no interrupted node to resume from")
	}

	f.step++
	resumeEntry := &checkpointer.CheckpointEntry{
		ID:        uuid.New().String(),
		ParentID:  latest.ID,
		Step:      f.step,
		State:     &resumeState,
		Source:    "resume",
		Timestamp: time.Now(),
	}
	if err := f.checkpointer.Save(ctx, threadID, resumeEntry); err != nil {
		return state.State{}, xerror.Wrap(err)
	}
	f.lastCheckpointID = resumeEntry.ID

	return f.execFromNodes(ctx, resumeState, startNodes, streamFunc)
}

// generateTaskID 生成确定性任务ID，用于crash recovery时匹配已完成的节点
func generateTaskID(checkpointID string, nodeName string, step int) string {
	data := fmt.Sprintf("%s:%s:%d", checkpointID, nodeName, step)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
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
		fullState.SetNode(node)

		if hook, ok := nodeEntry.node.(flowcontract.BeforeRunHook); ok {
			if result := hook.BeforeRun(ctx, &fullState); result != nil {
				if result.JumpToNode != "" {
					f.Lock()
					nodeEntry.executing = false
					f.Unlock()
					wg.Add(1)
					queue <- workItem{node: result.JumpToNode, state: cloneStateForWorkItem(fullState)}

					fullState.SetNextNodes([]string{result.JumpToNode})
					f.Lock()
					f.step++
					step := f.step
					f.Unlock()
					entry := &checkpointer.CheckpointEntry{
						ID:        uuid.New().String(),
						Step:      step,
						State:     &fullState,
						Source:    "loop",
						Timestamp: time.Now(),
					}
					if err := f.checkpointer.Save(ctx, fullState.GetThreadID(), entry); err != nil {
						return xerror.Wrap(err)
					}
					f.Lock()
					f.lastCheckpointID = entry.ID
					f.Unlock()
					return nil
				}
			}
		}

		if err := nodeEntry.node.Run(ctx, &fullState, streamFunc); err != nil {
			if interruptErr, ok := flowcontract.IsInterrupt(err); ok {
				fullState.SetInterruptPayload(interruptErr.Payload)
				fullState.SetInterrupted(true)
				fullState.SetNextNodes([]string{node})

				f.Lock()
				f.step++
				step := f.step
				f.Unlock()

				entry := &checkpointer.CheckpointEntry{
					ID:        uuid.New().String(),
					Step:      step,
					State:     &fullState,
					Source:    "interrupt",
					Timestamp: time.Now(),
				}
				if err := f.checkpointer.Save(ctx, fullState.GetThreadID(), entry); err != nil {
					return xerror.Wrap(err)
				}

				f.Lock()
				f.lastCheckpointID = entry.ID
				f.Unlock()

				return interruptErr
			}
			return xerror.Wrap(err)
		}

		nodeEntry.mu.Lock()
		nodeEntry.completion = append(nodeEntry.completion, fullState)
		nodeEntry.mu.Unlock()

		f.Lock()
		checkpointID := f.lastCheckpointID
		step := f.step
		f.Unlock()
		taskID := generateTaskID(checkpointID, node, step)
		write := checkpointer.PendingWrite{
			TaskID:   taskID,
			NodeName: node,
			State:    &fullState,
		}
		if err := f.checkpointer.SaveWrite(ctx, fullState.GetThreadID(), checkpointID, write); err != nil {
			f.logger.Warnf(ctx, "failed to save pending write for node %s: %s", node, err)
		}

		if streamFuncErr := streamFunc(ctx, &flowcontract.FlowStreamEvent{
			FullState: &fullState,
		}); streamFuncErr != nil {
			f.logger.Errorf(ctx, "streaming failed state: %+v, error: %s", fullState, streamFuncErr)
		}

		if hook, ok := nodeEntry.node.(flowcontract.AfterRunHook); ok {
			if result := hook.AfterRun(ctx, &fullState); result != nil {
				if result.JumpToNode != "" {
					f.Lock()
					nodeEntry.executing = false
					f.Unlock()
					wg.Add(1)
					queue <- workItem{node: result.JumpToNode, state: cloneStateForWorkItem(fullState)}

					fullState.SetNextNodes([]string{result.JumpToNode})
					f.Lock()
					f.step++
					step := f.step
					f.Unlock()
					entry := &checkpointer.CheckpointEntry{
						ID:        uuid.New().String(),
						Step:      step,
						State:     &fullState,
						Source:    "loop",
						Timestamp: time.Now(),
					}
					if err := f.checkpointer.Save(ctx, fullState.GetThreadID(), entry); err != nil {
						return xerror.Wrap(err)
					}
					f.Lock()
					f.lastCheckpointID = entry.ID
					f.Unlock()
					return nil
				}
			}
		}
	}

	f.Lock()
	nodeEntry.executing = false
	f.Unlock()

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
		queue <- workItem{node: nextNode, state: cloneStateForWorkItem(fullState)}
	}

	// 保存检查点
	fullState.SetNextNodes(nextNodes)
	f.Lock()
	f.step++
	step := f.step
	f.Unlock()
	entry := &checkpointer.CheckpointEntry{
		ID:        uuid.New().String(),
		Step:      step,
		State:     &fullState,
		Source:    "loop",
		Timestamp: time.Now(),
	}
	if err := f.checkpointer.Save(ctx, fullState.GetThreadID(), entry); err != nil {
		return xerror.Wrap(err)
	}
	f.Lock()
	f.lastCheckpointID = entry.ID
	f.Unlock()

	return nil
}

func (f *Flow) waitDependencies(ctx context.Context, copiedNodes map[string]*nodeEntry, dependencies []string) ([]state.State, error) {
	wg := sync.WaitGroup{}
	wg.Add(len(dependencies))

	timer := time.NewTimer(time.Minute * 2)
	defer timer.Stop()

	timeoutCh := make(chan struct{})
	doneCh := make(chan struct{})
	go func() {
		select {
		case <-timer.C:
			close(timeoutCh)
		case <-doneCh:
		}
	}()
	defer close(doneCh)

	states := make([]state.State, len(dependencies))
	var firstErr error
	var errOnce sync.Once

	for i, dependency := range dependencies {
		i, dependency := i, dependency
		libutils.SafeGo(ctx, f.logger, func() {
			defer wg.Done()
			f.logger.Infof(ctx, "waiting for dependency %s", dependency)
			for {
				select {
				case <-ctx.Done():
					errOnce.Do(func() { firstErr = xerror.New("context canceled") })
					return
				case <-timeoutCh:
					errOnce.Do(func() { firstErr = xerror.New(fmt.Sprintf("dependency node %s timeout", dependency)) })
					return
				default:
				}

				dep := copiedNodes[dependency]
				dep.mu.Lock()
				if len(dep.completion) > 0 {
					firstState := dep.completion[0]
					dep.completion = dep.completion[1:]
					dep.mu.Unlock()
					states[i] = firstState
					return
				}
				dep.mu.Unlock()

				time.Sleep(time.Second * 2)
			}
		})
	}

	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	return states, nil
}

func (f *Flow) Draw(ctx context.Context) {
	panic("not implemented")
}
