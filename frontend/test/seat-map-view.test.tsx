import { create } from '@bufbuild/protobuf';
import { MantineProvider } from '@mantine/core';
import { cleanup, fireEvent, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cinekoTheme } from '../src/app/theme';
import { LayoutSchema, SeatSchema, SnapshotSchema } from '../src/api/proto';
import { SeatMapView } from '../src/features/presets/ui/SeatMapView';

const seatMap = create(SnapshotSchema, {
  id: 'seat-map',
  auditoriumId: 'auditorium',
  layoutHash: 'layout',
  capacity: 3,
  layout: create(LayoutSchema, {
    seats: [
      create(SeatSchema, { id: 'A1', auditoriumId: 'auditorium', label: 'A1', row: 'A', number: 1, x: 0.2, y: 0.25, type: 'standard' }),
      create(SeatSchema, { id: 'A2', auditoriumId: 'auditorium', label: 'A2', row: 'A', number: 2, x: 0.5, y: 0.25, type: 'standard' }),
      create(SeatSchema, { id: 'B3', auditoriumId: 'auditorium', label: 'B3', row: 'B', number: 3, x: 0.8, y: 0.75, type: 'standard' }),
    ],
  }),
});

beforeEach(() => {
  vi.stubGlobal('matchMedia', vi.fn<(query: string) => MediaQueryList>().mockImplementation((query) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    addListener: () => undefined,
    removeListener: () => undefined,
    dispatchEvent: () => true,
  })));
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function renderSeatMap(pickedSeats: string[], onToggleSeat: (label: string) => void) {
  return render(
    <MantineProvider theme={cinekoTheme}>
      <SeatMapView
        seatMap={seatMap}
        pickedSeats={pickedSeats}
        onToggleSeat={onToggleSeat}
        onClear={() => undefined}
      />
    </MantineProvider>,
  );
}

describe('seat map selection', () => {
  it('selects each seat once while the primary pointer is dragged across it', () => {
    const onToggleSeat = vi.fn<(label: string) => void>();
    const { container } = renderSeatMap([], onToggleSeat);
    const first = container.querySelector<HTMLElement>('[data-seat-label="A1"]');
    const second = container.querySelector<HTMLElement>('[data-seat-label="A2"]');
    if (!first || !second) throw new Error('seat buttons were not rendered');

    fireEvent.pointerDown(first, { button: 0, buttons: 1 });
    fireEvent.pointerEnter(second, { buttons: 1 });
    fireEvent.pointerEnter(second, { buttons: 1 });
    fireEvent.pointerUp(window, { button: 0, buttons: 0 });

    expect(onToggleSeat.mock.calls).toEqual([['A1'], ['A2']]);
  });

  it('uses the starting seat to choose drag deselection mode', () => {
    const onToggleSeat = vi.fn<(label: string) => void>();
    const { container } = renderSeatMap(['A1', 'A2'], onToggleSeat);
    const first = container.querySelector<HTMLElement>('[data-seat-label="A1"]');
    const second = container.querySelector<HTMLElement>('[data-seat-label="A2"]');
    if (!first || !second) throw new Error('seat buttons were not rendered');

    fireEvent.pointerDown(first, { button: 0, buttons: 1 });
    fireEvent.pointerEnter(second, { buttons: 1 });
    fireEvent.pointerUp(window, { button: 0, buttons: 0 });

    expect(onToggleSeat.mock.calls).toEqual([['A1'], ['A2']]);
  });
});
