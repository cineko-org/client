package cgv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/domain"
	contracts "github.com/cineko-org/contracts/v3"
)

type rawSeat struct {
	Label    string   `json:"label"`
	Source   string   `json:"source"`
	Classes  []string `json:"classes"`
	X        float64  `json:"x"`
	Y        float64  `json:"y"`
	Disabled bool     `json:"disabled"`
}

func (adapter *Adapter) CaptureSeatMap(
	ctx context.Context,
	auditorium domain.Auditorium,
	showtime domain.Showtime,
) (domain.SeatMap, error) {
	selection, err := adapter.openSeats(ctx, showtime, 1)
	if err != nil {
		return domain.SeatMap{}, err
	}
	for index := range selection.snapshot.Seats {
		selection.snapshot.Seats[index].AuditoriumID = auditorium.ID
	}
	screenshotPath, err := adapter.Capture("seat-map-" + auditorium.Name)
	if err != nil {
		return domain.SeatMap{}, fmt.Errorf("capture auditorium evidence: %w", err)
	}
	// #nosec G304 -- Capture returns a generated path rooted in the configured artifacts directory.
	screenshot, err := os.ReadFile(screenshotPath)
	if err != nil {
		return domain.SeatMap{}, fmt.Errorf("read auditorium evidence: %w", err)
	}
	evidenceHash := sha256.Sum256(screenshot)
	return domain.SeatMap{
		AuditoriumID: auditorium.ID,
		Version: seatMapVersion(
			selection.snapshot.Seats, selection.snapshot.Zones, selection.snapshot.Blocks,
		),
		Seats:  selection.snapshot.Seats,
		Zones:  selection.snapshot.Zones,
		Blocks: selection.snapshot.Blocks,
		Evidence: domain.LayoutEvidence{
			ScreenshotPath: screenshotPath, ScreenshotSHA256: hex.EncodeToString(evidenceHash[:]),
			SnapshotSHA256: selection.snapshot.Hash, SourceShowtimeID: showtime.ID,
			DOMSeatCount: selection.domSeatCount, SnapshotSeatCount: len(selection.snapshot.Seats),
			CaptureTrigger: "refresh-button", CapturedAt: selection.snapshot.Captured,
		},
		ObservedAt: selection.snapshot.Captured,
	}, nil
}

func (adapter *Adapter) OpenSeatSelection(
	ctx context.Context,
	showtime domain.Showtime,
	seatCount int,
) (domain.SeatSelection, error) {
	selection, err := adapter.openSeats(ctx, showtime, seatCount)
	if err != nil {
		return domain.SeatSelection{}, err
	}
	return domain.SeatSelection{
		SeatMap: domain.SeatMap{
			AuditoriumID: showtime.AuditoriumID,
			Seats:        selection.snapshot.Seats,
			Zones:        selection.snapshot.Zones,
			Blocks:       selection.snapshot.Blocks,
			ObservedAt:   selection.snapshot.Captured,
		},
		LiveSeats: selection.live,
	}, nil
}

type seatSelection struct {
	live         []domain.LiveSeat
	snapshot     parsedSeatSnapshot
	domSeatCount int
}

func (adapter *Adapter) openSeats(
	ctx context.Context,
	showtime domain.Showtime,
	seatCount int,
) (seatSelection, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return seatSelection{}, err
	}
	if adapter.selectedRegion == "" {
		return seatSelection{}, fmt.Errorf("%w: theater context is unavailable", ErrUIContractChanged)
	}
	selectedShowtime, err := adapter.openShowtime(showtime)
	if err != nil {
		return seatSelection{}, err
	}
	snapshot, err := adapter.refreshSeatSnapshot(ctx, showtime.AuditoriumID)
	if err != nil {
		return seatSelection{}, err
	}
	if err := adapter.configureSeatSelection(selectedShowtime, seatCount); err != nil {
		return seatSelection{}, err
	}
	raw, err := adapter.validatedSeatNodes(snapshot)
	if err != nil {
		return seatSelection{}, err
	}
	return seatSelection{
		live: intersectAvailability(snapshot.Live, raw), snapshot: snapshot, domSeatCount: len(raw),
	}, nil
}

