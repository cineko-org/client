package application

import (
	"context"
	"errors"
	"time"

	"github.com/cineko-org/client/internal/domain"
	contracts "github.com/cineko-org/contracts/v3"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrConflict        = errors.New("conflict")
	ErrBookingNotOpen  = errors.New("booking is not open")
	ErrSeatUnavailable = errors.New("preferred seats are unavailable")
	ErrMonitorExpired  = errors.New("all target dates have passed")
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
	Publish(context.Context, domain.AppEvent) error
}

type TheaterRepository interface {
	PutTheater(context.Context, domain.Theater) error
	GetTheater(context.Context, string) (domain.Theater, error)
	ListTheaters(context.Context) ([]domain.Theater, error)
}

type AuditoriumRepository interface {
	PutAuditorium(context.Context, domain.Auditorium) error
	GetAuditorium(context.Context, string) (domain.Auditorium, error)
	ListAuditoriumsByTheater(context.Context, string) ([]domain.Auditorium, error)
}

type SeatMapRepository interface {
	PutSeatMap(context.Context, domain.SeatMap) error
	GetSeatMap(context.Context, string) (domain.SeatMap, error)
}

type PresetRepository interface {
	PutPreset(context.Context, domain.Preset) error
	GetPreset(context.Context, string) (domain.Preset, error)
	ListPresetsByUser(context.Context, string) ([]domain.Preset, error)
	DeletePreset(context.Context, string) error
}

type MonitorRepository interface {
	PutMonitor(context.Context, domain.MonitorJob) error
	GetMonitor(context.Context, string) (domain.MonitorJob, error)
	ListMonitorsByUser(context.Context, string) ([]domain.MonitorJob, error)
	DeleteMonitor(context.Context, string) error
	AcquireMonitor(context.Context, string, string, time.Time, time.Duration) (domain.MonitorJob, error)
	RenewMonitor(context.Context, string, string, time.Time, time.Duration) error
	ReleaseMonitor(context.Context, string, string) error
}

type ReservationRepository interface {
	PutReservation(context.Context, domain.Reservation) error
	GetReservation(context.Context, string) (domain.Reservation, error)
	ListReservationsByUser(context.Context, string) ([]domain.Reservation, error)
}

type ExternalOperationRepository interface {
	PutExternalOperation(context.Context, domain.ExternalOperation) error
}

type GlobalCatalogRepository interface {
	PublishCatalogSnapshot(context.Context, contracts.CatalogSnapshot) error
	GetCatalog(context.Context) (contracts.CatalogIndex, error)
}

type ConfigurationRepository interface {
	SnapshotConfiguration(context.Context, string) (domain.Configuration, error)
	ReplaceConfiguration(context.Context, domain.Configuration) error
}

type TheaterRef struct {
	Region string
	Name   string
}

type AuditoriumObservation struct {
	Auditorium            domain.Auditorium
	RepresentativeShowing *domain.Showtime
}

type CatalogGateway interface {
	ResolveTheater(context.Context, TheaterRef) (domain.Theater, error)
	DiscoverAuditoriums(context.Context, domain.Theater, []string) ([]AuditoriumObservation, error)
	CaptureSeatMap(context.Context, domain.Auditorium, domain.Showtime) (domain.SeatMap, error)
}

type ShowtimeQuery struct {
	MovieID      string
	Movie        string
	Theater      domain.Theater
	Auditorium   domain.Auditorium
	TargetDates  []string
	EarliestTime string
	LatestTime   string
}

type ShowtimeGateway interface {
	FindShowtimes(context.Context, ShowtimeQuery) ([]domain.Showtime, error)
}

type BookingGateway interface {
	OpenSeatSelection(context.Context, domain.Showtime, int) (domain.SeatSelection, error)
	PreparePayment(context.Context, domain.Showtime, []string) (domain.BookingDraft, error)
	PrepareCancellation(context.Context, domain.Reservation) (domain.CancellationDraft, error)
	CommitCancellation(context.Context) error
}
