package main

import (
	"os"
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
