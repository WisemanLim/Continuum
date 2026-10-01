//go:build !windows

package main

import "context"

func underSCM(func(ctx context.Context) error) (bool, error) { return false, nil }
