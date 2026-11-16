//go:build !linux

package main

import (
	"context"
	"errors"
)

func kernelDirectRoutes(context.Context) ([]directRoute, error) {
	return nil, errors.New("split routing requires Linux")
}
func mutateDirectRoutes(context.Context, []directRoute, []directRoute) error {
	return errors.New("split routing requires Linux")
}
