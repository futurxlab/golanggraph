package flow

import (
	"context"
	"fmt"
	"testing"

	"github.com/futurxlab/golanggraph/checkpointer"
	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/futurxlab/golanggraph/edge"

	"github.com/futurxlab/golanggraph/logger"
	"github.com/futurxlab/golanggraph/state"
)

type sample1Node struct{}

func (n *sample1Node) Name() string {
	return "sample1"
}

func (n *sample1Node) Run(ctx context.Context, state *state.State, streamFunc flowcontract.StreamFunc) error {

	if state.Metadata == nil {
		state.Metadata = make(map[string]interface{})
	}

	state.Metadata["sample1"] = "sample1"
	return nil
}

type sample2Node struct{}

func (n *sample2Node) Name() string {
	return "sample2"
}

func (n *sample2Node) Run(ctx context.Context, state *state.State, streamFunc flowcontract.StreamFunc) error {
	if state.Metadata == nil {
		state.Metadata = make(map[string]interface{})
	}

	state.Metadata["sample2"] = "sample2"
	return nil
}

type sample3Node struct{}

func (n *sample3Node) Name() string {
	return "sample3"
}

func (n *sample3Node) Run(ctx context.Context, state *state.State, streamFunc flowcontract.StreamFunc) error {

	fmt.Println(state.Metadata)

	state.Metadata["sample3"] = "sample3"
	return nil
}

func TestFlow(t *testing.T) {

	t.Run("test parallel flow", func(t *testing.T) {
		sample1 := &sample1Node{}
		sample2 := &sample2Node{}
		sample3 := &sample3Node{}

		logger, err := logger.NewLogger()
		if err != nil {
			t.Fatal(err)
		}

		checkpointer := checkpointer.NewInMemoryCheckpointer()

		flow, err := NewFlowBuilder(logger).
			SetCheckpointer(checkpointer).
			AddNode(sample1).
			AddNode(sample2).
			AddNode(sample3, sample1.Name(), sample2.Name()).
			AddEdge(edge.Edge{
				From: StartNode,
				To:   sample1.Name(),
			}).
			AddEdge(edge.Edge{
				From: StartNode,
				To:   sample2.Name(),
			}).
			AddEdge(edge.Edge{
				From: sample1.Name(),
				To:   sample3.Name(),
			}).
			AddEdge(edge.Edge{
				From: sample2.Name(),
				To:   sample3.Name(),
			}).
			AddEdge(edge.Edge{
				From: sample3.Name(),
				To:   EndNode,
			}).
			Compile()

		if err != nil {
			t.Fatal(err)
		}

		flow.Exec(context.Background(), state.State{}, nil)
	})
}
