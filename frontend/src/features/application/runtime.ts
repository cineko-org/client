import { create, fromJson } from '@bufbuild/protobuf';
import { createRequestID } from '../../api/client';
import { WebUIAccountStateSchema, WebUITaskStatusResponseSchema } from '../../api/proto';
import { unknownMonitoringRuntime, type ApplicationRuntime } from '../../shared/application';

const states = new Set(['checking', 'stopped', 'login_required', 'preparation_failed', 'scan_failed', 'rate_limited', 'ready']);

export const initialRuntime: ApplicationRuntime = {
  scanner: 'checking',
  ...unknownMonitoringRuntime,
  account: create(WebUIAccountStateSchema, { state: { case: 'checking', value: {} } }),
  tasks: [],
};

export function failedRuntime(): ApplicationRuntime {
  return { scanner: 'failed', state: 'unavailable', reason: '실제 실행 상태를 확인할 수 없습니다. 연결 복구 후 다시 확인합니다.',
    account: create(WebUIAccountStateSchema, { state: { case: 'error', value: {} } }), tasks: [],
  };
}

export async function readApplicationRuntime(signal: AbortSignal): Promise<ApplicationRuntime> {
  // This is one local memory snapshot, never a provider request.
  const response = await fetch('/api/runtime', {
    signal, cache: 'no-store', headers: { 'X-Request-Id': createRequestID() },
  });
  if (!response.ok) throw new Error(`runtime status ${response.status}`);
  const value = await response.json();
  if (!states.has(value.state) || typeof value.reason !== 'string') throw new Error('invalid application runtime');
  const account = fromJson(WebUIAccountStateSchema, value.account);
  if (!account.state.case) throw new Error('missing account runtime');
  const tasks = fromJson(WebUITaskStatusResponseSchema, value.tasks).tasks;
  const scanner = ['checking', 'off', 'failed', 'ready'].includes(value.scanner) ? value.scanner : 'failed';
  return { state: value.state, reason: value.reason, account, tasks, scanner };
}