func (adapter *Adapter) openShowtime(showtime domain.Showtime) (domain.Showtime, error) {
	region := showtime.TheaterRegion
	if region == "" {
		region = adapter.selectedRegion
	}
	if region == "" {
		return domain.Showtime{}, fmt.Errorf("%w: theater region is unavailable", ErrUIContractChanged)
	}
	if err := adapter.selectCinemaTheater(region, showtime.TheaterName); err != nil {
		return domain.Showtime{}, err
	}
	if err := adapter.selectDate(showtime.Date); err != nil {
		return domain.Showtime{}, err
	}
	rows, err := adapter.captureScheduleRows()
	if err != nil {
		return domain.Showtime{}, fmt.Errorf("capture commanded CGV showtime: %w", err)
	}
	providerShowtime, err := commandedShowtime(rows, showtime)
	if err != nil {
		return domain.Showtime{}, err
	}
	clicked, err := adapter.clickExactShowtime(providerShowtime)
	if err != nil {
		return domain.Showtime{}, err
	}
	if !clicked {
		return domain.Showtime{}, fmt.Errorf("%w: exact showtime %s was not found", ErrUIContractChanged, showtime.ID)
	}
	if err := adapter.wait(800 * time.Millisecond); err != nil {
		return domain.Showtime{}, err
	}
	nonMember, err := adapter.clickButtonExact("비회원 예매")
	if err != nil {
		return domain.Showtime{}, err
	}
	if nonMember {
		if err := adapter.wait(500 * time.Millisecond); err != nil {
			return domain.Showtime{}, err
		}
	}
	loginRequired, err := adapter.bodyContains("CGV 회원 로그인이 필요한 서비스")
	if err != nil {
		return domain.Showtime{}, err
	}
	if loginRequired {
		return domain.Showtime{}, ErrAuthenticationRequired
	}
	return providerShowtime, nil
}

// commandedShowtime resolves the command tuple against the fresh provider
// response. The returned display projection is the only identity allowed at
// the DOM boundary; command display text is never trusted for this lookup.
func commandedShowtime(rows []providerScheduleRow, command domain.Showtime) (domain.Showtime, error) {
	if err := validateShowtimeIdentity(command); err != nil {
		return domain.Showtime{}, err
	}
	var matches []providerScheduleRow
	for _, row := range rows {
		if showtimeSourceKey(row.SiteNo, row.Date, row.AuditoriumNo, row.Sequence) != command.SourceKey {
			continue
		}
		if contracts.CatalogID(contracts.ProviderCGV, "movie", row.MovieNo) != command.MovieID {
			return domain.Showtime{}, fmt.Errorf("%w: provider tuple movie does not match command", ErrUIContractChanged)
		}
		matches = append(matches, row)
	}
	if len(matches) != 1 {
		return domain.Showtime{}, fmt.Errorf("%w: expected one provider row for %s, got %d", ErrUIContractChanged, command.SourceKey, len(matches))
	}
	entry, err := scheduleEntryFromProviderRow(matches[0], domain.Theater{
		ID: command.TheaterID, Name: command.TheaterName,
	})
	if err != nil {
		return domain.Showtime{}, fmt.Errorf("%w: provider showtime display projection is incomplete: %w", ErrUIContractChanged, err)
	}
	if entry.Showtime.Date != command.Date {
		return domain.Showtime{}, fmt.Errorf("%w: provider schedule date does not match command", ErrUIContractChanged)
	}
	return entry.Showtime, nil
}

