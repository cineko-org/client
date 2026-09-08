#import <Cocoa/Cocoa.h>
#import "../desktop_activation_darwin.m"

static void settle(void) {
    NSDate *until = [NSDate dateWithTimeIntervalSinceNow:0.5];
    while ([until timeIntervalSinceNow] > 0) {
        NSEvent *event = [NSApp nextEventMatchingMask:NSEventMaskAny untilDate:until
            inMode:NSDefaultRunLoopMode dequeue:YES];
        if (event != nil) [NSApp sendEvent:event];
    }
}

static void activate(void) {
    [NSApp activateIgnoringOtherApps:YES];
    [[NSNotificationCenter defaultCenter] postNotificationName:NSApplicationDidBecomeActiveNotification object:NSApp];
    settle();
}

int main(void) {
    @autoreleasepool {
        [NSApplication sharedApplication];
        [NSApp finishLaunching];
        NSWindow *window = [[NSWindow alloc] initWithContentRect:NSMakeRect(200, 200, 500, 300)
            styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable
            backing:NSBackingStoreBuffered defer:NO];
        window.title = @"Cineko activation fixture";
        [window makeKeyAndOrderFront:nil];
        NSCAssert(cineko_configure_activation_policy(0), @"accessory policy failed");
        cineko_configure_activation_policy(0); // Reconfiguration must not duplicate observers.
        activate();
        NSCAssert(NSApp.activationPolicy == NSApplicationActivationPolicyAccessory, @"Client gained a second Dock icon");
        [window miniaturize:nil];
        settle();
        NSCAssert(window.miniaturized, @"fixture did not minimize");
        activate();
        NSCAssert(!window.miniaturized && window.visible, @"activation did not restore minimized window: mini=%d visible=%d windows=%@", window.miniaturized, window.visible, NSApp.windows);
        [window orderOut:nil];
        activate();
        NSCAssert(window.visible, @"activation did not reveal hidden window");
        for (int i = 0; i < 3; i++) activate();
        NSCAssert(window.visible && !window.miniaturized, @"repeated activation lost window");
        cineko_remove_window_activation_observer();
        cineko_remove_window_activation_observer();
        [window orderOut:nil];
        [[NSNotificationCenter defaultCenter] postNotificationName:NSApplicationDidBecomeActiveNotification object:NSApp];
        NSCAssert(!window.visible, @"observer survived shutdown");
        puts("PASS: accessory policy, minimize restore, hidden restore, repeated activation, shutdown");
    }
    return 0;
}
