//go:build darwin || linux

package cmd

import (
	"context"
	"reflect"

	"github.com/anydoor7/tslink/internal/testenv"
)

// Preserve the guard's function-pointer restoration check for context seams.
func contextManagerSeam(name string, get func() func(context.Context, ...string) ([]byte, error), set func(func(context.Context, ...string) ([]byte, error))) testenv.ServiceManagerSeam {
	var installed uintptr
	var guarded testenv.ServiceManagerCall
	return testenv.ServiceManagerSeam{
		Manager: name,
		Get: func() testenv.ServiceManagerCall {
			current := get()
			if guarded != nil && reflect.ValueOf(current).Pointer() == installed {
				return guarded
			}
			return func(args ...string) ([]byte, error) { return current(context.Background(), args...) }
		},
		Set: func(call testenv.ServiceManagerCall) {
			wrapper := func(_ context.Context, args ...string) ([]byte, error) { return call(args...) }
			installed, guarded = reflect.ValueOf(wrapper).Pointer(), call
			set(wrapper)
		},
	}
}
