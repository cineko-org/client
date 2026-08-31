//go:build darwin && cgo

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

#import <Cocoa/Cocoa.h>
#import <dispatch/dispatch.h>
#import <pthread.h>

struct cineko_activation_result {
	int applied;
};

static void cineko_apply_activation_policy(void *context) {
	struct cineko_activation_result *result = context;
	NSInteger policy = result->applied == 2 ? NSApplicationActivationPolicyRegular : NSApplicationActivationPolicyAccessory;
	[NSApp setActivationPolicy:policy];
	if (policy == NSApplicationActivationPolicyRegular) {
		[NSApp activateIgnoringOtherApps:YES];
	}
	result->applied = [NSApp activationPolicy] == policy ? 1 : 0;
}

static int cineko_configure_activation_policy(int foreground) {
	struct cineko_activation_result result = {foreground ? 2 : 0};
	if (pthread_main_np() != 0) {
		cineko_apply_activation_policy(&result);
	} else {
		dispatch_sync_f(dispatch_get_main_queue(), &result, cineko_apply_activation_policy);
	}
	return result.applied;
}
*/
import "C"

func configureDesktopActivationPolicy(foreground bool) bool {
	value := C.int(0)
	if foreground {
		value = 1
	}
	return C.cineko_configure_activation_policy(value) == 1
}
