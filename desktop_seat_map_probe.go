package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync/atomic"
	"time"

	"github.com/cineko-org/client/internal/adapters/browserfactory"
	"github.com/cineko-org/client/internal/adapters/cgv"
	"github.com/cineko-org/client/internal/adapters/egress"
	"github.com/cineko-org/client/internal/application"
	"github.com/cineko-org/client/internal/domain"
	central "github.com/cineko-org/contracts/v3"
)

type seatMapCapabilityState struct {
	authenticated atomic.Bool
}

func (state *seatMapCapabilityState) SetAuthenticated(authenticated bool) {
	state.authenticated.Store(authenticated)
}

func (state *seatMapCapabilityState) AvailableCapabilities() []string {
	capabilities := []string{central.CapabilityCGVCatalogCapture, central.CapabilityCGVScheduleCapture}
	if state.authenticated.Load() {
		capabilities = append(capabilities, central.CapabilityCGVSeatMapCapture)
	}
	return capabilities
}

type clientSeatMapExecutor struct {
	browsers *browserfactory.Factory
	userID   string
	state    *seatMapCapabilityState
}

func (executor *clientSeatMapExecutor) CaptureSeatMap(
	ctx context.Context,
	task central.AssignmentTask,
) (*central.SeatMapVersion, error) {
	if task.Kind != central.CapabilityCGVSeatMapCapture || task.Auditorium == nil || task.Showtime == nil {
		return nil, errors.New("seat-map assignment target is incomplete")
	}
	if task.Theater.ID == "" || task.Auditorium.TheaterID != task.Theater.ID ||
		task.Showtime.Auditorium.ID != task.Auditorium.ID {
		return nil, errors.New("seat-map assignment identities do not match")
	}
	browser, err := executor.browsers.Open(ctx, browserfactory.Task{
		Purpose: egress.PurposeSession, SessionKey: executor.userID, Headless: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open authenticated seat-map browser: %w", err)
	}
	defer browser.Close()
	authenticated, err := browser.IsAuthenticated(ctx)
	if err != nil {
		executor.state.SetAuthenticated(false)
		return nil, fmt.Errorf("verify CGV session for seat-map capture: %w", err)
	}
	executor.state.SetAuthenticated(authenticated)
	if !authenticated {
		return nil, cgv.ErrAuthenticationRequired
	}
	if _, err := browser.ResolveTheater(ctx, application.TheaterRef{
		Region: task.Theater.Region,
		Name:   task.Theater.Name,
	}); err != nil {
		return nil, fmt.Errorf("select seat-map theater: %w", err)
	}
	auditorium := domain.Auditorium{
		ID: task.Auditorium.ID, TheaterID: task.Auditorium.TheaterID, SourceKey: task.Auditorium.SourceKey,
		Name: task.Auditorium.Name, ScreenTypes: append([]string(nil), task.Auditorium.ScreenTypes...),
		Capacity: task.Auditorium.Capacity,
	}
	showtime, err := seatMapShowtime(task, auditorium)
	if err != nil {
		return nil, err
	}
	seatMap, err := browser.CaptureSeatMap(ctx, auditorium, showtime)
	if err != nil {
		if errors.Is(err, cgv.ErrAuthenticationRequired) {
			executor.state.SetAuthenticated(false)
		}
		return nil, fmt.Errorf("capture CGV seat map: %w", err)
	}
	if seatMap.Evidence.ScreenshotPath != "" {
		defer func() { _ = os.Remove(seatMap.Evidence.ScreenshotPath) }()
	}
	return staticSeatMapVersion(auditorium.ID, seatMap)
}

func staticSeatMapVersion(auditoriumID string, seatMap domain.SeatMap) (*central.SeatMapVersion, error) {
	seats, zones, blocks := canonicalStaticLayout(seatMap)
	layout, err := json.Marshal(struct {
		Seats  []domain.Seat        `json:"seats"`
		Zones  []domain.LayoutZone  `json:"zones"`
		Blocks []domain.LayoutBlock `json:"blocks"`
	}{Seats: seats, Zones: zones, Blocks: blocks})
	if err != nil {
		return nil, fmt.Errorf("encode static seat-map layout: %w", err)
	}
	return &central.SeatMapVersion{
		AuditoriumID: auditoriumID, Capacity: len(seatMap.Seats), Layout: layout, ObservedAt: seatMap.ObservedAt,
	}, nil
}

func canonicalStaticLayout(seatMap domain.SeatMap) ([]domain.Seat, []domain.LayoutZone, []domain.LayoutBlock) {
	seats := make([]domain.Seat, len(seatMap.Seats))
	copy(seats, seatMap.Seats)
	for index := range seats {
		seats[index].Features = sortedStrings(seats[index].Features)
		seats[index].SourceClasses = sortedStrings(seats[index].SourceClasses)
	}
	sort.Slice(seats, func(left, right int) bool { return seats[left].Label < seats[right].Label })
	zones := make([]domain.LayoutZone, len(seatMap.Zones))
	copy(zones, seatMap.Zones)
	sort.Slice(zones, func(left, right int) bool {
		if zones[left].Code == zones[right].Code {
			return zones[left].Name < zones[right].Name
		}
		return zones[left].Code < zones[right].Code
	})
	blocks := make([]domain.LayoutBlock, len(seatMap.Blocks))
	copy(blocks, seatMap.Blocks)
	sort.Slice(blocks, func(left, right int) bool {
		if blocks[left].Code != blocks[right].Code {
			return blocks[left].Code < blocks[right].Code
		}
		if blocks[left].MinY != blocks[right].MinY {
			return blocks[left].MinY < blocks[right].MinY
		}
		return blocks[left].MinX < blocks[right].MinX
	})
	return seats, zones, blocks
}

func sortedStrings(values []string) []string {
	result := make([]string, len(values))
	copy(result, values)
	sort.Strings(result)
	return result
}

func seatMapShowtime(task central.AssignmentTask, auditorium domain.Auditorium) (domain.Showtime, error) {
	location, err := time.LoadLocation(task.TimeZone)
	if err != nil {
		return domain.Showtime{}, fmt.Errorf("load seat-map assignment time zone: %w", err)
	}
	showtime := task.Showtime
	if showtime.StartsAt.IsZero() || showtime.EndsAt.IsZero() || !showtime.EndsAt.After(showtime.StartsAt) {
		return domain.Showtime{}, errors.New("seat-map assignment showtime range is invalid")
	}
	if showtime.ProviderID == "" || showtime.SourceKey == "" || showtime.Movie.ID == "" || showtime.Movie.Title == "" {
		return domain.Showtime{}, errors.New("seat-map assignment movie identity is incomplete")
	}
	return domain.Showtime{
		ID: showtime.ID, ProviderID: showtime.ProviderID, SourceKey: showtime.SourceKey,
		MovieID: showtime.Movie.ID, Movie: showtime.Movie.Title, PosterURL: showtime.Movie.PosterURL,
		TheaterID: task.Theater.ID, TheaterRegion: task.Theater.Region, TheaterName: task.Theater.Name,
		AuditoriumID: auditorium.ID, AuditoriumName: auditorium.Name,
		ScreenTypes: append([]string(nil), auditorium.ScreenTypes...),
		Date:        showtime.StartsAt.In(location).Format(time.DateOnly),
		StartsAt:    showtime.StartsAt.In(location).Format("15:04"), EndsAt: showtime.EndsAt.In(location).Format("15:04"),
		AvailableSeats: showtime.AvailableSeats, Capacity: showtime.Capacity, SoldOut: showtime.SoldOut,
		ObservedAt: time.Now().UTC(),
	}, nil
}
