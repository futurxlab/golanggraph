package flow

import (
	"fmt"
	"slices"

	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/futurxlab/golanggraph/edge"
	"github.com/futurxlab/golanggraph/logger"
	"github.com/futurxlab/golanggraph/state"
)

type FlowBuilder struct {
	name         string
	edges        []edge.Edge
	nodes        []flowcontract.Node
	dependencies map[string][]string
	checkpointer flowcontract.Checkpointer
	logger       logger.ILogger
}

func (b *FlowBuilder) AddEdge(edge edge.Edge) *FlowBuilder {
	b.edges = append(b.edges, edge)
	return b
}

func (b *FlowBuilder) AddNode(node flowcontract.Node, dependencies ...string) *FlowBuilder {
	b.nodes = append(b.nodes, node)
	b.dependencies[node.Name()] = dependencies
	return b
}

func (b *FlowBuilder) SetName(name string) *FlowBuilder {
	b.name = name
	return b
}

func (b *FlowBuilder) SetCheckpointer(checkpointer flowcontract.Checkpointer) *FlowBuilder {
	b.checkpointer = checkpointer
	return b
}

func (b *FlowBuilder) Compile() (*Flow, error) {

	if b.name == "" {
		return nil, fmt.Errorf("flow name cannot be empty")
	}

	// 创建图的邻接表表示
	graph := make(map[string][]edge.Edge)
	nodes := make(map[string]*nodeEntry)

	nodes[StartNode] = &nodeEntry{}
	nodes[EndNode] = &nodeEntry{}

	// 检查是否设置了 checkpointer
	if b.checkpointer == nil {
		return nil, fmt.Errorf("checkpointer is required")
	}

	// 将所有节点添加到 nodes map 中
	for _, node := range b.nodes {
		if node.Name() == "" {
			return nil, fmt.Errorf("node name cannot be empty")
		}
		if _, exists := nodes[node.Name()]; exists {
			return nil, fmt.Errorf("duplicate node name: %s", node.Name())
		}
		nodes[node.Name()] = &nodeEntry{
			node:         node,
			dependencies: b.dependencies[node.Name()],
			completion:   make([]state.State, 0),
		}
	}

	// 构建图的边
	for _, e := range b.edges {
		if e.From == "" {
			return nil, fmt.Errorf("edge from node cannot be empty")
		}
		// 检查边的起始节点是否存在
		if e.From != StartNode {
			if _, exists := nodes[e.From]; !exists {
				return nil, fmt.Errorf("edge from node %s does not exist", e.From)
			}
		}
		// 如果边有明确的目标节点，检查目标节点是否存在
		if e.To != EndNode {
			if e.To != "" {
				if _, exists := nodes[e.To]; !exists {
					return nil, fmt.Errorf("edge to node %s does not exist", e.To)
				}
			}
		}
		graph[e.From] = append(graph[e.From], e)
	}

	// 除了end节点和start节点外，其他节点必须要有上游节点和下游节点
	for _, node := range nodes {
		if node.node == nil {
			continue
		}
		// node has not to edges
		if len(graph[node.node.Name()]) == 0 {
			return nil, fmt.Errorf("node %s has no to edges", node.node.Name())
		}

		// node has not from edges
		noFromEdges := true
		for _, edge := range b.edges {
			if edge.To == node.node.Name() || slices.Contains(edge.ConditionalTo, node.node.Name()) {
				noFromEdges = false
				break
			}
		}

		if noFromEdges {
			return nil, fmt.Errorf("node %s has no from edges", node.node.Name())
		}
	}

	// 返回构建好的 Flow
	return &Flow{
		name:         b.name,
		checkpointer: b.checkpointer,
		logger:       b.logger,
		graph:        graph,
		nodes:        nodes,
	}, nil
}

func NewFlowBuilder(logger logger.ILogger) *FlowBuilder {
	return &FlowBuilder{
		logger:       logger,
		dependencies: make(map[string][]string),
	}
}
