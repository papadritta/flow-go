// Code generated by mockery v1.0.0. DO NOT EDIT.

package mocknetwork

import (
	context "context"

	flow "github.com/onflow/flow-go/model/flow"
	mock "github.com/stretchr/testify/mock"
)

// Connector is an autogenerated mock type for the Connector type
type Connector struct {
	mock.Mock
}

// UpdatePeers provides a mock function with given fields: ctx, ids
func (_m *Connector) UpdatePeers(ctx context.Context, ids flow.IdentityList) error {
	ret := _m.Called(ctx, ids)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, flow.IdentityList) error); ok {
		r0 = rf(ctx, ids)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}