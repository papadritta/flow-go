// Code generated by mockery v1.0.0. DO NOT EDIT.

package mock

import (
	irrecoverable "github.com/onflow/flow-go/module/irrecoverable"
	mock "github.com/stretchr/testify/mock"
)

// Component is an autogenerated mock type for the Component type
type Component struct {
	mock.Mock
}

// Done provides a mock function with given fields:
func (_m *Component) Done() <-chan struct{} {
	ret := _m.Called()

	var r0 <-chan struct{}
	if rf, ok := ret.Get(0).(func() <-chan struct{}); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(<-chan struct{})
		}
	}

	return r0
}

// Ready provides a mock function with given fields:
func (_m *Component) Ready() <-chan struct{} {
	ret := _m.Called()

	var r0 <-chan struct{}
	if rf, ok := ret.Get(0).(func() <-chan struct{}); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(<-chan struct{})
		}
	}

	return r0
}

// Start provides a mock function with given fields: _a0
func (_m *Component) Start(_a0 irrecoverable.SignalerContext) error {
	ret := _m.Called(_a0)

	var r0 error
	if rf, ok := ret.Get(0).(func(irrecoverable.SignalerContext) error); ok {
		r0 = rf(_a0)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}
