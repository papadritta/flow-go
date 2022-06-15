// Code generated by mockery v2.13.0. DO NOT EDIT.

package mocknetwork

import (
	p2p "github.com/onflow/flow-go/network/p2p"
	mock "github.com/stretchr/testify/mock"
)

// ConnectorOption is an autogenerated mock type for the ConnectorOption type
type ConnectorOption struct {
	mock.Mock
}

// Execute provides a mock function with given fields: connector
func (_m *ConnectorOption) Execute(connector *p2p.Libp2pConnector) {
	_m.Called(connector)
}

type NewConnectorOptionT interface {
	mock.TestingT
	Cleanup(func())
}

// NewConnectorOption creates a new instance of ConnectorOption. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
func NewConnectorOption(t NewConnectorOptionT) *ConnectorOption {
	mock := &ConnectorOption{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
