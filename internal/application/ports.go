package application

import (
	"context"
	"errors"
	"time"

	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrConflict        = errors.New("conflict")
	ErrBookingNotOpen  = errors.New("booking is not open")
	ErrSeatUnavailable = errors.New("preferred seats are unavailable")
)

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	NewID() string
}

type Waiter interface {
	Wait(context.Context, time.Duration) error
}

// EventPublisher receives events only after their durable application record
// has been written. Implementations must not make the originating workflow
// depend on an external notification service.
type EventPublisher interface {
	Publish(context.Context, *clientpb.AppEvent) error
}

type TheaterRepository interface {
	GetTheater(context.Context, string) (*catalogpb.Theater, error)
	ListTheaters(context.Context) ([]*catalogpb.Theater, error)
}

type AuditoriumRepository interface {
	GetAuditorium(context.Context, string) (*catalogpb.Auditorium, error)
	ListAuditoriumsByTheater(context.Context, string) ([]*catalogpb.Auditorium, error)
}

type SeatMapRepository interface {
	GetSeatMap(context.Context, string) (*seatmappb.Snapshot, error)
}

type CatalogRepository interface {
	GetCatalog(context.Context) (*catalogpb.CatalogIndex, error)
}

type MoviePoster struct {
	MovieID     string
	MediaType   string
	ContentHash string
	Data        []byte
}

type MoviePosterRepository interface {
	GetMoviePoster(context.Context, string) (*MoviePoster, error)
}

// LiveSeatObservationRepository persists the exact layout and availability
// observed in the authenticated provider browser as one generated contract.
type LiveSeatObservationRepository interface {
	SubmitLiveSeatObservation(
		context.Context,
		*seatmappb.LiveSeatObservation,
	) (*seatmappb.Snapshot, error)
}

type PresetRepository interface {
	PutPreset(context.Context, *clientpb.Resource) error
	GetPreset(context.Context, string) (*clientpb.Resource, error)
	ListPresetsByUser(context.Context, string) ([]*clientpb.Resource, error)
	DeletePreset(context.Context, string) error
}

type MonitorRepository interface {
	PutMonitor(context.Context, *clientpb.Resource) error
	GetMonitor(context.Context, string) (*clientpb.Resource, error)
	ListMonitorsByUser(context.Context, string) ([]*clientpb.Resource, error)
	DeleteMonitor(context.Context, string) error
}

type ReservationRepository interface {
	PutReservation(context.Context, *clientpb.Resource) error
	GetReservation(context.Context, string) (*clientpb.Resource, error)
	ListReservationsByUser(context.Context, string) ([]*clientpb.Resource, error)
}

type ExternalOperationRepository interface {
	PutExternalOperation(context.Context, *clientpb.Resource) error
}

type BookingGateway interface {
	OpenSeatSelection(context.Context, *observationpb.SeatAvailabilityTask, int) (*seatmappb.LiveSeatObservation, error)
	PreparePayment(context.Context, *catalogpb.Showtime, []string) (*clientpb.Reservation, error)
	PrepareCancellation(context.Context, *clientpb.Reservation) (*clientpb.WebUICancellationResult, error)
	CommitCancellation(context.Context) error
}

// LiveSeatSelectionRefresher refreshes the already-open seat page without
// repeating cinema, date, or showtime navigation. Booking gateways that do not
// support this capability continue to use the single-attempt contract above.
type LiveSeatSelectionRefresher interface {
	RefreshSeatSelection(context.Context, *observationpb.SeatAvailabilityTask) (*seatmappb.LiveSeatObservation, error)
}
