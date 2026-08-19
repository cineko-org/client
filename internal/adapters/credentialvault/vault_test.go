package credentialvault

import (
	"errors"
	"testing"

	"github.com/cineko-org/client/internal/domain"
	"github.com/zalando/go-keyring"
)

type memoryBackend struct {
	values map[string]string
	err    error
}

func (backend *memoryBackend) Get(service, user string) (string, error) {
	if backend.err != nil {
		return "", backend.err
	}
	value, ok := backend.values[service+"\x00"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}

func (backend *memoryBackend) Set(service, user, password string) error {
	if backend.err != nil {
		return backend.err
	}
	backend.values[service+"\x00"+user] = password
	return nil
}

func (backend *memoryBackend) Delete(service, user string) error {
	if backend.err != nil {
		return backend.err
	}
	key := service + "\x00" + user
	if _, ok := backend.values[key]; !ok {
		return keyring.ErrNotFound
	}
	delete(backend.values, key)
	return nil
}

func TestVaultStoresCredentialsPerHashedCinekoUser(t *testing.T) {
	t.Parallel()
	backend := &memoryBackend{values: make(map[string]string)}
	vault := &Vault{backend: backend}
	want := domain.AccountCredentials{ID: "member", Password: "secret"}
	if err := vault.Save(t.Context(), "user-a", want); err != nil {
		t.Fatal(err)
	}
	if _, exposed := backend.values[serviceName+"\x00user-a"]; exposed {
		t.Fatal("operating system vault key exposes the Central user ID")
	}
	got, err := vault.Load(t.Context(), "user-a")
	if err != nil || got != want {
		t.Fatalf("Load() = %+v, %v", got, err)
	}
	if _, err := vault.Load(t.Context(), "user-b"); !errors.Is(err, domain.ErrAccountCredentialsNotFound) {
		t.Fatalf("other user's Load() error = %v", err)
	}
	if err := vault.Delete(t.Context(), "user-a"); err != nil {
		t.Fatal(err)
	}
	if err := vault.Delete(t.Context(), "user-a"); err != nil {
		t.Fatalf("idempotent Delete() error = %v", err)
	}
}

func TestVaultRejectsIncompleteCredentialsAndRedactsBackendFailures(t *testing.T) {
	t.Parallel()
	vault := &Vault{backend: &memoryBackend{values: make(map[string]string)}}
	if err := vault.Save(t.Context(), "user", domain.AccountCredentials{ID: "member"}); err == nil {
		t.Fatal("Save() accepted an empty password")
	}

	backendErr := errors.New("vault unavailable")
	vault.backend = &memoryBackend{values: make(map[string]string), err: backendErr}
	if _, err := vault.Load(t.Context(), "user"); !errors.Is(err, backendErr) {
		t.Fatalf("Load() error = %v", err)
	}
}
