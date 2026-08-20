import { Box, Group, Stack, Text, Tooltip, UnstyledButton } from '@mantine/core';
import { useMemo } from 'react';
import { SecondaryButton } from '../../../components/core/Actions';
import { EmptyState } from '../../../components/core/Section';
import type { Seat, SeatMap } from '../../../api/types';
import { seatTypePresentation } from '../model';

export interface SeatMapViewProps {
  seatMap: SeatMap | null;
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
  const color = seatTypePresentation[seat.type].color;
  return `linear-gradient(135deg, ${color} 0 44%, var(--mantine-color-dark-8) 45% 55%, ${color} 56% 100%)`;
}

export function SeatMapView({
  seatMap, pickedSeats, onToggleSeat, onClear, auditoriumName, reportedCapacity, layoutAspectRatio,
  seatSizeRatio, emptyMessage = '상영관을 선택하면 좌석 배치를 불러옵니다.',
}: SeatMapViewProps) {
  const picked = new Set(pickedSeats);
  const metrics = useMemo(
    () => layoutMetrics(seatMap?.seats ?? [], layoutAspectRatio, seatSizeRatio),
    [layoutAspectRatio, seatMap, seatSizeRatio],
  );
  const visibleTypes = useMemo(
    () => [...new Set((seatMap?.seats ?? []).map((seat) => seat.type))],
    [seatMap],
  );
  const capacityDiffers = Boolean(
    seatMap && reportedCapacity && reportedCapacity !== seatMap.seats.length,
  );
  return (
    <Stack gap="md">
      <Group justify="space-between" align="flex-end" wrap="wrap">
        <Stack gap={2}>
          <Text fw={600}>{auditoriumName || '좌석 배치'}</Text>
          {seatMap ? (
            <Text size="xs" c="dimmed">
              배치 {seatMap.seats.length}석{capacityDiffers ? ` · 회차 정원 ${reportedCapacity}석` : ''}
            </Text>
          ) : null}
        </Stack>
        {seatMap ? (
          <Group gap="md">
            {visibleTypes.map((type) => {
              const presentation = seatTypePresentation[type];
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
            <Box pos="relative" w="100%" style={{ aspectRatio: metrics.aspectRatio }}>
              {seatMap.seats.map((seat) => {
                const title = `${seat.label} · ${seat.zoneName || '존 미지정'} · ${seat.saleFormName || seat.type}`;
                return (
                  <Tooltip key={seat.id} label={title} openDelay={250}>
                    <UnstyledButton
                      aria-label={title}
                      onClick={() => onToggleSeat(seat.label)}
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
                      }}
                    >{metrics.showLabels ? seat.label : null}</UnstyledButton>
                  </Tooltip>
                );
              })}
            </Box>
          )}
        </Stack>
      </Box>
      <Group justify="space-between" align="flex-start" wrap="wrap">
        <Stack gap="xs" style={{ flex: 1 }}>
          <Group gap="xs"><Text size="sm" fw={600}>선택 좌석</Text><Text size="xs" c="dimmed">{pickedSeats.length ? `${pickedSeats.length}석 · 선택 순서대로 우선` : '선택하지 않으면 전체 좌석에서 자동 선택'}</Text></Group>
          <Group gap={6}>
            {pickedSeats.length === 0 ? <Text size="sm" c="dimmed">특정 좌석을 우선하고 싶을 때만 선택하세요.</Text> : pickedSeats.map((label, index) => (
              <SecondaryButton key={label} size="compact-xs" onClick={() => onToggleSeat(label)}>{index + 1}. {label}</SecondaryButton>
            ))}
          </Group>
        </Stack>
        <SecondaryButton size="xs" onClick={onClear} disabled={pickedSeats.length === 0}>선택 비우기</SecondaryButton>
      </Group>
    </Stack>
  );
}
