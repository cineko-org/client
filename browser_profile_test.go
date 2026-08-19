package main

import (
	"testing"

	"github.com/cineko-org/client/internal/adapters/egress"
	"github.com/cineko-org/client/internal/interfaces/webui"
)

func TestBrowserTaskForUserOwnsOnePersistentAccountProfile(t *testing.T) {
	session := browserTaskForUser("central-user", true, webui.AutomationSession)
	if session.Purpose != egress.PurposeSession || session.SessionKey != "central-user" || !session.Headless {
		t.Fatalf("session task = %+v", session)
	}
	scan := browserTaskForUser("central-user", false, webui.AutomationScan)
	if scan.Purpose != egress.PurposeScan || scan.SessionKey != "" || scan.Headless {
		t.Fatalf("scan task = %+v", scan)
	}
}
