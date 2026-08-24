import { Box, Group, Stack, Text, Tooltip, UnstyledButton } from '@mantine/core';
import { useEffect, useMemo, useRef, type PointerEvent as ReactPointerEvent } from 'react';
import { SecondaryButton } from '../../../components/core/Actions';
import { EmptyState } from '../../../components/core/Section';
import type { Seat, Snapshot } from '../../../api/proto';
import { seatPresentation } from '../model';

export interface SeatMapViewProps {
  seatMap: Snapshot | null;
  pickedSeats: string[];
  onToggleSeat: (label: string) => void;
  onClear: () => void;
  auditoriumName?: string;
  reportedCapacity?: number;
  layoutAspectRatio?: number;
  seatSizeRatio?: number;
  emptyMessage?: string;
}

function median(values: number[]): number {
  if (values.length === 0) return 0;
  const sorted: number[] = [];
  for (const value of values) {
    const insertionIndex = sorted.findIndex((candidate) => candidate > value);
    if (insertionIndex === -1) sorted.push(value);
    else sorted.splice(insertionIndex, 0, value);
  }
  return sorted[Math.floor(sorted.length / 2)];
}

function positiveGaps(values: number[]): number[] {
  const sorted: number[] = [];
  for (const value of new Set(values)) {
    const insertionIndex = sorted.findIndex((candidate) => candidate > value);
    if (insertionIndex === -1) sorted.push(value);
    else sorted.splice(insertionIndex, 0, value);
  }
  return sorted.slice(1).map((value, index) => value - sorted[index]).filter((gap) => gap > 0.0001);
}

interface AxisLabel {
  label: string;
  position: number;
}

function layoutAxes(seats: Seat[]): { rows: AxisLabel[]; columns: AxisLabel[] } {
  const rowPositions = new Map<string, number[]>();
  const columnPositions = new Map<number, number[]>();
  for (const seat of seats) {
    rowPositions.set(seat.row, [...(rowPositions.get(seat.row) ?? []), seat.y]);
    columnPositions.set(seat.number, [...(columnPositions.get(seat.number) ?? []), seat.x]);
  }
  const rows = [...rowPositions].map(([label, positions]) => ({
      label,
      position: median(positions),
    }));
  const columns = [...columnPositions].map(([label, positions]) => ({
      label: String(label),
      position: median(positions),
    }));
  rows.sort((left, right) => left.position - right.position);
  columns.sort((left, right) => left.position - right.position);
  return {
    rows,
    columns,
  };
}

function ColumnAxis({ labels }: { labels: AxisLabel[] }) {
  return (
    <Box pos="relative" h={16} aria-hidden="true">
      {labels.map((item) => (
        <Text
          key={item.label}
          component="span"
          c="dimmed"
          pos="absolute"
          style={{
            left: `${item.position * 100}%`,
            top: '50%',
            transform: 'translate(-50%, -50%)',
            fontSize: 'clamp(6px, 0.65vw, 9px)',
            fontVariantNumeric: 'tabular-nums',
            lineHeight: 1,
          }}
        >
          {item.label}
        </Text>
      ))}
    </Box>
  );
}

function RowAxis({ labels }: { labels: AxisLabel[] }) {
  return (
    <Box pos="relative" h="100%" aria-hidden="true">
      {labels.map((item) => (
        <Text
          key={item.label}
          component="span"
          c="dimmed"
          fw={600}
          pos="absolute"
          style={{
            top: `${item.position * 100}%`,
            left: '50%',
            transform: 'translate(-50%, -50%)',
            fontSize: 'clamp(8px, 0.8vw, 11px)',
            lineHeight: 1,
          }}
        >
          {item.label}
        </Text>
      ))}
    </Box>
  );
}

