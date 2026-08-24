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

static void cineko_apply_accessory_activation_policy(void *context) {
	struct cineko_activation_result *result = context;
	result->applied = [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory] ? 1 : 0;
}

static int cineko_use_launcher_owned_activation_policy(void) {
	struct cineko_activation_result result = {0};
	if (pthread_main_np() != 0) {
		cineko_apply_accessory_activation_policy(&result);
	} else {
		dispatch_sync_f(dispatch_get_main_queue(), &result, cineko_apply_accessory_activation_policy);
	}
	return result.applied;
}
*/
import "C"

func useLauncherOwnedActivationPolicy() bool {
	return C.cineko_use_launcher_owned_activation_policy() == 1
}