func (adapter *Adapter) clickExactShowtime(showtime domain.Showtime) (bool, error) {
	if err := validateShowtimeIdentity(showtime); err != nil {
		return false, err
	}
	seatTotals := scheduleSeatTotals(showtime)
	if seatTotals == "" {
		return false, fmt.Errorf("%w: showtime seat totals are incomplete", ErrUIContractChanged)
	}
	// CGV schedule buttons expose display text only; the authoritative tuple was
	// captured from searchMovScnInfo before this boundary. Never infer identity
	// from DOM attributes or silently choose among duplicate display rows.
	expression := fmt.Sprintf(`(() => {
		const expectedRow = [%s, %s];
		const expectedButton = [%s, %s];
		const expectedSeats = %s;
		const normalize = value => (value || '').replace(/\s+/g, ' ').trim();
		const compact = value => normalize(value).replace(/\s+/g, '');
		const scopeText = element => {
			const values = [];
			let current = element;
			for (let depth = 0; current && depth < 5; depth += 1, current = current.parentElement) {
				values.push(normalize(current.innerText || current.textContent));
			}
			return values.join(' ');
		};
		const matches = window.__cinekoQueryAll('button').filter(candidate => {
			if (candidate.disabled) return false;
			const buttonText = normalize(candidate.innerText || candidate.textContent);
			const renderedText = scopeText(candidate);
			return expectedRow.every(value => renderedText.includes(normalize(value))) &&
				expectedButton.every(value => buttonText.includes(normalize(value))) &&
				(!expectedSeats || compact(buttonText).includes(compact(expectedSeats)));
		});
		if (matches.length !== 1) return {count: matches.length, clicked: false};
		matches[0].scrollIntoView({block: 'center'});
		matches[0].click();
		return {count: 1, clicked: true};
	})()`, jsString(showtime.Movie), jsString(showtime.AuditoriumName), jsString(showtime.StartsAt), jsString(showtime.EndsAt), seatTotals)
	var result struct {
		Count   int  `json:"count"`
		Clicked bool `json:"clicked"`
	}
	if err := adapter.evaluate(expression, &result); err != nil {
		return false, err
	}
	if result.Count > 1 {
		return false, fmt.Errorf("%w: showtime display is ambiguous for provider tuple %s", ErrUIContractChanged, showtime.SourceKey)
	}
	return result.Clicked, nil
}

func (adapter *Adapter) configureSeatSelection(showtime domain.Showtime, seatCount int) error {
	if err := adapter.selectPartySize(seatCount); err != nil {
		return err
	}
	if err := adapter.verifySeatPageShowtime(showtime); err != nil {
		return err
	}
	return adapter.wait(500 * time.Millisecond)
}

func (adapter *Adapter) validatedSeatNodes(snapshot parsedSeatSnapshot) ([]rawSeat, error) {
	raw, err := adapter.extractSeatNodes()
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: no seat nodes found", ErrUIContractChanged)
	}
	if len(raw) != len(snapshot.Seats) {
		return nil, fmt.Errorf(
			"%w: DOM has %d seats but refreshed snapshot has %d",
			ErrUIContractChanged, len(raw), len(snapshot.Seats),
		)
	}
	return raw, nil
}

func (adapter *Adapter) refreshSeatSnapshot(
	ctx context.Context,
	auditoriumID string,
) (parsedSeatSnapshot, error) {
	for {
		select {
		case <-adapter.seatResponses:
		default:
			goto drained
		}
	}

drained:
	clicked, err := adapter.clickRefresh()
	if err != nil {
		return parsedSeatSnapshot{}, err
	}
	if !clicked {
		return parsedSeatSnapshot{}, fmt.Errorf("%w: seat Refresh button not found", ErrUIContractChanged)
	}
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return parsedSeatSnapshot{}, ctx.Err()
	case <-timer.C:
		return parsedSeatSnapshot{}, errors.New("timed out waiting for refreshed CGV seat data")
	case response := <-adapter.seatResponses:
		if response.err != nil {
			return parsedSeatSnapshot{}, fmt.Errorf("read refreshed CGV seat data: %w", response.err)
		}
		return parseSeatSnapshot(response.body, auditoriumID, time.Now())
	}
}

