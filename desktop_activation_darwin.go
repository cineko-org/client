//go:build darwin && cgo

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

int cineko_configure_activation_policy(int foreground);
void cineko_remove_window_activation_observer(void);
*/
import "C"

func configureDesktopActivationPolicy(foreground bool) bool {
	value := C.int(0)
	if foreground {
		value = 1
	}
	return C.cineko_configure_activation_policy(value) == 1
}

func removeDesktopActivationHandler() { C.cineko_remove_window_activation_observer() }
