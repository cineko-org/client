import type { Meta, StoryObj } from '@storybook/react-vite';
import { Box, Divider, Group, Stack, Text } from '@mantine/core';
import { PrimaryButton, SecondaryButton, DangerButton, IconAction } from '../components/core/Actions';
import { Columns } from '../components/core/Columns';
import { SelectField, TextAreaField, TextField } from '../components/core/Fields';
import { Metric } from '../components/core/Metric';
import { EmptyState, Section } from '../components/core/Section';
import { StatusIndicator } from '../components/core/StatusIndicator';
import { SeatMapView } from '../features/presets/ui/SeatMapView';
import { ReservationListView } from '../features/reservations/ui/ReservationListView';
import { ScheduleView } from '../features/monitors/ui/ScheduleView';
import { initialMonitorForm } from '../features/monitors/model';
import { AccountSettingsView } from '../features/settings/ui/AccountSettingsView';
import { HookSettingsView } from '../features/settings/ui/HookSettingsView';
import { ProxySettingsView } from '../features/settings/ui/ProxySettingsView';
import { IconBell } from '@tabler/icons-react';
import { authenticatedAccount, noop, proxyNetwork, reservations, seatMap } from './fixtures';
import { imaxSeatMapFixture } from './liveSeatMaps';

const meta = { title: 'Client/Components' } satisfies Meta;
export default meta;
type Story = StoryObj;
const Canvas = ({ children }: { children: React.ReactNode }) => <Box bg="dark.9" mih="100dvh" p={{ base: 'md', md: 48 }}>{children}</Box>;

export const Actions: Story = {
  render: () => <Canvas><Group><PrimaryButton>저장</PrimaryButton><SecondaryButton>취소</SecondaryButton><DangerButton>삭제</DangerButton><PrimaryButton loading>저장 중</PrimaryButton><PrimaryButton disabled>사용 불가</PrimaryButton><IconAction label="알림" icon={<IconBell size={19} />} onClick={noop} /></Group></Canvas>,
};

export const StatusAndMetrics: Story = {
  render: () => <Canvas><Stack gap="xl"><Group><StatusIndicator label="프록시" color="green" /><StatusIndicator label="CGV" color="gray" muted /><StatusIndicator label="실행 중" color="blue" processing /></Group><Columns><Metric label="실행 중" value={2} detail="찾고 있는 예매 4개" color="blue" processing /><Metric label="좌석 프리셋" value={5} detail="상영관·후보 좌석" color="violet" /><Metric label="예약" value={1} detail="완료된 예약" color="green" /></Columns></Stack></Canvas>,
};

export const Fields: Story = {
  render: () => <Canvas><Stack maw={760} gap="md"><TextField label="이름" value="용산 IMAX 중앙 2석" onChange={noop} /><SelectField label="연결 방식" data={['사용 안 함', 'HTTP(S) / SOCKS5']} value="HTTP(S) / SOCKS5" onChange={noop} /><TextAreaField label="프록시 주소" value={'socks5://127.0.0.1:1080\nhttps://proxy.example:8443'} onChange={noop} /></Stack></Canvas>,
};

export const Sections: Story = {
  render: () => <Canvas><Stack gap="xl"><Section title="관람 조건" description="영화와 좌석 조건을 확인합니다."><Text>2026-08-20 · 18:00–23:30</Text></Section><Divider /><Section title="빈 상태"><EmptyState>등록된 항목이 없습니다.</EmptyState></Section></Stack></Canvas>,
};

export const SeatMap: Story = {
  render: () => <Canvas><Box maw={900}><SeatMapView seatMap={seatMap} pickedSeats={imaxSeatMapFixture.pickedSeats} auditoriumName={imaxSeatMapFixture.auditorium} reportedCapacity={imaxSeatMapFixture.scheduleCapacity} layoutAspectRatio={imaxSeatMapFixture.layoutWidth / imaxSeatMapFixture.layoutHeight} seatSizeRatio={38 / imaxSeatMapFixture.layoutWidth} onToggleSeat={noop} onClear={noop} /></Box></Canvas>,
};

export const EmptySeatMap: Story = {
  render: () => <Canvas><Box maw={900}><SeatMapView seatMap={null} pickedSeats={[]} onToggleSeat={noop} onClear={noop} /></Box></Canvas>,
};

export const Reservations: Story = {
  render: () => <Canvas><ReservationListView reservations={reservations} cancelId={null} cancelling={false} onReviewCancellation={noop} onCancelRequest={noop} onConfirmCancellation={noop} /></Canvas>,
};

export const OpeningSchedule: Story = {
  render: () => <Canvas><Box maw={900}><ScheduleView form={{ ...initialMonitorForm, weekdays: ['5', '6'], earliestTime: '18:00', latestTime: '23:30' }} onChange={noop} /></Box></Canvas>,
};

export const EveningSchedule: Story = {
  render: () => <Canvas><Box maw={900}><ScheduleView form={{ ...initialMonitorForm, weekdays: ['4'], earliestTime: '19:00' }} onChange={noop} /></Box></Canvas>,
};

export const AccountAndProxy: Story = {
  render: () => <Canvas><Stack maw={760} gap="xl"><AccountSettingsView account={authenticatedAccount} onAuthenticate={noop} /><Divider /><ProxySettingsView available settings={proxyNetwork} form={{ mode: 'proxy', proxyUrls: 'socks5://127.0.0.1:1080', proxyUsername: '', proxyPassword: '' }} loadState="ready" saving={false} onChange={noop} onReload={noop} onSave={noop} /></Stack></Canvas>,
};

export const ExternalNotifications: Story = {
  render: () => <Canvas><Box maw={760}><HookSettingsView available loadState="ready" saving={false}
    forms={[{
      id: 'slack', name: '팀 예매 알림', kind: 'slack',
      url: 'https://hooks.slack.com/services/…', secret: '', eventKinds: [], enabled: true,
    }]}
    onAdd={noop} onReload={noop} onChange={noop} onRemove={noop} onSave={noop}
  /></Box></Canvas>,
};
