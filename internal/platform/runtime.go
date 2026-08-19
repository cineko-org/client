package platform

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"time"
)

type Clock struct{}

func (Clock) Now() time.Time { return time.Now() }

type IDGenerator struct{}

func (IDGenerator) NewID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		panic(err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buffer)
}

type Waiter struct{}

func (Waiter) Wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
