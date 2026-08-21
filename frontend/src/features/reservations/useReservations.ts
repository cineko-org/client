import { useCallback, useState } from 'react';
import { create } from '@bufbuild/protobuf';
import { api, errorMessage } from '../../api/client';
import {
	WebUICancellationResultSchema, WebUIReservationCancellationRequestSchema, type WebUIState,
} from '../../api/proto';
import { stateReservations } from '../../api/resources';
import type { Notify } from '../../components/core/feedback';

export function useReservations(state: WebUIState, userId: string, reload: () => Promise<WebUIState>, notify: Notify) {
  const [cancelId, setCancelId] = useState<string | null>(null);
  const [cancelling, setCancelling] = useState(false);

  const cancel = useCallback(async (reservationId: string, commit: boolean) => {
    setCancelId(null);
    setCancelling(true);
    try {
		const reservation = stateReservations(state).find((item) => item.id === reservationId);
		if (!reservation) return;
		const draft = await api('/api/reservations/cancel', WebUICancellationResultSchema, { method: 'POST' },
			WebUIReservationCancellationRequestSchema,
				create(WebUIReservationCancellationRequestSchema, {
					reservation, commit, headful: true,
				}));
		notify(commit ? '예매를 취소했습니다.' : `취소 검토 완료 · ${draft.refundAmount || '환불액 화면 확인'}`, {
        tone: commit ? 'warning' : 'info', important: commit,
      });
      await reload();
    } catch (error) {
      notify(errorMessage(error), { tone: 'error', important: commit });
    } finally {
      setCancelling(false);
    }
	}, [notify, reload, state]);

  return {
		reservations: stateReservations(state), cancelId, setCancelId, cancelling,
    reviewCancellation: (id: string) => cancel(id, false),
    confirmCancellation: () => cancelId ? cancel(cancelId, true) : undefined,
  };
}
