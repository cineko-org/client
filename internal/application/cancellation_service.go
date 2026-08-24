package application

import (
	"context"
	"fmt"

	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
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
	request *clientpb.WebUIReservationCancellationRequest,
) (*clientpb.WebUICancellationResult, error) {
	if request == nil || request.GetReservation() == nil {
		return nil, ErrNotFound
	}
	requestedReservation := request.GetReservation()
	resource, err := service.reservations.GetReservation(ctx, requestedReservation.GetId())
	if err != nil {
		return nil, ErrNotFound
	}
	reservation, _, err := reservationMessage(resource)
	if err != nil || reservation.GetUserId() != requestedReservation.GetUserId() {
		return nil, ErrNotFound
	}
	switch {
	case reservation.GetCancelled() != nil:
		// A retried UI request must be idempotent after the provider confirmed
		// cancellation. Never reopen the browser or issue a second provider
		// cancellation for the same reservation.
		return cancellationResultForReservation(reservation), nil
	case reservation.GetCancellationCommitting() != nil, reservation.GetCancellationUnknown() != nil:
		return nil, ErrConflict
	}
	draft, err := service.booking.PrepareCancellation(ctx, reservation)
	if err != nil {
		return nil, err
	}
	result := cloneCancellationResult(draft)
	if result.GetReservationId() == "" {
		result.SetReservationId(reservation.GetId())
	}
	if !request.GetCommit() {
		return result, nil
	}
	if err := service.commit(ctx, reservation, draft); err != nil {
		return nil, err
	}
	return result, nil
}

func (service *CancellationService) commit(
	ctx context.Context,
	reservation *clientpb.Reservation,
	draft *clientpb.WebUICancellationResult,
) error {
	// PrepareCancellation can take long enough for another local task
	// worker to advance the reservation. Refresh immediately before the first
	// CAS and use the revision returned by that read.
	current, refreshedRevision, err := service.currentReservation(ctx, reservation.GetId(), reservation.GetUserId())
	if err != nil {
		return err
	}
	reservation = current
	switch {
	case reservation.GetCancelled() != nil:
		// Another request may have completed between the initial read and this
		// commit. Treat that race as the same idempotent success.
		return nil
	case reservation.GetCancellationCommitting() != nil, reservation.GetCancellationUnknown() != nil,
		reservation.GetBooked() == nil:
		return ErrConflict
	}
	now := service.clock.Now()
	// The operation identity is stable across a retried request. A timestamp
	// here would turn an uncertain response into a second provider attempt.
	operationID := fmt.Sprintf("cancellation:%s", reservation.GetId())
	operation := clientpb.ExternalOperation_builder{
		Id: &operationID, UserId: stringPointer(reservation.GetUserId()), MonitorId: stringPointer(reservation.GetMonitorId()),
		ReservationId: stringPointer(reservation.GetId()), RefundAmount: stringPointer(draft.GetRefundAmount()),
		CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now),
		Cancellation: clientpb.CancellationOperation_builder{}.Build(), Prepared: clientpb.OperationPrepared_builder{}.Build(),
	}.Build()
	operationRevision := int64(0)
	reservation = cloneReservation(reservation)
	reservation.SetCancellationCommitting(clientpb.ReservationCancellationCommitting_builder{}.Build())
	if err := service.reservations.PutReservation(ctx, resourceForReservation(reservation, refreshedRevision)); err != nil {
		return err
	}
	if err := service.putExternalOperation(ctx, operation, &operationRevision); err != nil {
		return err
	}
	if err := service.booking.CommitCancellation(ctx); err != nil {
		operation.SetUnknown(clientpb.OperationUnknown_builder{}.Build())
		operation.SetLastError(err.Error())
		operation.SetUpdatedAt(timestamppb.New(service.clock.Now()))
		_ = service.putExternalOperation(context.WithoutCancel(ctx), operation, &operationRevision)
		if latest, latestRevision, refreshErr := service.currentReservation(context.WithoutCancel(ctx), reservation.GetId(), reservation.GetUserId()); refreshErr == nil && latest.GetCancellationCommitting() != nil {
			latest.SetCancellationUnknown(clientpb.ReservationCancellationUnknown_builder{}.Build())
			_ = service.reservations.PutReservation(context.WithoutCancel(ctx), resourceForReservation(latest, latestRevision))
		}
		return err
	}
	operation.SetConfirmed(clientpb.OperationConfirmed_builder{}.Build())
	operation.SetUpdatedAt(timestamppb.New(service.clock.Now()))
	if err := service.putExternalOperation(context.WithoutCancel(ctx), operation, &operationRevision); err != nil {
		if latest, latestRevision, refreshErr := service.currentReservation(context.WithoutCancel(ctx), reservation.GetId(), reservation.GetUserId()); refreshErr == nil && latest.GetCancellationCommitting() != nil {
			latest.SetCancellationUnknown(clientpb.ReservationCancellationUnknown_builder{}.Build())
			_ = service.reservations.PutReservation(context.WithoutCancel(ctx), resourceForReservation(latest, latestRevision))
		}
		return err
	}
	now = service.clock.Now()
	latest, latestRevision, err := service.currentReservation(ctx, reservation.GetId(), reservation.GetUserId())
	if err != nil {
		return err
	}
	if latest.GetCancellationCommitting() == nil {
		return ErrConflict
	}
	latest.SetCancelled(clientpb.ReservationCancelled_builder{}.Build())
	latest.SetCancelledAt(timestamppb.New(now))
	latest.SetRefundAmount(draft.GetRefundAmount())
	if err := service.reservations.PutReservation(ctx, resourceForReservation(latest, latestRevision)); err != nil {
		return err
	}
	operation.SetReconciled(clientpb.OperationReconciled_builder{}.Build())
	operation.SetUpdatedAt(timestamppb.New(service.clock.Now()))
	_ = service.putExternalOperation(context.WithoutCancel(ctx), operation, &operationRevision)
	return nil
}

func (service *CancellationService) putExternalOperation(
	ctx context.Context,
	operation *clientpb.ExternalOperation,
	revision *int64,
) error {
	if service.operations == nil {
		return nil
	}
	resource := resourceForExternalOperation(operation, *revision)
	if err := service.operations.PutExternalOperation(ctx, resource); err != nil {
		return err
	}
	*revision = resource.GetIdentity().GetRevision()
	return nil
}

func (service *CancellationService) currentReservation(ctx context.Context, id, userID string) (*clientpb.Reservation, int64, error) {
	resource, err := service.reservations.GetReservation(ctx, id)
	if err != nil {
		return nil, 0, ErrNotFound
	}
	reservation, revision, err := reservationMessage(resource)
	if err != nil || reservation.GetUserId() != userID {
		return nil, 0, ErrNotFound
	}
	return cloneReservation(reservation), revision, nil
}

func cloneCancellationResult(value *clientpb.WebUICancellationResult) *clientpb.WebUICancellationResult {
	if value == nil {
		return clientpb.WebUICancellationResult_builder{}.Build()
	}
	return proto.CloneOf(value)
}

func cancellationResultForReservation(reservation *clientpb.Reservation) *clientpb.WebUICancellationResult {
	if reservation == nil {
		return clientpb.WebUICancellationResult_builder{}.Build()
	}
	reservationID := reservation.GetId()
	bookingNumber := reservation.GetBookingNumber()
	refundAmount := reservation.GetRefundAmount()
	return clientpb.WebUICancellationResult_builder{
		ReservationId: &reservationID,
		BookingNumber: &bookingNumber,
		RefundAmount:  &refundAmount,
	}.Build()
}
