import { describe, expect, it } from 'vitest';
import { liveSeatMapFixtures } from '../src/stories/liveSeatMaps';

const expectedLayouts = [
  { key: 'imax', seats: 624, types: { standard: 618, wheelchair: 6 } },
  { key: 'fourDx', seats: 144, types: { standard: 100, prime: 40, wheelchair: 4 } },
  { key: 'screenXRecliner', seats: 192, types: { recliner: 190, wheelchair: 2 } },
  { key: 'standard6', seats: 190, types: { standard: 188, wheelchair: 2 } },
  { key: 'premium17', seats: 12, types: { premium: 12 } },
] as const;

describe('live CGV seat map fixtures', () => {
  it.each(expectedLayouts)('preserves $key seat labels, coordinates, and types', (expected) => {
    const fixture = liveSeatMapFixtures.find((candidate) => candidate.key === expected.key);
    expect(fixture).toBeDefined();
    if (!fixture) return;

    const seats = fixture.seatMap.layout?.seats ?? [];
    const labels = seats.map((seat) => seat.label);
    expect(labels).toHaveLength(expected.seats);
    expect(new Set(labels).size).toBe(expected.seats);
    expect(seats.every((seat) => (
      seat.x >= 0 && seat.x <= 1 && seat.y >= 0 && seat.y <= 1
    ))).toBe(true);

    const types = Object.fromEntries(
      [...new Set(seats.map((seat) => seat.type))]
        .map((type) => [type, seats.filter((seat) => seat.type === type).length]),
    );
    expect(types).toEqual(expected.types);
  });
});
