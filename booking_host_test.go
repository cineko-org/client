package main

import (
	"testing"
	"time"

	"github.com/cineko-org/client/internal/adapters/browserfactory"
)

func TestBookingHostResetPreservesPaymentWinner(t *testing.T) {
	// No browser is opened: this tests host ownership independently of CGV.
	parent := &browserfactory.WarmAutomation{}
	host := newBookingAutomationHost(nil, nil)
	winner := &bookingTabAutomation{host: host, parent: parent, retained: true}
	host.parent, host.winner = parent, winner
	host.tabs[winner] = struct{}{}
	host.Reset()
	if host.parent != parent || host.winner != winner || winner.closed {
		t.Fatal("monitoring reset destroyed the payment owner")
	}
	if host.CanAccept() {
		t.Fatal("a payment winner must block new booking attempts")
	}
	host.Close()
	if !winner.closed || host.parent != nil || host.winner != nil {
		t.Fatal("explicit app shutdown did not release the payment owner")
	}
}

func TestBookingHostResetWaitsForSeatAttemptBeforeDecidingOwnership(t *testing.T) {
	host := newBookingAutomationHost(nil, nil)
	parent := &browserfactory.WarmAutomation{}
	host.parent = parent
	host.attemptMu.Lock()
	finished := make(chan struct{})
	go func() { host.Reset(); close(finished) }()
	select {
	case <-finished:
		host.attemptMu.Unlock()
		t.Fatal("reset ran while seat selection still owned the host")
	case <-time.After(20 * time.Millisecond):
	}
	winner := &bookingTabAutomation{host: host, parent: parent}
	host.mu.Lock()
	host.winner = winner
	host.tabs[winner] = struct{}{}
	host.mu.Unlock()
	host.attemptMu.Unlock()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("reset did not finish after seat attempt")
	}
	if host.winner != winner || winner.closed {
		t.Fatal("reset discarded the successful seat attempt before payment retention")
	}
	host.Close()
}
