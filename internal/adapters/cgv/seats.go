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
	if err := adapter.openShowtime(showtime); err != nil {
		return seatSelection{}, err
	}
	snapshot, err := adapter.refreshSeatSnapshot(ctx, showtime.AuditoriumID)
	if err != nil {
		return seatSelection{}, err
	}
	if err := adapter.configureSeatSelection(showtime, seatCount); err != nil {
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

func (adapter *Adapter) openShowtime(showtime domain.Showtime) error {
	region := showtime.TheaterRegion
	if region == "" {
		region = adapter.selectedRegion
	}
	if region == "" {
		return fmt.Errorf("%w: theater region is unavailable", ErrUIContractChanged)
	}
	if err := adapter.selectCinemaTheater(region, showtime.TheaterName); err != nil {
		return err
	}
	if err := adapter.selectDate(showtime.Date); err != nil {
		return err
	}
	clicked, err := adapter.clickExactShowtime(showtime)
	if err != nil {
		return err
	}
	if !clicked {
		return fmt.Errorf("%w: exact showtime %s was not found", ErrUIContractChanged, showtime.ID)
	}
	if err := adapter.wait(800 * time.Millisecond); err != nil {
		return err
	}
	nonMember, err := adapter.clickButtonExact("비회원 예매")
	if err != nil {
		return err
	}
	if nonMember {
		if err := adapter.wait(500 * time.Millisecond); err != nil {
			return err
		}
	}
	loginRequired, err := adapter.bodyContains("CGV 회원 로그인이 필요한 서비스")
	if err != nil {
		return err
	}
	if loginRequired {
		return ErrAuthenticationRequired
	}
	return nil
}

func (adapter *Adapter) clickExactShowtime(showtime domain.Showtime) (bool, error) {
	if showtime.SourceLabel != "" {
		return adapter.clickButtonExact(showtime.SourceLabel)
	}
	expression := fmt.Sprintf(`(() => {
		const expected = {movie: %s, auditorium: %s, startsAt: %s, endsAt: %s};
		const normalize = value => (value || '').replace(/\s+/g, ' ').trim();
		const previous = (element, selector) => {
			let current = element;
			while (current) {
				let sibling = current.previousElementSibling;
				while (sibling) {
					if (sibling.matches && sibling.matches(selector)) return sibling;
					const nested = sibling.querySelectorAll ? window.__cinekoQueryAll(selector, sibling) : [];
					if (nested.length) return nested[nested.length - 1];
					sibling = sibling.previousElementSibling;
				}
				current = current.parentElement;
			}
			return null;
		};
		const button = window.__cinekoQueryAll('button').find(candidate => {
			if (candidate.disabled) return false;
			const label = normalize(candidate.innerText || candidate.textContent);
			if (!label.includes(expected.startsAt) || !label.includes(expected.endsAt)) return false;
			const movieHeading = previous(candidate, 'h2');
			const poster = movieHeading && window.__cinekoQuery('img[alt*="포스터"]', movieHeading);
			const movie = poster
				? normalize(poster.getAttribute('alt')).replace(/\s*포스터$/, '')
				: normalize(movieHeading && movieHeading.innerText);
			const structured = window.__cinekoQuery('[class*="_theater__"]', candidate);
			const group = previous(candidate, 'h3');
			const auditorium = normalize(structured && structured.innerText) || normalize(group && group.innerText);
			return movie.toLocaleLowerCase('ko-KR') === expected.movie.toLocaleLowerCase('ko-KR') &&
				(auditorium === expected.auditorium || auditorium.includes(expected.auditorium));
		});
		if (!button) return false;
		button.click();
		return true;
	})()`, jsString(showtime.Movie), jsString(showtime.AuditoriumName),
		jsString(showtime.StartsAt), jsString(showtime.EndsAt))
	var clicked bool
	if err := adapter.evaluate(expression, &clicked); err != nil {
		return false, err
	}
	return clicked, nil
}

func (adapter *Adapter) configureSeatSelection(showtime domain.Showtime, seatCount int) error {
	if err := adapter.selectPartySize(seatCount); err != nil {
		return err
	}
	if err := adapter.selectSeatPageShowtime(showtime); err != nil {
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

func (adapter *Adapter) selectSeatPageShowtime(showtime domain.Showtime) error {
	expression := fmt.Sprintf(`(() => {
		const startsAt = %s;
		const endsAt = %s;
		const normalize = value => (value || '').replace(/\s+/g, ' ').trim();
		const button = window.__cinekoQueryAll('button').find(item => {
			const label = normalize(item.getAttribute('aria-label') || item.innerText || item.textContent);
			return !item.disabled && label.includes(startsAt) && (!endsAt || label.includes(endsAt));
		});
		if (!button) return false;
		button.click();
		return true;
	})()`, jsString(showtime.StartsAt), jsString(showtime.EndsAt))
	var selected bool
	if err := adapter.evaluate(expression, &selected); err != nil {
		return err
	}
	if !selected {
		return fmt.Errorf("%w: seat-page showtime %s was not found", ErrUIContractChanged, showtime.StartsAt)
	}
	return nil
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
		const elements = window.__cinekoQueryAll('button,[role="button"],[data-seat]');
		const seats = [];
		for (const element of elements) {
			const source = normalize(
				element.getAttribute('aria-label') || element.getAttribute('title') ||
				element.getAttribute('data-seat') || element.innerText || element.textContent
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
