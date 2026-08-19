import { useCallback, useState } from 'react';
import { api, errorMessage } from '../../api/client';
import type { AppState } from '../../api/types';
import type { Notify } from '../../components/core/feedback';

export function useReservations(state: AppState, userId: string, reload: () => Promise<AppState>, notify: Notify) {
  const [cancelId, setCancelId] = useState<string | null>(null);
  const [cancelling, setCancelling] = useState(false);

  const cancel = useCallback(async (reservationId: string, commit: boolean) => {
    setCancelId(null);
    setCancelling(true);
    try {
      const draft = await api<{ refundAmount?: string }>('/api/reservations/cancel', { method: 'POST', body: {
        userId, reservationId, commit, headful: true,
      } });
      notify(commit ? '예매를 취소했습니다.' : `취소 검토 완료 · ${draft.refundAmount || '환불액 화면 확인'}`, {
        tone: commit ? 'warning' : 'info', important: commit,
      });
      await reload();
    } catch (error) {
      notify(errorMessage(error), { tone: 'error', important: commit });
    } finally {
      setCancelling(false);
    }
  }, [notify, reload, userId]);

  return {
    reservations: state.reservations, cancelId, setCancelId, cancelling,
    reviewCancellation: (id: string) => cancel(id, false),
    confirmCancellation: () => cancelId ? cancel(cancelId, true) : undefined,
  };
}