func (adapter *Adapter) clickRefresh() (bool, error) {
	const expression = `(() => {
		const normalize = value => (value || '').replace(/\s+/g, ' ').trim();
		const button = window.__cinekoQueryAll('button').find(item => {
			const label = normalize(item.getAttribute('aria-label') || item.title || item.innerText || item.textContent);
			return !item.disabled && (label === '새로고침' || label === 'Refresh');
		});
		if (!button) return false;
		button.click();
		return true;
	})()`
	var clicked bool
	err := adapter.evaluate(expression, &clicked)
	return clicked, err
}

// verifySeatPageShowtime checks the fields CGV renders after navigation. The
// seat page has no provider tuple in its DOM, so a mismatch is a hard stop.
func (adapter *Adapter) verifySeatPageShowtime(showtime domain.Showtime) error {
	if err := validateShowtimeIdentity(showtime); err != nil {
		return err
	}
	dateVariants, err := showtimeDateDisplayVariants(showtime.Date)
	if err != nil {
		return err
	}
	encodedDates := make([]string, 0, len(dateVariants))
	for _, value := range dateVariants {
		encodedDates = append(encodedDates, jsString(value))
	}
	expression := fmt.Sprintf(`(() => {
		const text = (document.body && (document.body.innerText || document.body.textContent) || '').replace(/\s+/g, ' ').trim();
		const expectedDate = [%s];
		return {
			movie: text.includes(%s),
			theater: text.includes(%s),
			auditorium: text.includes(%s),
			start: text.includes(%s),
			end: text.includes(%s),
			date: expectedDate.some(value => text.includes(value))
		};
	})()`, strings.Join(encodedDates, ","), jsString(showtime.Movie), jsString(showtime.TheaterName), jsString(showtime.AuditoriumName), jsString(showtime.StartsAt), jsString(showtime.EndsAt))
	var result struct {
		Movie      bool `json:"movie"`
		Theater    bool `json:"theater"`
		Auditorium bool `json:"auditorium"`
		Start      bool `json:"start"`
		End        bool `json:"end"`
		Date       bool `json:"date"`
	}
	if err := adapter.evaluate(expression, &result); err != nil {
		return err
	}
	if !result.Movie || !result.Theater || !result.Auditorium || !result.Start || !result.End || !result.Date {
		return fmt.Errorf("%w: seat-page showtime display does not match provider tuple %s", ErrUIContractChanged, showtime.SourceKey)
	}
	return nil
}

func validateShowtimeIdentity(showtime domain.Showtime) error {
	parts := strings.Split(strings.TrimSpace(showtime.SourceKey), "/")
	if strings.TrimSpace(showtime.ProviderID) != contracts.ProviderCGV || len(parts) != 4 {
		return fmt.Errorf("%w: provider showtime tuple is incomplete", ErrUIContractChanged)
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return fmt.Errorf("%w: provider showtime tuple is incomplete", ErrUIContractChanged)
		}
	}
	for name, value := range map[string]string{
		"movie": showtime.Movie, "theater": showtime.TheaterName,
		"auditorium": showtime.AuditoriumName, "date": showtime.Date, "start": showtime.StartsAt,
		"end": showtime.EndsAt,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: showtime %s display identity is incomplete", ErrUIContractChanged, name)
		}
	}
	return nil
}

func scheduleSeatTotals(showtime domain.Showtime) string {
	if showtime.Capacity <= 0 || showtime.AvailableSeats < 0 || showtime.AvailableSeats > showtime.Capacity {
		return ""
	}
	return fmt.Sprintf("%d/%d석", showtime.AvailableSeats, showtime.Capacity)
}

func showtimeDateDisplayVariants(isoDate string) ([]string, error) {
	parsed, err := time.ParseInLocation(time.DateOnly, isoDate, domain.KoreaLocation)
	if err != nil {
		return nil, fmt.Errorf("%w: showtime date %q is invalid", ErrUIContractChanged, isoDate)
	}
	return []string{
		parsed.Format(time.DateOnly),
		parsed.Format("2006.01.02"),
		parsed.Format("2006/01/02"),
		fmt.Sprintf("%d년 %d월 %d일", parsed.Year(), parsed.Month(), parsed.Day()),
		fmt.Sprintf("%d월 %d일", parsed.Month(), parsed.Day()),
		parsed.Format("01/02"),
	}, nil
}