function layoutMetrics(seats: Seat[], requestedAspectRatio?: number, requestedSeatSizeRatio?: number) {
  const rows = new Map<string, Seat[]>();
  for (const seat of seats) rows.set(seat.row, [...(rows.get(seat.row) ?? []), seat]);
  const horizontalGaps = [...rows.values()].flatMap((row) => positiveGaps(row.map((seat) => seat.x)));
  const verticalGaps = positiveGaps([...rows.values()].map((row) => median(row.map((seat) => seat.y))));
  const horizontalStep = median(horizontalGaps) || 0.04;
  const estimatedAspectRatio = verticalGaps.length > 0 ? median(verticalGaps) / horizontalStep : 1.6;
  return {
    aspectRatio: Math.min(3.2, Math.max(1.2, requestedAspectRatio ?? estimatedAspectRatio)),
    seatWidth: `${Math.min(5.5, Math.max(1.05, (requestedSeatSizeRatio ?? horizontalStep * 0.82) * 100))}%`,
    showLabels: seats.length <= 220 && horizontalStep >= 0.035,
  };
}

function seatBackground(seat: Seat, selected: boolean): string {
  if (selected) return 'var(--mantine-color-cineko-5)';
  const color = seatPresentation(seat.type).color;
  return `linear-gradient(135deg, ${color} 0 44%, var(--mantine-color-dark-8) 45% 55%, ${color} 56% 100%)`;
}

const emptySeats: Seat[] = [];

interface DragSelection {
  select: boolean;
  selected: Set<string>;
  visited: Set<string>;
}

