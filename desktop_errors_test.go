package main

import (
	"errors"
	"strings"
	"testing"
)

func TestDesktopErrorsDoNotExposeInternalDetails(t *testing.T) {
	for _, err := range []error{
		errors.New("dial tcp 10.0.0.1:443: connect: connection refused"),
		errors.New("POST https://internal.invalid/v1/monitors failed"),
		errors.New("invalid proxy URL socks5://user:password@internal.invalid"),
	} {
		message := userFacingDesktopError(err)
		if strings.Contains(message, "10.0.0.1") || strings.Contains(message, "internal.invalid") ||
			strings.Contains(message, "/v1/") || strings.Contains(message, "password") {
			t.Fatalf("internal error leaked: %q", message)
		}
	}
}
