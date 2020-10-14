// Code generated by mockery v1.0.0. DO NOT EDIT.

package mempool

import (
	flow "github.com/onflow/flow-go/model/flow"

	mock "github.com/stretchr/testify/mock"
)

// Approvals is an autogenerated mock type for the Approvals type
type Approvals struct {
	mock.Mock
}

// Add provides a mock function with given fields: approval
func (_m *Approvals) Add(approval *flow.ResultApproval) (bool, error) {
	ret := _m.Called(approval)

	var r0 bool
	if rf, ok := ret.Get(0).(func(*flow.ResultApproval) bool); ok {
		r0 = rf(approval)
	} else {
		r0 = ret.Get(0).(bool)
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(*flow.ResultApproval) error); ok {
		r1 = rf(approval)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// All provides a mock function with given fields:
func (_m *Approvals) All() []*flow.ResultApproval {
	ret := _m.Called()

	var r0 []*flow.ResultApproval
	if rf, ok := ret.Get(0).(func() []*flow.ResultApproval); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]*flow.ResultApproval)
		}
	}

	return r0
}

// ByChunk provides a mock function with given fields: resultID, chunkIndex
func (_m *Approvals) ByChunk(resultID flow.Identifier, chunkIndex uint64) map[flow.Identifier]*flow.ResultApproval {
	ret := _m.Called(resultID, chunkIndex)

	var r0 map[flow.Identifier]*flow.ResultApproval
	if rf, ok := ret.Get(0).(func(flow.Identifier, uint64) map[flow.Identifier]*flow.ResultApproval); ok {
		r0 = rf(resultID, chunkIndex)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(map[flow.Identifier]*flow.ResultApproval)
		}
	}

	return r0
}

// RemApproval provides a mock function with given fields: approval
func (_m *Approvals) RemApproval(approval *flow.ResultApproval) (bool, error) {
	ret := _m.Called(approval)

	var r0 bool
	if rf, ok := ret.Get(0).(func(*flow.ResultApproval) bool); ok {
		r0 = rf(approval)
	} else {
		r0 = ret.Get(0).(bool)
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(*flow.ResultApproval) error); ok {
		r1 = rf(approval)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// RemChunk provides a mock function with given fields: resultID, chunkIndex
func (_m *Approvals) RemChunk(resultID flow.Identifier, chunkIndex uint64) bool {
	ret := _m.Called(resultID, chunkIndex)

	var r0 bool
	if rf, ok := ret.Get(0).(func(flow.Identifier, uint64) bool); ok {
		r0 = rf(resultID, chunkIndex)
	} else {
		r0 = ret.Get(0).(bool)
	}

	return r0
}

// Size provides a mock function with given fields:
func (_m *Approvals) Size() uint {
	ret := _m.Called()

	var r0 uint
	if rf, ok := ret.Get(0).(func() uint); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(uint)
	}

	return r0
}
