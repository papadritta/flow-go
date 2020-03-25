// Code generated by mockery v1.0.0. DO NOT EDIT.

package mock

import cluster "github.com/dapperlabs/flow-go/model/cluster"
import flow "github.com/dapperlabs/flow-go/model/flow"
import mock "github.com/stretchr/testify/mock"

// PendingClusterBlockBuffer is an autogenerated mock type for the PendingClusterBlockBuffer type
type PendingClusterBlockBuffer struct {
	mock.Mock
}

// Add provides a mock function with given fields: block
func (_m *PendingClusterBlockBuffer) Add(block *cluster.PendingBlock) bool {
	ret := _m.Called(block)

	var r0 bool
	if rf, ok := ret.Get(0).(func(*cluster.PendingBlock) bool); ok {
		r0 = rf(block)
	} else {
		r0 = ret.Get(0).(bool)
	}

	return r0
}

// ByID provides a mock function with given fields: blockID
func (_m *PendingClusterBlockBuffer) ByID(blockID flow.Identifier) (*cluster.PendingBlock, bool) {
	ret := _m.Called(blockID)

	var r0 *cluster.PendingBlock
	if rf, ok := ret.Get(0).(func(flow.Identifier) *cluster.PendingBlock); ok {
		r0 = rf(blockID)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*cluster.PendingBlock)
		}
	}

	var r1 bool
	if rf, ok := ret.Get(1).(func(flow.Identifier) bool); ok {
		r1 = rf(blockID)
	} else {
		r1 = ret.Get(1).(bool)
	}

	return r0, r1
}

// ByParentID provides a mock function with given fields: parentID
func (_m *PendingClusterBlockBuffer) ByParentID(parentID flow.Identifier) ([]*cluster.PendingBlock, bool) {
	ret := _m.Called(parentID)

	var r0 []*cluster.PendingBlock
	if rf, ok := ret.Get(0).(func(flow.Identifier) []*cluster.PendingBlock); ok {
		r0 = rf(parentID)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]*cluster.PendingBlock)
		}
	}

	var r1 bool
	if rf, ok := ret.Get(1).(func(flow.Identifier) bool); ok {
		r1 = rf(parentID)
	} else {
		r1 = ret.Get(1).(bool)
	}

	return r0, r1
}

// DropForParent provides a mock function with given fields: parentID
func (_m *PendingClusterBlockBuffer) DropForParent(parentID flow.Identifier) {
	_m.Called(parentID)
}