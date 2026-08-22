package centralhttp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"buf.build/go/protovalidate"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	servicepb "github.com/cineko-org/contracts/v3/gen/go/cineko/service"
	"google.golang.org/protobuf/encoding/protojson"
)

const seatMapEventType = "cineko.seat-map"

// WatchSeatMap streams Central-owned collection state and cached layouts for
// one auditorium. The callback is invoked once immediately and only after
// durable state changes thereafter.
func (store *Store) WatchSeatMap(
	ctx context.Context,
	auditoriumID string,
	consume func(*seatmappb.Resolution) error,
) error {
	input := servicepb.WatchSeatMapRequest_builder{AuditoriumId: &auditoriumID}.Build()
	if err := protovalidate.Validate(input); err != nil {
		return fmt.Errorf("validate Central seat-map watch request: %w", err)
	}
	if consume == nil {
		return errors.New("seat-map stream consumer is required")
	}
	token, err := store.sessionToken(ctx, false)
	if err != nil {
		return err
	}
	err = store.watchSeatMapWithToken(ctx, auditoriumID, token, consume)
	if !errors.Is(err, errCentralUnauthorized) {
		return err
	}
	token, err = store.sessionToken(ctx, true)
	if err != nil {
		return err
	}
	return store.watchSeatMapWithToken(ctx, auditoriumID, token, consume)
}

func (store *Store) watchSeatMapWithToken(
	ctx context.Context,
	auditoriumID string,
	token string,
	consume func(*seatmappb.Resolution) error,
) error {
	endpoint := store.baseURL + "/v1/catalog/auditoriums/" + url.PathEscape(auditoriumID) + "/seat-map:watch"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create Central seat-map stream request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "text/event-stream")
	streamClient := *store.client
	streamClient.Timeout = 0
	response, err := streamClient.Do(request)
	if err != nil {
		return eventStreamTransportError{err: fmt.Errorf("open Central seat-map stream: %w", err)}
	}
	defer func() { _ = response.Body.Close() }()
	if err := store.validateEventStreamResponse(response); err != nil {
		return err
	}
	return consumeSeatMapStream(response.Body, consume)
}

func consumeSeatMapStream(body io.Reader, consume func(*seatmappb.Resolution) error) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), maximumResponseBody)
	parser := newSSEParser(0)
	for scanner.Scan() {
		event, complete, err := parser.Consume(scanner.Text())
		if err != nil {
			return err
		}
		if !complete {
			continue
		}
		if event.type_ != seatMapEventType || len(event.data) == 0 {
			return errors.New("central seat-map stream returned an invalid event")
		}
		response := &servicepb.WatchSeatMapResponse{}
		if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(event.data, response); err != nil {
			return errors.New("central seat-map stream returned invalid response data")
		}
		if err := protovalidate.Validate(response); err != nil || response.GetResolution() == nil {
			return errors.New("central seat-map stream response violates the contract")
		}
		if err := consume(response.GetResolution()); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return eventStreamTransportError{err: fmt.Errorf("read Central seat-map stream: %w", err)}
	}
	return eventStreamTransportError{err: io.EOF}
}
