// Code generated by mockery v1.0.0. DO NOT EDIT.

package mockfetcher

import (
	flow "github.com/onflow/flow-go/model/flow"
	mock "github.com/stretchr/testify/mock"
)

// ChunkDataPackRequester is an autogenerated mock type for the ChunkDataPackRequester type
type ChunkDataPackRequester struct {
	mock.Mock
}

// Request provides a mock function with given fields: chunkID, executorID
func (_m *ChunkDataPackRequester) Request(chunkID flow.Identifier, executorID flow.Identifier) error {
	ret := _m.Called(chunkID, executorID)

	var r0 error
	if rf, ok := ret.Get(0).(func(flow.Identifier, flow.Identifier) error); ok {
		r0 = rf(chunkID, executorID)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}