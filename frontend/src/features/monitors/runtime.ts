import type { Monitor } from '../../api/proto';
import { monitorStatus } from '../../api/resources';
import { monitorStatusLabel } from './model';
import { unknownMonitoringRuntime, type MonitoringRuntime } from '../../shared/application';
export type { MonitoringRuntime } from '../../shared/application';

// Login/rate-limit waits delay work, not the user's ability to enable or stop it.
export function runtimeCanStart(runtime = unknownMonitoringRuntime): boolean {
  return runtime.state !== 'unavailable' && runtime.state !== 'stopped';
}

export function monitorPresentation(monitor: Monitor, runtime = unknownMonitoringRuntime) {
  const status = monitorStatus(monitor);
  const enabled = status === 'pending' || status === 'running';
  const canStart = runtimeCanStart(runtime);
  const active = enabled && runtime.state === 'ready';
  const blockedLabels: Record<MonitoringRuntime['state'], string> = {
    checking: '로그인 확인 중', stopped: '감시 오류',
    login_required: '로그인 필요', preparation_failed: '브라우저 준비 실패',
    unavailable: '실행 상태 확인 불가', ready: '',
    rate_limited: '요청 제한으로 휴식 중',
	  scan_failed: '신규 일정 조회 실패',
  };
  const label = enabled
    ? active
      ? status === 'running' ? '좌석 확인 중' : monitor.watchCancellationSeats ? '신규 일정·취소표 감시 중' : '신규 일정 감시 중'
      : blockedLabels[runtime.state]
    : monitorStatusLabel(status);
  return {
    enabled, active, canStart, label,
    reason: enabled && !active ? runtime.reason : '',
    color: active ? 'blue' : enabled ? 'orange' : status === 'booked' ? 'green' : status === 'failed' ? 'red' : status === 'triggered' || status === 'payment_unknown' ? 'orange' : 'gray',
  };
}
