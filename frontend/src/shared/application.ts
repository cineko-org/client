import type { WebUIAccountState, WebUITaskState } from '../api/proto';
import type { IndicatorState } from '../components/core/StatusIndicator';

export interface MonitoringRuntime {
  state: 'checking' | 'stopped' | 'login_required' | 'preparation_failed' | 'scan_failed' | 'rate_limited' | 'ready' | 'unavailable';
  reason: string;
}

export interface ApplicationRuntime extends MonitoringRuntime {
	 scanner?: IndicatorState;
  account: WebUIAccountState;
  tasks: WebUITaskState[];
}

export const unknownMonitoringRuntime: MonitoringRuntime = { state: 'unavailable', reason: '실제 감시 상태를 확인하고 있습니다.' };

export interface ApplicationConnection {
	status: 'loading' | 'ready' | 'stale' | 'unavailable';
	message: string;
	lastSuccessfulAt: string;
	retrying: boolean;
}

export const initialApplicationConnection: ApplicationConnection = {
	status: 'loading',
	message: '',
	lastSuccessfulAt: '',
	retrying: false,
};
export function accountIndicatorState(account: WebUIAccountState): IndicatorState {
  switch (account.state.case) {
    case 'authenticated': return 'ready';
    case 'unauthenticated': return 'off';
    case 'error': return 'failed';
    default: return 'checking';
  }
}
