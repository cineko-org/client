package main

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestMacOSClientBundleIsLauncherOwnedUIElement(t *testing.T) {
	contents, err := os.ReadFile("build/darwin/Info.plist")
	if err != nil {
		t.Fatal(err)
	}
	plist := string(contents)
	if !strings.Contains(plist, "<key>LSUIElement</key>\n        <true/>") {
		t.Fatal("macOS Client bundle is declared as an independent Dock application")
	}
}

func TestMacOSWindowCloseKeepsLauncherOwnedClientRunning(t *testing.T) {
	options := desktopWindowOptions(nil, nil, nil, nil, "", "", nil)
	if options.HideWindowOnClose != (runtime.GOOS == "darwin") {
		t.Fatal("window close does not match platform status-menu availability")
	}
	if options.SingleInstanceLock.OnSecondInstanceLaunch == nil {
		t.Fatal("second-instance activation is not connected")
	}
	if options.OnShutdown == nil {
		t.Fatal("activation observers have no cleanup")
	}
}
