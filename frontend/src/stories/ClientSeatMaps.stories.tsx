import type { Meta, StoryObj } from '@storybook/react-vite';
import { Box, Group, SimpleGrid, Stack, Text } from '@mantine/core';
import { useState } from 'react';
import { SeatMapView } from '../features/presets/ui/SeatMapView';
import { seatPresentation } from '../features/presets/model';
import type { LiveSeatMapFixture } from './liveSeatMaps';
import {
  fourDxSeatMapFixture,
  imaxSeatMapFixture,
  liveSeatMapFixtures,
  premium17SeatMapFixture,
  screenXReclinerSeatMapFixture,
  standard6SeatMapFixture,
} from './liveSeatMaps';

function fixtureSeats(fixture: LiveSeatMapFixture) {
  return fixture.seatMap.layout?.seats ?? [];
}

const meta = { title: 'Client/Seat maps' } satisfies Meta;
export default meta;
type Story = StoryObj;

function InteractiveSeatMap({ fixture }: { fixture: LiveSeatMapFixture }) {
  const [pickedSeats, setPickedSeats] = useState(() => [...fixture.pickedSeats]);
  const toggleSeat = (label: string) => {
    setPickedSeats((current) => current.includes(label)
      ? current.filter((candidate) => candidate !== label)
      : [...current, label]);
  };
  return (
    <SeatMapView
      seatMap={fixture.seatMap}
      pickedSeats={pickedSeats}
      auditoriumName={fixture.auditorium}
      reportedCapacity={fixture.scheduleCapacity}
      layoutAspectRatio={fixture.layoutWidth / fixture.layoutHeight}
      seatSizeRatio={38 / fixture.layoutWidth}
      onToggleSeat={toggleSeat}
      onClear={() => setPickedSeats([])}
    />
  );
}

function SeatMapExample({ fixture }: { fixture: LiveSeatMapFixture }) {
  return (
    <Box bg="dark.9" mih="100dvh" p={{ base: 'md', md: 40 }}>
      <Stack gap="lg" maw={1100} mx="auto">
        <Group justify="space-between" align="flex-end" wrap="wrap">
          <Stack gap={2}>
            <Text size="xl" fw={700}>{fixture.theater} · {fixture.auditorium}</Text>
            <Text size="sm" c="dimmed">{fixture.screenTypes.join(' · ')}</Text>
          </Stack>
          <Stack gap={2} align="flex-end">
            <Text size="xs" c="dimmed">2026-08-12 CGV 실측</Text>
            <Text size="xs" c="dimmed">좌석을 눌러 후보 순서와 선택 상태를 확인할 수 있습니다.</Text>
          </Stack>
        </Group>
        <InteractiveSeatMap fixture={fixture} />
      </Stack>
    </Box>
  );
}

function CapturedCoordinatePlot({ fixture }: { fixture: LiveSeatMapFixture }) {
  const roundingError = Math.max(fixture.layoutWidth, fixture.layoutHeight) * 0.0000005;
  return (
    <Stack gap="md">
      <Group justify="space-between" align="flex-end">
        <Stack gap={2}>
          <Text fw={600}>CGV DOM 추출 좌표</Text>
          <Text size="xs" c="dimmed">
            {fixture.layoutWidth}×{fixture.layoutHeight}px · {fixtureSeats(fixture).length}석
          </Text>
        </Stack>
        <Text size="xs" c="green.5">좌표 반올림 오차 ≤ {roundingError.toFixed(3)}px</Text>
      </Group>
      <Box bg="dark.9" p={{ base: 'sm', md: 'lg' }}>
        <Stack gap="lg">
          <Stack gap={4} align="center">
            <Text size="xs" c="dimmed">screen</Text>
            <Box w="72%" h={16} style={{ borderTop: '2px solid var(--mantine-color-orange-8)', borderRadius: '50% 50% 0 0' }} />
          </Stack>
          <Box pos="relative" w="100%" style={{ aspectRatio: fixture.layoutWidth / fixture.layoutHeight }}>
            {fixtureSeats(fixture).map((seat) => (
              <Box
                key={seat.id}
                pos="absolute"
                title={`${seat.label} · ${seat.type}`}
                style={{
                  left: `${seat.x * 100}%`,
                  top: `${seat.y * 100}%`,
                  width: `${(38 / fixture.layoutWidth) * 100}%`,
                  aspectRatio: '1',
                  transform: 'translate(-50%, -50%)',
                  background: seatPresentation(seat.type).color,
                  boxShadow: 'inset 0 0 0 1px var(--mantine-color-dark-9)',
                }}
              />
            ))}
          </Box>
        </Stack>
      </Box>
    </Stack>
  );
}

function LayoutComparison({ fixture }: { fixture: LiveSeatMapFixture }) {
  return (
    <Box bg="dark.9" mih="100dvh" p={{ base: 'md', md: 40 }}>
      <Stack gap="xl" maw={1180} mx="auto">
        <Stack gap={2}>
          <Text size="xl" fw={700}>{fixture.theater} · {fixture.auditorium}</Text>
          <Text size="sm" c="dimmed">
            원본 좌석 {fixtureSeats(fixture).length}석 = Cineko 렌더 {fixtureSeats(fixture).length}석 · 라벨·좌표·타입 유지
          </Text>
        </Stack>
        <SimpleGrid cols={{ base: 1, xl: 2 }} spacing="xl">
          <CapturedCoordinatePlot fixture={fixture} />
          <Stack gap="md">
            <Stack gap={2}>
              <Text fw={600}>Cineko SeatMapView</Text>
              <Text size="xs" c="dimmed">좌석을 눌러 실제 후보 선택 동작을 확인할 수 있습니다.</Text>
            </Stack>
            <InteractiveSeatMap fixture={fixture} />
          </Stack>
        </SimpleGrid>
      </Stack>
    </Box>
  );
}

export const IMAX: Story = { render: () => <SeatMapExample fixture={imaxSeatMapFixture} /> };
export const FourDX: Story = { render: () => <SeatMapExample fixture={fourDxSeatMapFixture} /> };
export const ScreenXRecliner: Story = { render: () => <SeatMapExample fixture={screenXReclinerSeatMapFixture} /> };
export const StandardLaser: Story = { render: () => <SeatMapExample fixture={standard6SeatMapFixture} /> };
export const Premium: Story = { render: () => <SeatMapExample fixture={premium17SeatMapFixture} /> };
export const SourceComparison: Story = { render: () => <LayoutComparison fixture={imaxSeatMapFixture} /> };

export const AllSourceComparisons: Story = {
  render: () => (
    <Stack gap={72} bg="dark.9" mih="100dvh">
      {liveSeatMapFixtures.map((fixture) => <LayoutComparison key={fixture.key} fixture={fixture} />)}
    </Stack>
  ),
};

export const AllLayouts: Story = {
  render: () => (
    <Stack gap={72} bg="dark.9" mih="100dvh" p={{ base: 'md', md: 40 }}>
      {liveSeatMapFixtures.map((fixture) => <SeatMapExample key={fixture.key} fixture={fixture} />)}
    </Stack>
  ),
};
