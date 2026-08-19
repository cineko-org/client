import { Group, Modal, Stack, Text } from '@mantine/core';
import { DangerButton, PrimaryButton, SecondaryButton } from '../../../components/core/Actions';
import { EmptyState, Section } from '../../../components/core/Section';
import { StatusIndicator } from '../../../components/core/StatusIndicator';
import type { Reservation } from '../../../api/types';
import { reservationReference, reservationStatusLabel } from '../model';

export interface ReservationListViewProps {
  reservations: Reservation[];
  cancelId: string | null;
  cancelling: boolean;
  onReviewCancellation: (id: string) => void;
  onCancelRequest: (id: string | null) => void;
  onConfirmCancellation: () => void;
}

export function ReservationListView(props: ReservationListViewProps) {
  const { reservations, cancelId, cancelling, onReviewCancellation, onCancelRequest, onConfirmCancellation } = props;
  return (
    <Section title="예약·취소" actions={<Text size="xs" c="dimmed">{reservations.length}건</Text>} subtle>
      {reservations.length === 0 ? <EmptyState>예약 기록이 없습니다.</EmptyState> : (
        <Stack gap="xs">
          {reservations.map((reservation) => (
            <Stack key={reservation.id} gap="xs" bg="dark.6" p="md">
              <Group justify="space-between"><Text fw={600}>{reservation.draft?.showtime?.movie || '예약'}</Text><StatusIndicator label={reservationStatusLabel(reservation.status)} color={reservation.status === 'booked' ? 'green' : reservation.status === 'cancelled' ? 'yellow' : 'gray'} /></Group>
              <Stack gap={2}><Text size="sm" c="dimmed">{reservation.draft?.seatLabels?.join(' · ') || '좌석 준비 중'}</Text><Text size="sm" c="dimmed">{reservationReference(reservation.status, reservation.bookingNumber)}</Text></Stack>
              {reservation.status === 'booked' ? <Group gap="xs"><SecondaryButton size="xs" onClick={() => onReviewCancellation(reservation.id)}>취소 검토</SecondaryButton><DangerButton size="xs" onClick={() => onCancelRequest(reservation.id)}>실제 취소</DangerButton></Group> : null}
            </Stack>
          ))}
        </Stack>
      )}
      <Modal opened={Boolean(cancelId)} onClose={() => onCancelRequest(null)} title="CGV 예매 취소">
        <Stack gap="lg"><Text size="sm">CGV 예매를 실제로 취소합니다. 계속할까요?</Text><Group justify="flex-end"><SecondaryButton onClick={() => onCancelRequest(null)}>돌아가기</SecondaryButton><PrimaryButton color="red" loading={cancelling} onClick={onConfirmCancellation}>실제 취소</PrimaryButton></Group></Stack>
      </Modal>
    </Section>
  );
}
