//go:build !linux

package main

import (
	"context"
	"errors"
)

func kernelManagedRoutes(context.Context) ([]managedRoute, error) {
	return nil, errors.New("split routing requires Linux")
}

func mutateManagedRoutes(context.Context, []managedRoute, []managedRoute) error {
	return errors.New("split routing requires Linux")
}
