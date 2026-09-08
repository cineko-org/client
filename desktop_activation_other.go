//go:build !darwin || !cgo

package main

func configureDesktopActivationPolicy(bool) bool { return true }

func removeDesktopActivationHandler() {}
