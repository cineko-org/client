import { Group, Stack, Text } from '@mantine/core';
import { PrimaryButton, SecondaryButton } from '../components/core/Actions';
import { Columns } from '../components/core/Columns';
import { Metric } from '../components/core/Metric';
import { PageHeader } from '../components/core/PageHeader';

export interface HomePageProps {
  canStart?: boolean;
  runtimeReason?: string;
  monitors: number;
  runningMonitors: number;
  presets: number;
  reservations: number;
  onMonitors: () => void;
  onNewMonitor: () => void;
}

export function HomePage(props: HomePageProps) {
  return (
    <Stack gap="xl">
      <PageHeader
        title="예매 현황"
        description="저장된 조건과 현재 실행 상태를 확인합니다."
        actions={<Group gap="xs"><SecondaryButton onClick={props.onMonitors}>예매 찾기</SecondaryButton><PrimaryButton disabled={props.canStart === false} onClick={props.onNewMonitor}>예매 찾기 시작</PrimaryButton></Group>}
      />
      {props.runtimeReason ? <Text c="orange.4">{props.runtimeReason}</Text> : null}
      <Columns>
        <Metric label="감시 중" value={props.runningMonitors} detail={`저장된 예매 찾기 ${props.monitors}개`} color={props.runningMonitors > 0 ? 'blue' : 'gray'} processing={props.runningMonitors > 0} />
        <Metric label="좌석 프리셋" value={props.presets} detail="상영관·후보 좌석" color="violet" />
        <Metric label="예약" value={props.reservations} detail="완료된 예약" color="green" />
      </Columns>
    </Stack>
  );
}
