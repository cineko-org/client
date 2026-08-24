package application

import (
	"context"
	"errors"
	"fmt"

	"buf.build/go/protovalidate"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
)

// SubmitLiveSeatObservation persists a read made by the local authenticated
// booking browser. There is no remote command lease in the Client-only model.
func SubmitLiveSeatObservation(
	ctx context.Context,
	repository LiveSeatObservationRepository,
	observation *seatmappb.LiveSeatObservation,
) error {
	if repository == nil {
		return errors.New("live seat observation repository is required")
	}
	if err := protovalidate.Validate(observation); err != nil {
		return fmt.Errorf("validate live seat observation: %w", err)
	}
	snapshot, err := repository.SubmitLiveSeatObservation(ctx, observation)
	if err != nil {
		return err
	}
	if err := protovalidate.Validate(snapshot); err != nil {
		return fmt.Errorf("validate persisted live seat snapshot: %w", err)
	}
	availability := observation.GetAvailability()
	if snapshot == nil || availability == nil ||
		snapshot.GetAuditoriumId() != availability.GetAuditoriumId() ||
		snapshot.GetLayoutHash() != availability.GetLayoutHash() {
		return errors.New("local store returned a mismatched live seat snapshot")
	}
	return nil
}
