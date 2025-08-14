package edge

import flowcontract "golanggraph/contract"

type Edge struct {
	From          string
	To            string
	ConditionalTo []string
	ConditionFunc flowcontract.ConditionEdgeFunc
}
