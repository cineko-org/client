import { useCallback, useState } from 'react';
import { create } from '@bufbuild/protobuf';
import { api, errorMessage } from '../../api/client';
import {
	WebUICancellationResultSchema, WebUIReservationCancellationRequestSchema, type WebUIState,
} from '../../api/proto';
import { stateReservations } from '../../api/resources';
import type { Notify } from '../../components/core/feedback';
import { useExclusiveOperation } from '../../shared/useExclusiveOperation';
import { refreshAfterMutation } from '../../shared/refreshAfterMutation';

export function useReservations(state: WebUIState, userId: string, reload: () => Promise<WebUIState>, notify: Notify) {
  const [cancelId, setCancelId] = useState<string | null>(null);
  const { active, start, isRunning } = useExclusiveOperation();

  const cancel = useCallback(async (reservationId: string, commit: boolean) => {
    const release = start(reservationId);
    if (!release) return;
    setCancelId(null);
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
      await refreshAfterMutation(reload, notify);
    } catch (error) {
      notify(errorMessage(error), { tone: 'error', important: commit });
    } finally {
      release();
    }
	}, [notify, reload, start, state]);

  return {
			reservations: stateReservations(state), cancelId, setCancelId: (id: string | null) => { if (!isRunning()) setCancelId(id); }, cancelling: active !== null,
    reviewCancellation: (id: string) => cancel(id, false),
    confirmCancellation: () => cancelId ? cancel(cancelId, true) : undefined,
  };
}