func (adapter *Adapter) selectPartySize(count int) error {
	expression := fmt.Sprintf(`(() => {
		const target = %d;
		const normalize = value => (value || '').replace(/\s+/g, ' ').trim();
		const groups = window.__cinekoQueryAll('[role="group"]');
		let group = groups.find(item => normalize(item.getAttribute('aria-label')) === '일반');
		let button = group && window.__cinekoQueryAll('button', group)
			.find(item => !item.disabled && normalize(item.innerText || item.textContent) === String(target));
		if (!button) {
			const sections = window.__cinekoQueryAll('section,div,li')
				.filter(element => /^(일반|성인)(\s|$)/.test(normalize(element.innerText || '')));
			for (const section of sections) {
				button = window.__cinekoQueryAll('button', section)
					.find(item => !item.disabled && normalize(item.innerText || item.textContent) === String(target));
				if (button) break;
			}
		}
		if (!button) return false;
		button.click();
		return true;
	})()`, count)
	var selected bool
	if err := adapter.evaluate(expression, &selected); err != nil {
		return err
	}
	if !selected {
		return fmt.Errorf("%w: party size %d control not found", ErrUIContractChanged, count)
	}
	return nil
}

func (adapter *Adapter) extractSeatNodes() ([]rawSeat, error) {
	const expression = `(() => {
		const normalize = value => (value || '').replace(/\s+/g, ' ').trim();
		const pattern = /(?:^|\s)([A-Z])\s?(\d{1,2})(?:\s|$)/i;
		const elements = window.__cinekoQueryAll('button,[role="button"],[data-seatlocno]');
		const seats = [];
		for (const element of elements) {
			const source = normalize(
				element.getAttribute('aria-label') || element.getAttribute('title') ||
				element.getAttribute('data-seatlocno') || element.innerText || element.textContent
			);
			const match = source.match(pattern);
			if (!match) continue;
			const rect = element.getBoundingClientRect();
			if (rect.width <= 0 || rect.height <= 0) continue;
			const classes = String(element.className || '').split(/\s+/).filter(Boolean);
			seats.push({
				label: match[1].toUpperCase() + Number(match[2]),
				source,
				classes,
				x: rect.x + rect.width / 2,
				y: rect.y + rect.height / 2,
				disabled: !!element.disabled || element.getAttribute('aria-disabled') === 'true' ||
					classes.some(value => /disabled|sold|reserved|unavailable/i.test(value))
			});
		}
		return seats;
	})()`
	var raw []rawSeat
	if err := adapter.evaluate(expression, &raw); err != nil {
		return nil, fmt.Errorf("extract seat map: %w", err)
	}
	unique := make(map[string]rawSeat, len(raw))
	for _, seat := range raw {
		unique[seat.Label] = seat
	}
	raw = raw[:0]
	for _, seat := range unique {
		raw = append(raw, seat)
	}
	sort.Slice(raw, func(i, j int) bool { return raw[i].Label < raw[j].Label })
	return raw, nil
}

func intersectAvailability(live []domain.LiveSeat, raw []rawSeat) []domain.LiveSeat {
	enabled := make(map[string]bool, len(raw))
	for _, seat := range raw {
		enabled[seat.Label] = !seat.Disabled
	}
	result := append([]domain.LiveSeat(nil), live...)
	for index := range result {
		result[index].Available = result[index].Available && enabled[result[index].Label]
		result[index].Source = "cgv-seat-snapshot+enabled-button"
	}
	return result
}

func normalizeAxis(value, minimum, maximum float64) float64 {
	if maximum == minimum {
		return 0.5
	}
	return (value - minimum) / (maximum - minimum)
}

