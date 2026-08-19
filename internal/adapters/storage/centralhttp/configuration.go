package centralhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/cineko-org/client/internal/domain"
)

type configurationResource struct {
	Kind string          `json:"kind"`
	ID   string          `json:"id"`
	Data json.RawMessage `json:"data"`
}

type configurationSnapshot struct {
	Revision  int64                   `json:"revision"`
	Resources []configurationResource `json:"resources"`
}

func (store *Store) SnapshotConfiguration(ctx context.Context, userID string) (domain.Configuration, error) {
	if err := store.owns(userID); err != nil {
		return domain.Configuration{}, err
	}
	var snapshot configurationSnapshot
	if err := store.request(ctx, http.MethodGet, "/v1/configuration", true, nil, nil, &snapshot); err != nil {
		return domain.Configuration{}, err
	}
	configuration := domain.Configuration{Revision: snapshot.Revision}
	for _, resource := range snapshot.Resources {
		if err := store.decodeConfigurationResource(ctx, &configuration, resource); err != nil {
			return domain.Configuration{}, err
		}
	}
	return configuration, nil
}

func (store *Store) ReplaceConfiguration(ctx context.Context, value domain.Configuration) error {
	if err := store.validateConfigurationOwnership(value); err != nil {
		return err
	}
	resources, err := store.encodeConfigurationResources(ctx, value)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(resources)
	if err != nil {
		return fmt.Errorf("encode Central configuration: %w", err)
	}
	headers := map[string]string{
		"Content-Type":    "application/json",
		"If-Match":        strconv.FormatInt(value.Revision, 10),
		"Idempotency-Key": commandID("replace", "configuration", store.userID, value.Revision, payload),
	}
	var response struct {
		Revision int64 `json:"revision"`
	}
	return store.request(ctx, http.MethodPut, "/v1/configuration", true, headers,
		map[string]any{"resources": resources}, &response)
}

func (store *Store) validateConfigurationOwnership(value domain.Configuration) error {
	values := make([]any, 0, len(value.Presets)+len(value.Monitors))
	for _, items := range [][]any{
		asAny(value.Presets), asAny(value.Monitors),
	} {
		values = append(values, items...)
	}
	for _, item := range values {
		payload, err := json.Marshal(item)
		if err != nil {
			return fmt.Errorf("encode configuration ownership: %w", err)
		}
		if err := store.validateEmbeddedOwnership(payload); err != nil {
			return err
		}
	}
	return nil
}

func asAny[T any](values []T) []any {
	result := make([]any, len(values))
	for index := range values {
		result[index] = values[index]
	}
	return result
}

func (store *Store) decodeConfigurationResource(
	_ context.Context,
	configuration *domain.Configuration,
	resource configurationResource,
) error {
	decode := func(output any) error {
		if err := store.validateEmbeddedOwnership(resource.Data); err != nil {
			return err
		}
		return json.Unmarshal(resource.Data, output)
	}
	switch resource.Kind {
	case "presets":
		var value domain.Preset
		if err := decode(&value); err != nil {
			return err
		}
		configuration.Presets = append(configuration.Presets, value)
	case "monitors":
		var value domain.MonitorJob
		if err := decode(&value); err != nil {
			return err
		}
		configuration.Monitors = append(configuration.Monitors, value)
	default:
		return fmt.Errorf("unknown Central configuration resource %q", resource.Kind)
	}
	return nil
}

func (store *Store) encodeConfigurationResources(_ context.Context, value domain.Configuration) ([]configurationResource, error) {
	resources := make([]configurationResource, 0, len(value.Presets)+len(value.Monitors))
	appendValue := func(kind, id string, input any) error {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		resources = append(resources, configurationResource{Kind: kind, ID: id, Data: data})
		return nil
	}
	for _, item := range value.Presets {
		if err := appendValue("presets", item.ID, item); err != nil {
			return nil, err
		}
	}
	for _, item := range value.Monitors {
		if err := appendValue("monitors", item.ID, item); err != nil {
			return nil, err
		}
	}
	return resources, nil
}
