import { Stack } from '@mantine/core';
import { PrimaryButton } from '../../components/core/Actions';
import { PageHeader } from '../../components/core/PageHeader';
import { MonitorListView, type MonitorListViewProps } from '../../features/monitors/ui/MonitorListView';
import { ReservationListView, type ReservationListViewProps } from '../../features/reservations/ui/ReservationListView';

interface MonitorsPageProps {
  monitors: MonitorListViewProps;
  reservations: ReservationListViewProps;
  onNew: () => void;
}

export function MonitorsPage({ monitors, reservations, onNew }: MonitorsPageProps) {
  return (
    <Stack gap="xl">
      <PageHeader title="예매 찾기" description="원하는 영화·일정·좌석을 찾고 실행 상태를 관리합니다." actions={<PrimaryButton onClick={onNew}>예매 찾기 시작</PrimaryButton>} />
      <MonitorListView {...monitors} />
      <ReservationListView {...reservations} />
    </Stack>
  );
}
