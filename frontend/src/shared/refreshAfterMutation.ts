import { errorMessage, logClientEvent } from '../api/client';
import type { Notify } from '../components/core/feedback';

// A confirmed write must never be presented as a failed write just because
// the subsequent read failed: retrying could repeat a real-world action.
export async function refreshAfterMutation(reload: () => Promise<unknown>, notify: Notify) {
  try { await reload(); }
  catch (error) {
    notify('요청은 처리됐지만 목록을 갱신하지 못했습니다. 다시 실행하지 말고 새로고침하세요.', { tone: 'warning' });
    logClientEvent('warn', 'state.refresh.after_mutation.failed', { operation: 'refresh_after_mutation', error: errorMessage(error) });
  }
}
