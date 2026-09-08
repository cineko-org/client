import { useCallback, useEffect, useRef, useState } from 'react';
import { api, desktopBridge, errorMessage, logClientEvent } from '../../api/client';
import {
	WebUIAccountStateSchema, WebUIActionStatusSchema, WebUIStateSchema,
	type WebUIState,
} from '../../api/proto';
import type { Notify } from '../../components/core/feedback';
import { emptyAppState, initialApplicationConnection, type ApplicationConnection } from './model';
import { failedRuntime, initialRuntime, readApplicationRuntime } from './runtime';

export function useApplicationState(notify: Notify, loadNotices: (userId: string) => Promise<void>) {
	const [state, setState] = useState<WebUIState>(emptyAppState);
  const [runtime, setRuntime] = useState(initialRuntime);
  const [connection, setConnection] = useState<ApplicationConnection>(initialApplicationConnection);
  const userIdRef = useRef('local-user');
  const reportedTasks = useRef(new Set<string>());
  const pollTimer = useRef<number | undefined>(undefined);
  const stateRequest = useRef(0);
  const statusRequest = useRef(0);
  const runtimeFlight = useRef<Promise<void> | null>(null);
  const runtimePending = useRef(false);
  const runtimeAbort = useRef<AbortController | null>(null);
  const runtimeFailureLogged = useRef(false);
  const lifecycle = useRef(0);
  const bridge = desktopBridge();
  const invalidateRequests = useCallback(() => {
    lifecycle.current++;
    stateRequest.current++;
    statusRequest.current++;
    runtimeAbort.current?.abort();
  }, []);

  const markConnectionFailure = useCallback((error: unknown) => {
    setConnection((current) => ({
      status: current.lastSuccessfulAt ? 'stale' : 'unavailable',
      message: errorMessage(error),
      lastSuccessfulAt: current.lastSuccessfulAt,
      retrying: false,
    }));
  }, []);

  const markConnectionReady = useCallback(() => {
    const synchronizedAt = new Date().toISOString();
    setConnection({ status: 'ready', message: '', lastSuccessfulAt: synchronizedAt, retrying: false });
  }, []);

	const loadState = useCallback(async (activeUserId = userIdRef.current) => {
    const request = ++stateRequest.current;
    try {
		const next = await api(`/api/state?user=${encodeURIComponent(activeUserId)}`, WebUIStateSchema);
      if (request !== stateRequest.current) return next;
		setState(next);
      markConnectionReady();
      return next;
    } catch (error) {
      if (request === stateRequest.current) markConnectionFailure(error);
      throw error;
    }
  }, [markConnectionFailure, markConnectionReady]);

  const pollStatus = useCallback(async function pollStatusForUser(activeUserId = userIdRef.current) {
    if (runtimeFlight.current) {
      runtimePending.current = true;
      return runtimeFlight.current;
    }
    const request = ++statusRequest.current;
    window.clearTimeout(pollTimer.current);
    const run = async () => {
      do {
        runtimePending.current = false;
        const controller = new AbortController();
        runtimeAbort.current = controller;
        const deadline = window.setTimeout(() => controller.abort(), 4000);
        try {
          // eslint-disable-next-line no-await-in-loop -- Keep refreshes serial; a burst requests only one follow-up.
          const next = await readApplicationRuntime(controller.signal);
          if (request !== statusRequest.current) return;
          setRuntime(next);
          const recovering = runtimeFailureLogged.current;
          runtimeFailureLogged.current = false;
          let changed = false;
          for (const task of next.tasks) {
            const reportKey = `${task.id}:${task.state.case}:${task.updatedAt?.seconds ?? 0n}`;
            if (task.state.case === 'running' || reportedTasks.current.has(reportKey)) continue;
            reportedTasks.current.add(reportKey);
            changed = true;
            if (task.state.case === 'failed') notify(task.message || `${task.id} 작업이 실패했습니다.`, { tone: 'error', important: true });
          }
          // eslint-disable-next-line no-await-in-loop -- Publish task-related data before the next runtime refresh.
          if (changed || recovering) await Promise.all([loadState(activeUserId), loadNotices(activeUserId)]);
        } catch (error) {
          if (request === statusRequest.current) {
            setRuntime(failedRuntime());
            markConnectionFailure(error);
            if (!runtimeFailureLogged.current) logClientEvent('warn', 'application.runtime.read.failed', { scenario: 'application_state', operation: 'read_local_runtime', error: String(error) });
            runtimeFailureLogged.current = true;
          }
        } finally { window.clearTimeout(deadline); }
      } while (runtimePending.current && request === statusRequest.current);
    };
    runtimeFlight.current = run();
    try { await runtimeFlight.current; } finally {
      runtimeFlight.current = null;
      if (request === statusRequest.current) pollTimer.current = window.setTimeout(() => void pollStatusForUser(activeUserId), 5000);
    }
  }, [loadNotices, loadState, markConnectionFailure, notify]);

  const initialize = useCallback(async () => {
    const generation = lifecycle.current;
    setConnection((current) => ({ ...current, retrying: current.status !== 'loading' }));
    try {
      const activeUserId = bridge ? await bridge.GetUserID() : 'local-user';
      userIdRef.current = activeUserId;
      // Initialize the existing account check, but never keep a second UI
      // account state. All displays use the combined runtime snapshot.
      await Promise.all([loadState(activeUserId), loadNotices(activeUserId), api('/api/account', WebUIAccountStateSchema)]);
      if (generation === lifecycle.current) void pollStatus(activeUserId);
    } catch (error) {
      if (generation !== lifecycle.current) return;
      setRuntime(failedRuntime());
      markConnectionFailure(error);
      notify(errorMessage(error), { tone: 'error', important: true });
    }
  }, [bridge, loadNotices, loadState, markConnectionFailure, notify, pollStatus]);

  useEffect(() => {
    window.__cinekoAppBooted = true;
    let active = true;
    queueMicrotask(() => { if (active) void initialize(); });
    const eventsOn = window.runtime?.EventsOn;
    if (eventsOn) {
      const unsubscribeData = eventsOn('data:changed', () => void initialize());
      return () => {
        active = false;
        window.clearTimeout(pollTimer.current);
        invalidateRequests();
        unsubscribeData?.();
      };
    }
    return () => {
      active = false;
      window.clearTimeout(pollTimer.current);
      invalidateRequests();
    };
  }, [initialize, invalidateRequests]);

  const openAuthentication = useCallback(async () => {
    try {
		await api('/api/auth/open', WebUIActionStatusSchema, { method: 'POST' });
      notify('CGV 로그인을 위한 Chrome을 열었습니다.');
      void pollStatus();
    } catch (error) {
      notify(errorMessage(error), { tone: 'error' });
    }
  }, [notify, pollStatus]);

  const exit = useCallback(async () => {
    if (!bridge) return;
    try {
      await bridge.Exit();
    } catch (error) {
      notify(errorMessage(error), { tone: 'error' });
    }
  }, [bridge, notify]);

  return {
    state, runtime, userId: state.userId, loading: connection.status === 'loading', connection, desktopAvailable: Boolean(bridge),
    retryConnection: initialize,
    reload: loadState, openAuthentication, exit, pollStatus,
  };
}