func inferSeatType(source string, classes []string) domain.SeatType {
	haystack := strings.ToLower(source + " " + strings.Join(classes, " "))
	for _, candidate := range []struct {
		keywords []string
		seatType domain.SeatType
	}{
		{[]string{"wheel", "휠체어"}, domain.SeatTypeWheelchair},
		{[]string{"companion", "보호자"}, domain.SeatTypeCompanion},
		{[]string{"couple", "sweetbox", "커플"}, domain.SeatTypeCouple},
		{[]string{"tempur", "bed", "템퍼"}, domain.SeatTypeBed},
		{[]string{"recliner", "stressless", "리클라이너"}, domain.SeatTypeRecliner},
		{[]string{"premium", "primium", "프리미엄"}, domain.SeatTypePremium},
		{[]string{"prime", "프라임"}, domain.SeatTypePrime},
		{[]string{"4dx", "motion", "모션"}, domain.SeatTypeMotion},
	} {
		for _, keyword := range candidate.keywords {
			if strings.Contains(haystack, keyword) {
				return candidate.seatType
			}
		}
	}
	if haystack != "" {
		return domain.SeatTypeStandard
	}
	return domain.SeatTypeUnknown
}

func seatMapVersion(
	seats []domain.Seat,
	zones []domain.LayoutZone,
	blocks []domain.LayoutBlock,
) string {
	sorted := append([]domain.Seat(nil), seats...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Label < sorted[j].Label })
	hash := sha256.New()
	for _, seat := range sorted {
		_, _ = fmt.Fprintf(
			hash, "seat|%s|%s|%d|%.6f|%.6f|%s|%s|%s|%s|%s|%t|%t|%s|%s|%s|%s\n",
			seat.Label, seat.Row, seat.Number, seat.X, seat.Y, seat.Type, seat.ZoneName,
			seat.ZoneKind, seat.SaleFormCode, seat.SaleFormName, seat.LeftAisle, seat.RightAisle,
			seat.SourceLabel, seat.SourceSeatKindCode, seat.SourceSeatKindName,
			canonicalStrings(append(append([]string(nil), seat.Features...), seat.SourceClasses...)),
		)
	}
	sortedZones := append([]domain.LayoutZone(nil), zones...)
	sort.Slice(sortedZones, func(i, j int) bool {
		if sortedZones[i].Code == sortedZones[j].Code {
			return sortedZones[i].Name < sortedZones[j].Name
		}
		return sortedZones[i].Code < sortedZones[j].Code
	})
	for _, zone := range sortedZones {
		_, _ = fmt.Fprintf(
			hash, "zone|%s|%s|%s|%s|%.6f|%.6f|%.6f|%.6f|%d\n",
			zone.Code, zone.Name, zone.KindCode, zone.KindName,
			zone.MinX, zone.MaxX, zone.MinY, zone.MaxY, zone.Capacity,
		)
	}
	sortedBlocks := append([]domain.LayoutBlock(nil), blocks...)
	sort.Slice(sortedBlocks, func(i, j int) bool {
		if sortedBlocks[i].Code == sortedBlocks[j].Code {
			if sortedBlocks[i].MinY == sortedBlocks[j].MinY {
				return sortedBlocks[i].MinX < sortedBlocks[j].MinX
			}
			return sortedBlocks[i].MinY < sortedBlocks[j].MinY
		}
		return sortedBlocks[i].Code < sortedBlocks[j].Code
	})
	for _, block := range sortedBlocks {
		_, _ = fmt.Fprintf(
			hash, "block|%s|%s|%s|%s|%.6f|%.6f|%.6f|%.6f\n",
			block.Code, block.Name, block.KindCode, block.KindName,
			block.MinX, block.MaxX, block.MinY, block.MaxY,
		)
	}
	return hex.EncodeToString(hash.Sum(nil)[:16])
}

func canonicalStrings(values []string) string {
	sort.Strings(values)
	return strings.Join(values, "\x1f")
}
