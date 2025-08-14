package edge

import flowcontract "github.com/futurxlab/golanggraph/contract"

type Edge struct {
	From          string
	To            string
	ConditionalTo []string
	ConditionFunc flowcontract.ConditionEdgeFunc
}
