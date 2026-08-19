import { Group, Stack, Text } from '@mantine/core';
import { PrimaryButton, SecondaryButton } from '../../../components/core/Actions';
import { Columns } from '../../../components/core/Columns';
import { Metric } from '../../../components/core/Metric';
import { PageHeader } from '../../../components/core/PageHeader';
import { EmptyState, Section } from '../../../components/core/Section';
import type { Monitor } from '../../../api/types';
import { monitorIntervalLabel, monitorScheduleLabel, monitorStatusLabel, monitorTimeLabel } from '../model';

interface MonitorDetailPageViewProps {
  monitor?: Monitor;
  mutating: boolean;
  onBack: () => void;
  onEdit: () => void;
  onRetry: () => void;
}

export function MonitorDetailPageView({ monitor, mutating, onBack, onEdit, onRetry }: MonitorDetailPageViewProps) {
  if (!monitor) {
    return (
      <Stack gap="xl">
        <PageHeader title="모니터 상세" actions={<SecondaryButton onClick={onBack}>목록</SecondaryButton>} />
        <EmptyState>모니터를 찾지 못했습니다.</EmptyState>
      </Stack>
    );
  }
  const awaitingPayment = monitor.status === 'triggered';
  const paymentUnknown = monitor.status === 'payment_unknown';
  const running = monitor.status === 'running';
  const retryLabel = paymentUnknown ? '확인 후 다시 찾기' : '다시 찾기';
  return (
    <Stack gap="xl">
      <PageHeader
        title={monitor.movie}
        description={monitor.mode === 'cancellation' ? '취소표 모니터' : '예매 오픈 모니터'}
        actions={(
          <Group gap="xs">
            <SecondaryButton onClick={onBack}>목록</SecondaryButton>
            <SecondaryButton onClick={onEdit} disabled={mutating || running || awaitingPayment || paymentUnknown}>편집</SecondaryButton>
            {awaitingPayment || paymentUnknown
              ? <PrimaryButton loading={mutating} onClick={onRetry}>{retryLabel}</PrimaryButton>
              : null}
          </Group>
        )}
      />
      <Columns>
        <Metric
          label="상태"
          value={monitorStatusLabel(monitor.status)}
          detail={paymentUnknown
            ? 'CGV 예매 내역을 확인한 뒤 다시 실행하세요.'
            : awaitingPayment
            ? '결제 화면을 최대 15분 동안 유지합니다.'
            : monitor.updatedAt ? new Date(monitor.updatedAt).toLocaleString('ko-KR') : '업데이트 기록 없음'}
          color={awaitingPayment || paymentUnknown ? 'orange' : running ? 'blue' : 'gray'}
          processing={running}
        />
        <Metric label="확인 간격" value={monitorIntervalLabel(monitor)} detail="매회 범위 내 무작위" color="violet" />
        <Metric label="진행 범위" value={paymentUnknown ? '결과 확인 필요' : awaitingPayment ? '결제 대기' : '결제 전까지'} detail={monitorTimeLabel(monitor)} color="orange" />
      </Columns>
      <Section title="관람 조건">
        <Stack gap="xs">
          <Text>{monitorScheduleLabel(monitor)}</Text>
          <Text size="sm" c="dimmed">선호 시간 · {monitorTimeLabel(monitor)}</Text>
          {awaitingPayment ? <Text size="sm" c="orange.4">결제는 열린 브라우저에서 마치세요. 다시 찾으려면 위 버튼을 누르세요.</Text> : null}
          {paymentUnknown ? <Text size="sm" c="orange.4">중복 예매를 막기 위해 자동으로 다시 실행하지 않았습니다.</Text> : null}
          {monitor.lastError ? <Text size="sm" c="red">최근 실행에서 오류가 발생했습니다.</Text> : null}
        </Stack>
      </Section>
    </Stack>
  );
}
