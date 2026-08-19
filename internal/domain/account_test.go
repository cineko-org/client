package domain

import "testing"

func TestAccountCredentialsValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		credentials AccountCredentials
		wantError   bool
	}{
		{name: "valid", credentials: AccountCredentials{ID: "member", Password: "secret"}},
		{name: "blank ID", credentials: AccountCredentials{ID: "  ", Password: "secret"}, wantError: true},
		{name: "missing password", credentials: AccountCredentials{ID: "member"}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if gotError := test.credentials.Validate() != nil; gotError != test.wantError {
				t.Fatalf("Validate() error = %v, wantError %v", gotError, test.wantError)
			}
		})
	}
}
