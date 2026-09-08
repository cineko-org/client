//go:build darwin && cgo

#import <Cocoa/Cocoa.h>
#import <pthread.h>

static id windowActivationObserver;

static void onMainThread(dispatch_block_t action) {
    if (pthread_main_np()) action();
    else dispatch_sync(dispatch_get_main_queue(), action);
}

static void restoreClientWindow(void) {
    NSWindow *window = NSApp.mainWindow;
    if (window == nil) {
        for (NSWindow *candidate in NSApp.windows) {
            if (candidate.canBecomeMainWindow && ![candidate isKindOfClass:[NSPanel class]]) {
                window = candidate;
                break;
            }
        }
    }
    if (window == nil) return;
    if (window.miniaturized) [window deminiaturize:nil];
    // Keep a file picker or other modal interaction in front of its owner.
    NSWindow *front = NSApp.modalWindow ?: window.attachedSheet ?: window;
    [window orderFront:nil];
    [front makeKeyAndOrderFront:nil];
}

int cineko_configure_activation_policy(int foreground) {
    __block BOOL applied = NO;
    onMainThread(^{
        NSApplicationActivationPolicy policy = foreground
            ? NSApplicationActivationPolicyRegular : NSApplicationActivationPolicyAccessory;
        [NSApp setActivationPolicy:policy];
        if (windowActivationObserver == nil) {
            windowActivationObserver = [[[NSNotificationCenter defaultCenter]
                addObserverForName:NSApplicationDidBecomeActiveNotification object:NSApp queue:nil
                usingBlock:^(NSNotification *note) { restoreClientWindow(); }] retain];
        }
        if (foreground) [NSApp activateIgnoringOtherApps:YES];
        applied = NSApp.activationPolicy == policy;
    });
    return applied ? 1 : 0;
}

void cineko_remove_window_activation_observer(void) {
    onMainThread(^{
        if (windowActivationObserver == nil) return;
        [[NSNotificationCenter defaultCenter] removeObserver:windowActivationObserver];
        [windowActivationObserver release];
        windowActivationObserver = nil;
    });
}
