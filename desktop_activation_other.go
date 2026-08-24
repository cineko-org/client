//go:build !darwin || !cgo

package main

func useLauncherOwnedActivationPolicy() bool { return true }
