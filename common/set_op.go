package common

import (
	"fmt"
	"strings"
)

const (
	Union = iota
	Intersection
	Difference
	Xor
)

const (
	Append = iota
	ConCat
	IntegerSum
	IntegerSub
	IntegerDiv
	IntegerMul
	FloatSum
	FloatSub
	FloatDiv
	FLoatMul
	BigIntAdd
	BigIntAndNot
	BigIntDiv
	BigIntMod
	BigIntMul
	BigIntOr
	BigIntRem
	BigIntSub
	BigIntXor
)

type SetOpMerge int

func (self SetOpMerge) String() string {
	switch self {
	case Append:
		return "Append"
	case ConCat:
		return "ConCat"
	case IntegerSum:
		return "IntegerSum"
	case IntegerSub:
		return "IntegerSub"
	case IntegerDiv:
		return "IntegerDiv"
	case IntegerMul:
		return "IntegerMul"
	case FloatSum:
		return "FloatSum"
	case FloatSub:
		return "FloatSub"
	case FloatDiv:
		return "FloatDiv"
	case FLoatMul:
		return "FloatMul"
	case BigIntAdd:
		return "BigIntAdd"
	case BigIntAndNot:
		return "BigIntAndNot"
	case BigIntDiv:
		return "BigIntDiv"
	case BigIntMod:
		return "BigIntMod"
	case BigIntMul:
		return "BigIntMul"
	case BigIntOr:
		return "BigIntOr"
	case BigIntRem:
		return "BigIntRem"
	case BigIntSub:
		return "BigIntSub"
	case BigIntXor:
		return "BigIntXor"
	}
	panic(fmt.Errorf("Unknown SetOpType %v", int(self)))
}

type SetOpType int

func (self SetOpType) String() string {
	switch self {
	case Union:
		return "U"
	case Intersection:
		return "I"
	case Difference:
		return "D"
	case Xor:
		return "X"
	}
	panic(fmt.Errorf("Unknown SetOpType %v", int(self)))
}

type SetOpSource struct {
	Key   []byte
	SetOp *SetOp
}

type SetOp struct {
	Sources []SetOpSource
	Type    SetOpType
	Merge   SetOpMerge
}

func (self SetOp) String() string {
	sources := make([]string, len(self.Sources))
	for index, source := range self.Sources {
		if source.Key != nil {
			sources[index] = string(source.Key)
		} else {
			sources[index] = fmt.Sprint(source.SetOp)
		}
	}
	return fmt.Sprintf("(%v %v)", self.Type, strings.Join(sources, " "))
}

type SetExpression struct {
	Op     SetOp
	Min    []byte
	Max    []byte
	MinInc bool
	MaxInc bool
	Len    int
	Dest   []byte
}

type SetOpResult struct {
	Key    []byte
	Values [][]byte
}

func (self *SetOpResult) ShallowCopy() (result *SetOpResult) {
	result = &SetOpResult{
		Key:    self.Key,
		Values: make([][]byte, len(self.Values)),
	}
	copy(result.Values, self.Values)
	return
}
