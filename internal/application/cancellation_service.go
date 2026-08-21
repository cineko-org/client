package application

import (
	"context"
	"fmt"

	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
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
	reservation, revision, err := reservationMessage(resource)
	if err != nil || reservation.GetUserId() != requestedReservation.GetUserId() {
		return nil, ErrNotFound
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
	if err := service.commit(ctx, reservation, revision, draft); err != nil {
		return nil, err
	}
	return result, nil
}

func (service *CancellationService) commit(
	ctx context.Context,
	reservation *clientpb.Reservation,
	revision int64,
	draft *clientpb.WebUICancellationResult,
) error {
	now := service.clock.Now()
	operationID := fmt.Sprintf("cancellation:%s:%d", reservation.GetId(), now.UnixNano())
	operation := clientpb.ExternalOperation_builder{
		Id: &operationID, UserId: stringPointer(reservation.GetUserId()), MonitorId: stringPointer(reservation.GetMonitorId()),
		ReservationId: stringPointer(reservation.GetId()), RefundAmount: stringPointer(draft.GetRefundAmount()),
		CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now),
		Cancellation: clientpb.CancellationOperation_builder{}.Build(), Prepared: clientpb.OperationPrepared_builder{}.Build(),
	}.Build()
	reservation = cloneReservation(reservation)
	reservation.SetCancellationCommitting(clientpb.ReservationCancellationCommitting_builder{}.Build())
	if err := service.reservations.PutReservation(ctx, resourceForReservation(reservation, revision)); err != nil {
		return err
	}
	if service.operations != nil {
		operationResource := resourceForExternalOperation(operation)
		if err := service.operations.PutExternalOperation(ctx, operationResource); err != nil {
			return err
		}
	}
	if err := service.booking.CommitCancellation(ctx); err != nil {
		operation.SetUnknown(clientpb.OperationUnknown_builder{}.Build())
		operation.SetLastError(err.Error())
		operation.SetUpdatedAt(timestamppb.New(service.clock.Now()))
		if service.operations != nil {
			_ = service.operations.PutExternalOperation(context.WithoutCancel(ctx), resourceForExternalOperation(operation))
		}
		reservation.SetCancellationUnknown(clientpb.ReservationCancellationUnknown_builder{}.Build())
		_ = service.reservations.PutReservation(context.WithoutCancel(ctx), resourceForReservation(reservation, revision))
		return err
	}
	operation.SetConfirmed(clientpb.OperationConfirmed_builder{}.Build())
	operation.SetUpdatedAt(timestamppb.New(service.clock.Now()))
	if service.operations != nil {
		operationResource := resourceForExternalOperation(operation)
		if err := service.operations.PutExternalOperation(context.WithoutCancel(ctx), operationResource); err != nil {
			reservation.SetCancellationUnknown(clientpb.ReservationCancellationUnknown_builder{}.Build())
			_ = service.reservations.PutReservation(context.WithoutCancel(ctx), resourceForReservation(reservation, revision))
			return err
		}
	}
	now = service.clock.Now()
	reservation.SetCancelled(clientpb.ReservationCancelled_builder{}.Build())
	reservation.SetCancelledAt(timestamppb.New(now))
	reservation.SetRefundAmount(draft.GetRefundAmount())
	if err := service.reservations.PutReservation(ctx, resourceForReservation(reservation, revision)); err != nil {
		return err
	}
	operation.SetReconciled(clientpb.OperationReconciled_builder{}.Build())
	operation.SetUpdatedAt(timestamppb.New(service.clock.Now()))
	if service.operations != nil {
		_ = service.operations.PutExternalOperation(context.WithoutCancel(ctx), resourceForExternalOperation(operation))
	}
	return nil
}

func cloneCancellationResult(value *clientpb.WebUICancellationResult) *clientpb.WebUICancellationResult {
	if value == nil {
		return clientpb.WebUICancellationResult_builder{}.Build()
	}
	return proto.CloneOf(value)
}