export function SeatMapView({
  seatMap, pickedSeats, onToggleSeat, onClear, auditoriumName, reportedCapacity, layoutAspectRatio,
  seatSizeRatio, emptyMessage = '상영관을 선택하면 좌석 배치를 불러옵니다.',
}: SeatMapViewProps) {
  const picked = new Set(pickedSeats);
  const dragSelection = useRef<DragSelection | null>(null);
  const seats = seatMap?.layout?.seats ?? emptySeats;
  const metrics = useMemo(
    () => layoutMetrics(seats, layoutAspectRatio, seatSizeRatio),
    [layoutAspectRatio, seatSizeRatio, seats],
  );
  const axes = useMemo(() => layoutAxes(seats), [seats]);
  const visibleTypes = useMemo(
    () => [...new Set(seats.map((seat) => seat.type))],
    [seats],
  );
  const capacityDiffers = Boolean(
    seatMap && reportedCapacity && reportedCapacity !== seats.length,
  );
  useEffect(() => {
    const endSelection = () => {
      dragSelection.current = null;
    };
    window.addEventListener('pointerup', endSelection);
    window.addEventListener('pointercancel', endSelection);
    window.addEventListener('blur', endSelection);
    return () => {
      window.removeEventListener('pointerup', endSelection);
      window.removeEventListener('pointercancel', endSelection);
      window.removeEventListener('blur', endSelection);
    };
  }, []);

  const applyDragSelection = (label: string) => {
    const selection = dragSelection.current;
    if (!selection || selection.visited.has(label)) return;
    selection.visited.add(label);
    const selected = selection.selected.has(label);
    if (selected === selection.select) return;
    onToggleSeat(label);
    if (selection.select) selection.selected.add(label);
    else selection.selected.delete(label);
  };

  const startDragSelection = (event: ReactPointerEvent<HTMLButtonElement>, label: string) => {
    if (event.button !== 0) return;
    event.preventDefault();
    const selected = new Set(pickedSeats);
    dragSelection.current = {
      select: !selected.has(label),
      selected,
      visited: new Set<string>(),
    };
    applyDragSelection(label);
  };

  const continueDragSelection = (event: ReactPointerEvent<HTMLButtonElement>, label: string) => {
    if ((event.buttons & 1) === 0) return;
    applyDragSelection(label);
  };

  return (
    <Stack gap="md">
      <Group justify="space-between" align="flex-end" wrap="wrap">
        <Stack gap={2}>
          <Text fw={600}>{auditoriumName || '좌석 배치'}</Text>
          {seatMap ? (
            <Text size="xs" c="dimmed">
              배치 {seats.length}석{capacityDiffers ? ` · 회차 정원 ${reportedCapacity}석` : ''}
            </Text>
          ) : null}
        </Stack>
        {seatMap ? (
          <Group gap="md">
            {visibleTypes.map((type) => {
              const presentation = seatPresentation(type);
              return <Group key={type} gap={6}><Box w={8} h={8} bg={presentation.color} /><Text size="xs" c="dimmed">{presentation.label}</Text></Group>;
            })}
          </Group>
        ) : null}
      </Group>
      <Box bg="dark.9" p={{ base: 'sm', md: 'lg' }}>
        <Stack gap="lg">
          <Stack gap={4} align="center">
            <Text size="xs" c="dimmed">screen</Text>
            <Box w="72%" h={16} style={{ borderTop: '2px solid var(--mantine-color-orange-8)', borderRadius: '50% 50% 0 0' }} />
          </Stack>
          {!seatMap ? <EmptyState>{emptyMessage}</EmptyState> : (
            <Box
              w="100%"
              style={{
                display: 'grid',
                gridTemplateColumns: '18px minmax(0, 1fr) 18px',
                gridTemplateRows: '16px auto 16px',
                columnGap: 4,
                rowGap: 4,
              }}
            >
              <Box style={{ gridColumn: 2, gridRow: 1 }}><ColumnAxis labels={axes.columns} /></Box>
              <Box style={{ gridColumn: 1, gridRow: 2 }}><RowAxis labels={axes.rows} /></Box>
              <Box
                pos="relative"
                w="100%"
                style={{
                  gridColumn: 2,
                  gridRow: 2,
                  aspectRatio: metrics.aspectRatio,
                  touchAction: 'none',
                  userSelect: 'none',
                }}
              >
                {seats.map((seat) => {
                  const title = `${seat.label} · ${seat.zoneName || '존 미지정'} · ${seat.saleFormName || seat.type}`;
                  return (
                    <Tooltip key={seat.id} label={title} openDelay={250}>
                      <UnstyledButton
                        aria-label={title}
                        aria-pressed={picked.has(seat.label)}
                        data-seat-label={seat.label}
                        onClick={(event) => {
                          if (event.detail === 0) onToggleSeat(seat.label);
                        }}
                        onPointerDown={(event) => startDragSelection(event, seat.label)}
                        onPointerEnter={(event) => continueDragSelection(event, seat.label)}
                        pos="absolute"
                        style={{
                          left: `${seat.x * 100}%`,
                          top: `${seat.y * 100}%`,
                          width: metrics.seatWidth,
                          aspectRatio: '1',
                          transform: 'translate(-50%, -50%)',
                          background: seatBackground(seat, picked.has(seat.label)),
                          boxShadow: 'inset 0 0 0 1px var(--mantine-color-dark-9)',
                          color: 'var(--mantine-color-white)',
                          fontSize: metrics.showLabels ? 'clamp(7px, 0.65vw, 10px)' : 0,
                          lineHeight: 1,
                          overflow: 'hidden',
                          cursor: 'crosshair',
                        }}
                      >{metrics.showLabels ? seat.label : null}</UnstyledButton>
                    </Tooltip>
                  );
                })}
              </Box>
              <Box style={{ gridColumn: 3, gridRow: 2 }}><RowAxis labels={axes.rows} /></Box>
              <Box style={{ gridColumn: 2, gridRow: 3 }}><ColumnAxis labels={axes.columns} /></Box>
            </Box>
          )}
        </Stack>
      </Box>
      <Group justify="space-between" align="flex-start" wrap="wrap">
        <Stack gap="xs" style={{ flex: 1 }}>
          <Group gap="xs"><Text size="sm" fw={600}>선택 좌석</Text><Text size="xs" c="dimmed">{pickedSeats.length ? `${pickedSeats.length}석 · 선택 순서대로 우선` : '선택하지 않으면 전체 좌석에서 자동 선택'}</Text></Group>
          <Group gap={6}>
            {pickedSeats.length === 0 ? <Text size="sm" c="dimmed">클릭하거나 누른 채로 좌석을 훑어 후보를 선택하세요.</Text> : pickedSeats.map((label, index) => (
              <SecondaryButton key={label} size="compact-xs" onClick={() => onToggleSeat(label)}>{index + 1}. {label}</SecondaryButton>
            ))}
          </Group>
        </Stack>
        <SecondaryButton size="xs" onClick={onClear} disabled={pickedSeats.length === 0}>선택 비우기</SecondaryButton>
      </Group>
    </Stack>
  );
}
