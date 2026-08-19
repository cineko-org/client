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
      <PageHeader title="모니터" description="영화별 조건과 실행 상태를 관리합니다." actions={<PrimaryButton onClick={onNew}>새 모니터</PrimaryButton>} />
      <MonitorListView {...monitors} />
      <ReservationListView {...reservations} />
    </Stack>
  );
}
