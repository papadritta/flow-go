// Code generated by mockery v1.0.0. DO NOT EDIT.

package mocks

import mock "github.com/stretchr/testify/mock"

// Persister is an autogenerated mock type for the Persister type
type Persister struct {
	mock.Mock
}

// CurrentView provides a mock function with given fields: view
func (_m *Persister) CurrentView(view uint64) error {
	ret := _m.Called(view)

	var r0 error
	if rf, ok := ret.Get(0).(func(uint64) error); ok {
		r0 = rf(view)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}