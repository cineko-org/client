package credentialvault

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cineko-org/client/internal/domain"
	"github.com/zalando/go-keyring"
)

const serviceName = "org.cineko.client.cgv"

type backend interface {
	Get(service, user string) (string, error)
	Set(service, user, password string) error
	Delete(service, user string) error
}

type systemBackend struct{}

func (systemBackend) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (systemBackend) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}

func (systemBackend) Delete(service, user string) error {
	return keyring.Delete(service, user)
}

type Vault struct {
	backend backend
}

type storedCredentials struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

func New() *Vault {
	return &Vault{backend: systemBackend{}}
}

func (vault *Vault) Load(ctx context.Context, userID string) (domain.AccountCredentials, error) {
	if err := ctx.Err(); err != nil {
		return domain.AccountCredentials{}, err
	}
	encoded, err := vault.backend.Get(serviceName, accountKey(userID))
	if errors.Is(err, keyring.ErrNotFound) {
		return domain.AccountCredentials{}, domain.ErrAccountCredentialsNotFound
	}
	if err != nil {
		return domain.AccountCredentials{}, fmt.Errorf("read CGV credentials from operating system vault: %w", err)
	}
	var stored storedCredentials
	if err := json.Unmarshal([]byte(encoded), &stored); err != nil {
		return domain.AccountCredentials{}, errors.New("decode CGV credentials from operating system vault")
	}
	credentials := domain.AccountCredentials{ID: stored.ID, Password: stored.Value}
	if err := credentials.Validate(); err != nil {
		return domain.AccountCredentials{}, fmt.Errorf("validate CGV credentials from operating system vault: %w", err)
	}
	return credentials, nil
}

func (vault *Vault) Save(ctx context.Context, userID string, credentials domain.AccountCredentials) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(userID) == "" {
		return errors.New("cineko user is required")
	}
	credentials.ID = strings.TrimSpace(credentials.ID)
	if err := credentials.Validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal(storedCredentials{ID: credentials.ID, Value: credentials.Password})
	if err != nil {
		return errors.New("encode CGV credentials for operating system vault")
	}
	if err := vault.backend.Set(serviceName, accountKey(userID), string(encoded)); err != nil {
		return fmt.Errorf("store CGV credentials in operating system vault: %w", err)
	}
	return nil
}

func (vault *Vault) Delete(ctx context.Context, userID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := vault.backend.Delete(serviceName, accountKey(userID))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete CGV credentials from operating system vault: %w", err)
	}
	return nil
}

func accountKey(userID string) string {
	digest := sha256.Sum256([]byte("cineko-cgv-account-v1\x00" + strings.TrimSpace(userID)))
	return hex.EncodeToString(digest[:])
}
