package application

import (
	"context"
	"fmt"

	"github.com/cineko-org/client/internal/domain"
)

type CancellationService struct {
	reservations ReservationRepository
	booking      BookingGateway
	clock        Clock
	operations   ExternalOperationRepository
}

func NewCancellationService(
	reservations ReservationRepository,
	booking BookingGateway,
	clock Clock,
	operations ...ExternalOperationRepository,
) *CancellationService {
	service := &CancellationService{reservations: reservations, booking: booking, clock: clock}
	if len(operations) > 0 {
		service.operations = operations[0]
	}
	return service
}

func (service *CancellationService) Cancel(
	ctx context.Context,
	userID, reservationID string,
	commit bool,
) (domain.CancellationDraft, error) {
	reservation, err := service.reservations.GetReservation(ctx, reservationID)
	if err != nil || reservation.UserID != userID {
		return domain.CancellationDraft{}, ErrNotFound
	}
	draft, err := service.booking.PrepareCancellation(ctx, reservation)
	if err != nil || !commit {
		return draft, err
	}
	now := service.clock.Now()
	operation := domain.ExternalOperation{
		ID:     fmt.Sprintf("cancellation:%s:%d", reservation.ID, now.UnixNano()),
		UserID: reservation.UserID, MonitorID: reservation.MonitorID, ReservationID: reservation.ID,
		Kind: domain.ExternalOperationCancellation, Status: domain.ExternalOperationPrepared,
		RefundAmount: draft.RefundAmount, CreatedAt: now, UpdatedAt: now,
	}
	reservation.Status = "cancellation_committing"
	if err := service.reservations.PutReservation(ctx, reservation); err != nil {
		return domain.CancellationDraft{}, err
	}
	if service.operations != nil {
		if err := service.operations.PutExternalOperation(ctx, operation); err != nil {
			return domain.CancellationDraft{}, err
		}
	}
	if err := service.booking.CommitCancellation(ctx); err != nil {
		operation.Status = domain.ExternalOperationUnknown
		operation.LastError = err.Error()
		operation.UpdatedAt = service.clock.Now()
		if service.operations != nil {
			_ = service.operations.PutExternalOperation(context.WithoutCancel(ctx), operation)
		}
		reservation.Status = "cancellation_unknown"
		_ = service.reservations.PutReservation(context.WithoutCancel(ctx), reservation)
		return domain.CancellationDraft{}, err
	}
	operation.Status = domain.ExternalOperationConfirmed
	operation.UpdatedAt = service.clock.Now()
	if service.operations != nil {
		if err := service.operations.PutExternalOperation(context.WithoutCancel(ctx), operation); err != nil {
			reservation.Status = "cancellation_unknown"
			_ = service.reservations.PutReservation(context.WithoutCancel(ctx), reservation)
			return domain.CancellationDraft{}, err
		}
	}
	now = service.clock.Now()
	reservation.Status = "cancelled"
	reservation.CancelledAt = &now
	reservation.RefundAmount = draft.RefundAmount
	if err := service.reservations.PutReservation(ctx, reservation); err != nil {
		return domain.CancellationDraft{}, err
	}
	operation.Status = domain.ExternalOperationReconciled
	operation.UpdatedAt = service.clock.Now()
	if service.operations != nil {
		_ = service.operations.PutExternalOperation(context.WithoutCancel(ctx), operation)
	}
	return draft, nil
}
