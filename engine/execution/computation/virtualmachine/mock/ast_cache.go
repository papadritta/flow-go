// Code generated by mockery v1.0.0. DO NOT EDIT.

package mock

import (
	ast "github.com/onflow/cadence/runtime/ast"
	mock "github.com/stretchr/testify/mock"
)

// ASTCache is an autogenerated mock type for the ASTCache type
type ASTCache struct {
	mock.Mock
}

// GetProgram provides a mock function with given fields: _a0
func (_m *ASTCache) GetProgram(_a0 ast.Location) (*ast.Program, error) {
	ret := _m.Called(_a0)

	var r0 *ast.Program
	if rf, ok := ret.Get(0).(func(ast.Location) *ast.Program); ok {
		r0 = rf(_a0)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*ast.Program)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(ast.Location) error); ok {
		r1 = rf(_a0)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// SetProgram provides a mock function with given fields: _a0, _a1
func (_m *ASTCache) SetProgram(_a0 ast.Location, _a1 *ast.Program) error {
	ret := _m.Called(_a0, _a1)

	var r0 error
	if rf, ok := ret.Get(0).(func(ast.Location, *ast.Program) error); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}
