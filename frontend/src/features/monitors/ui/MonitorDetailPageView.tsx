import { Group, Stack, Text } from '@mantine/core';
import { PrimaryButton, SecondaryButton } from '../../../components/core/Actions';
import { Columns } from '../../../components/core/Columns';
import { Metric } from '../../../components/core/Metric';
import { PageHeader } from '../../../components/core/PageHeader';
import { EmptyState, Section } from '../../../components/core/Section';
import type { Monitor } from '../../../api/proto';
import { monitorMovie, monitorStatus } from '../../../api/resources';
import { monitorBookingLabel, monitorScheduleLabel, monitorStatusLabel, monitorTimeLabel, monitorWatchLabel } from '../model';

interface MonitorDetailPageViewProps {
  monitor?: Monitor;
  mutating: boolean;
  onBack: () => void;
  onEdit: () => void;
  onRetry: () => void;
  onStop: () => void;
  onToggleCancellationWatch?: () => void;
}

export function MonitorDetailPageView({ monitor, mutating, onBack, onEdit, onRetry, onStop, onToggleCancellationWatch }: MonitorDetailPageViewProps) {
  if (!monitor) {
    return (
      <Stack gap="xl">
        <PageHeader title="예매 찾기 상세" actions={<SecondaryButton onClick={onBack}>목록</SecondaryButton>} />
        <EmptyState>예매 찾기를 찾지 못했습니다.</EmptyState>
      </Stack>
    );
  }
	const status = monitorStatus(monitor);
	const awaitingPayment = status === 'triggered';
	const paymentUnknown = status === 'payment_unknown';
	const active = status === 'pending' || status === 'running';
	const retryable = awaitingPayment || paymentUnknown || status === 'failed' || status === 'stopped';
  const retryLabel = status === 'stopped' ? '켜기' : paymentUnknown ? '확인 후 다시 찾기' : '다시 찾기';
  return (
    <Stack gap="xl">
      <PageHeader
		 title={monitorMovie(monitor)}
		description="예매 오픈부터 취소표까지 감시하는 모니터"
        actions={(
          <Group gap="xs">
            <SecondaryButton onClick={onBack}>목록</SecondaryButton>
			<SecondaryButton onClick={onEdit} disabled={mutating || active || awaitingPayment || paymentUnknown}>편집</SecondaryButton>
			<SecondaryButton onClick={onToggleCancellationWatch} disabled={!onToggleCancellationWatch || mutating || awaitingPayment || paymentUnknown}>
			  {monitor.watchCancellationSeats ? '취소표 끄기' : '취소표 켜기'}
			</SecondaryButton>
			{retryable
              ? <PrimaryButton loading={mutating} onClick={onRetry}>{retryLabel}</PrimaryButton>
              : null}
			{active ? <SecondaryButton loading={mutating} onClick={onStop}>끄기</SecondaryButton> : null}
          </Group>
        )}
      />
      <Columns>
        <Metric
          label="상태"
		  value={monitorStatusLabel(status)}
          detail={paymentUnknown
            ? 'CGV 예매 내역을 확인한 뒤 다시 실행하세요.'
            : awaitingPayment
            ? '결제 화면을 최대 15분 동안 유지합니다.'
			: monitor.updatedAt ? new Date(Number(monitor.updatedAt.seconds) * 1000).toLocaleString('ko-KR') : '업데이트 기록 없음'}
		  color={awaitingPayment || paymentUnknown ? 'orange' : active ? 'blue' : 'gray'}
		  processing={active}
        />
        <Metric label="진행 범위" value={paymentUnknown ? '결과 확인 필요' : awaitingPayment ? '결제 대기' : '결제 전까지'} detail={monitorTimeLabel(monitor)} color="orange" />
      </Columns>
      <Section title="관람 조건">
        <Stack gap="xs">
          <Text>{monitorBookingLabel(monitor)}</Text>
          <Text>{monitorWatchLabel(monitor)}</Text>
          <Text>{monitorScheduleLabel(monitor)}</Text>
          <Text size="sm" c="dimmed">선호 시간 · {monitorTimeLabel(monitor)}</Text>
          {awaitingPayment ? (
            <Text size="sm" c="orange.4" lh={1.55} style={{ wordBreak: 'keep-all', overflowWrap: 'anywhere' }}>
              열린 브라우저에서 결제를 마치세요. 다시 찾으려면 위의 버튼을 누르세요.
            </Text>
          ) : null}
          {paymentUnknown ? <Text size="sm" c="orange.4">중복 예매를 막기 위해 자동으로 다시 실행하지 않았습니다.</Text> : null}
			{monitor.state?.state.case === 'failed' ? <Text size="sm" c="red">최근 실행에서 오류가 발생했습니다.</Text> : null}
        </Stack>
      </Section>
    </Stack>
  );
}
