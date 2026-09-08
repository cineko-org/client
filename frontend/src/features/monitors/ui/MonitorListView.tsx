import { Group, Modal, Stack, Text } from '@mantine/core';
import { DangerButton, PrimaryButton, SecondaryButton } from '../../../components/core/Actions';
import { EmptyState, Section } from '../../../components/core/Section';
import { StatusIndicator } from '../../../components/core/StatusIndicator';
import type { Monitor } from '../../../api/proto';
import { monitorMovie, monitorStatus } from '../../../api/resources';
import { monitorBookingLabel, monitorScheduleLabel, monitorTimeLabel, monitorWatchLabel } from '../model';
import { monitorPresentation, type MonitoringRuntime } from '../runtime';

export interface MonitorListViewProps {
  runtime?: MonitoringRuntime;
  monitors: Monitor[];
  deleteId: string | null;
  mutationId: string | null;
  onRetry: (id: string) => void;
  onStop: (id: string) => void;
  onToggleCancellationWatch?: (id: string) => void;
  onDeleteRequest: (id: string | null) => void;
  onDelete: () => void;
  onOpen: (id: string) => void;
  onEdit: (id: string) => void;
}

function executionDescription(monitor: Monitor): string {
	const status = monitorStatus(monitor);
	if (status === 'triggered') return '결제 화면을 열어 두었습니다 · 최대 15분 유지';
	if (status === 'payment_unknown') return 'CGV 예매 내역을 확인해야 합니다 · 자동 재실행 안 함';
	if (status === 'failed') return '최근 실행에서 오류가 발생했습니다 · 다시 찾을 수 있습니다';
	if (status === 'stopped') return '켜면 감시를 시작합니다';
  return `${monitorTimeLabel(monitor)} · 결제 전까지 진행`;
}

function retryLabel(monitor: Monitor): string {
	const status = monitorStatus(monitor);
	if (status === 'payment_unknown') return '확인 후 다시 찾기';
	if (status === 'triggered') return '다시 찾기';
	if (status === 'stopped') return '켜기';
  return '다시 찾기';
}

export function MonitorListView({ runtime, monitors, deleteId, mutationId, onRetry, onStop, onToggleCancellationWatch, onDeleteRequest, onDelete, onOpen, onEdit }: MonitorListViewProps) {
  return (
    <Section title="예매 찾기 목록" actions={<Text size="xs" c="dimmed">{monitors.length}개</Text>} subtle>
      {monitors.length === 0 ? <EmptyState>저장된 예매 찾기가 없습니다.</EmptyState> : (
        <Stack gap="xs">
          {monitors.map((monitor) => {
			const status = monitorStatus(monitor);
			const active = status === 'pending' || status === 'running';
			const presentation = monitorPresentation(monitor, runtime);
			const paymentBlocked = status === 'triggered' || status === 'payment_unknown';
			const retryable = paymentBlocked || status === 'failed' || status === 'stopped';
            const mutationLocked = Boolean(mutationId);
            const mutating = mutationId === monitor.id;
            return (
              <Stack key={monitor.id} gap="xs" bg="dark.6" p="md">
                <Group justify="space-between">
				  <Text fw={600}>{monitorMovie(monitor)}</Text>
				  <StatusIndicator label={presentation.label} color={presentation.color} processing={presentation.active} />
                </Group>
				<Stack gap={2}>
				  <Text size="sm" c="dimmed">{monitorBookingLabel(monitor)}</Text>
				  <Text size="sm" c="dimmed">{monitorWatchLabel(monitor)} · {monitorScheduleLabel(monitor)}</Text>
                  <Text size="sm" c={presentation.reason ? 'orange.4' : 'dimmed'}>{presentation.reason || executionDescription(monitor)}</Text>
                </Stack>
                <Group gap="xs">
                  <SecondaryButton size="xs" onClick={() => onOpen(monitor.id)}>상세</SecondaryButton>
                  <SecondaryButton size="xs" onClick={() => onEdit(monitor.id)} disabled={mutationLocked || active || paymentBlocked}>편집</SecondaryButton>
				  <SecondaryButton
					size="xs"
					disabled={mutationLocked || paymentBlocked}
					onClick={() => onToggleCancellationWatch?.(monitor.id)}
				  >
					{monitor.watchCancellationSeats ? '취소표 제외' : '취소표 포함'}
				  </SecondaryButton>
				  {retryable ? (
                    <PrimaryButton size="xs" loading={mutating} disabled={!presentation.canStart || (mutationLocked && !mutating)} onClick={() => onRetry(monitor.id)}>
                      {retryLabel(monitor)}
                    </PrimaryButton>
                  ) : null}
				  {active ? (
					<SecondaryButton size="xs" loading={mutating} disabled={mutationLocked && !mutating} onClick={() => onStop(monitor.id)}>
					  끄기
					</SecondaryButton>
				  ) : null}
                  <DangerButton size="xs" disabled={mutationLocked} onClick={() => onDeleteRequest(monitor.id)}>삭제</DangerButton>
                </Group>
              </Stack>
            );
          })}
        </Stack>
      )}
      <Modal opened={Boolean(deleteId)} onClose={() => onDeleteRequest(null)} title="예매 찾기 삭제">
        <Stack gap="lg"><Text size="sm">이 예매 찾기를 삭제할까요?</Text><Group justify="flex-end"><SecondaryButton disabled={Boolean(mutationId)} onClick={() => onDeleteRequest(null)}>취소</SecondaryButton><PrimaryButton color="red" loading={Boolean(deleteId && mutationId === deleteId)} disabled={Boolean(mutationId && mutationId !== deleteId)} onClick={onDelete}>삭제</PrimaryButton></Group></Stack>
      </Modal>
    </Section>
  );
}
