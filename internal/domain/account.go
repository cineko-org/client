package domain

import (
	"errors"
	"strings"
)

var ErrAccountCredentialsNotFound = errors.New("CGV account credentials not found")

// AccountCredentials are local-only secrets used to restore the user's CGV
// browser session. They must never be serialized into Central resources or
// exported configuration.
type AccountCredentials struct {
	ID       string
	Password string
}

func (credentials AccountCredentials) Validate() error {
	if strings.TrimSpace(credentials.ID) == "" || credentials.Password == "" {
		return errors.New("CGV ID and password are required")
	}
	return nil
}
