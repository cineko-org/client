import { useCallback, useEffect, useRef, useState } from 'react';
import { create } from '@bufbuild/protobuf';
import { api, desktopBridge, errorMessage } from '../../api/client';
import {
	AccountCredentialsSchema, WebUIAccountStateSchema, WebUIActionStatusSchema, WebUIStateSchema,
	WebUITaskStatusResponseSchema, type WebUIAccountState, type WebUIState,
} from '../../api/proto';
import type { Notify } from '../../components/core/feedback';
import { emptyAppState, initialApplicationConnection, type ApplicationConnection } from './model';

const checkingAccount = create(WebUIAccountStateSchema, { state: { case: 'checking', value: {} } });

export function useApplicationState(notify: Notify, loadNotices: (userId: string) => Promise<void>) {
	const [state, setState] = useState<WebUIState>(emptyAppState);
  const [userId, setUserId] = useState('local-user');
	const [account, setAccount] = useState<WebUIAccountState>(checkingAccount);
  const [loading, setLoading] = useState(true);
  const [connection, setConnection] = useState<ApplicationConnection>(initialApplicationConnection);
  const userIdRef = useRef('local-user');
  const reportedTasks = useRef(new Set<string>());
  const pollTimer = useRef<number | undefined>(undefined);
  const stateRequest = useRef(0);
  const statusRequest = useRef(0);
  const lastSuccessfulAt = useRef('');
  const bridge = desktopBridge();
  const invalidateRequests = useCallback(() => {
    stateRequest.current++;
    statusRequest.current++;
  }, []);

  const markConnectionFailure = useCallback((error: unknown) => {
    setConnection({
      status: lastSuccessfulAt.current ? 'stale' : 'unavailable',
      message: errorMessage(error),
      lastSuccessfulAt: lastSuccessfulAt.current,
      retrying: false,
    });
  }, []);

  const markConnectionReady = useCallback(() => {
    const synchronizedAt = new Date().toISOString();
    lastSuccessfulAt.current = synchronizedAt;
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
    const request = ++statusRequest.current;
    window.clearTimeout(pollTimer.current);
    try {
      const [tasks, accountState] = await Promise.all([
			api('/api/status', WebUITaskStatusResponseSchema),
			api('/api/account', WebUIAccountStateSchema),
		]);
      if (request !== statusRequest.current) return;
		const running = tasks.tasks.filter((task) => task.state.case === 'running').length;
		setAccount(accountState);
		for (const task of tasks.tasks) {
			const reportKey = `${task.id}:${task.state.case}:${task.updatedAt?.seconds ?? 0n}`;
			if (task.state.case === 'running' || reportedTasks.current.has(reportKey)) continue;
			reportedTasks.current.add(reportKey);
			if (task.state.case === 'failed') {
				notify(task.message || `${task.id} 작업이 실패했습니다.`, { tone: 'error', important: true });
        }
      }
      await Promise.all([loadState(activeUserId), loadNotices(activeUserId)]);
		if (running > 0 || accountState.state.case === 'checking') {
        pollTimer.current = window.setTimeout(() => void pollStatusForUser(activeUserId), 2500);
      }
    } catch (error) {
      if (request === statusRequest.current) {
        markConnectionFailure(error);
        pollTimer.current = window.setTimeout(() => void pollStatusForUser(activeUserId), 5000);
      }
    }
  }, [loadNotices, loadState, markConnectionFailure, notify]);

  const initialize = useCallback(async () => {
    const hasCachedState = Boolean(lastSuccessfulAt.current);
    setLoading(!hasCachedState);
    setConnection((current) => ({ ...current, retrying: current.status !== 'loading' }));
    try {
      const activeUserId = bridge ? await bridge.GetUserID() : 'local-user';
      userIdRef.current = activeUserId;
      setUserId(activeUserId);
      await Promise.all([loadState(activeUserId), loadNotices(activeUserId)]);
      void pollStatus(activeUserId);
    } catch (error) {
      markConnectionFailure(error);
      notify(errorMessage(error), { tone: 'error', important: true });
    } finally {
      setLoading(false);
    }
  }, [bridge, loadNotices, loadState, markConnectionFailure, notify, pollStatus]);

  useEffect(() => {
    window.__cinekoAppBooted = true;
    void initialize();
    const eventsOn = window.runtime?.EventsOn;
    if (eventsOn) {
      const unsubscribeData = eventsOn('data:changed', () => void initialize());
      return () => {
        window.clearTimeout(pollTimer.current);
        invalidateRequests();
        unsubscribeData?.();
      };
    }
    return () => {
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

  const saveAccountCredentials = useCallback(async (id: string, password: string) => {
    try {
		await api('/api/account/credentials', WebUIActionStatusSchema, { method: 'PUT' }, AccountCredentialsSchema,
			create(AccountCredentialsSchema, { id, password }));
      notify('로그인 정보를 안전하게 저장하고 CGV 로그인을 시작했습니다.');
      void pollStatus();
    } catch (error) {
      notify(errorMessage(error), { tone: 'error' });
    }
  }, [notify, pollStatus]);

  const restoreAuthentication = useCallback(async () => {
    try {
		await api('/api/auth/restore', WebUIActionStatusSchema, { method: 'POST' });
      notify('저장된 정보로 CGV 로그인을 시작했습니다.');
      void pollStatus();
    } catch (error) {
      notify(errorMessage(error), { tone: 'error' });
    }
  }, [notify, pollStatus]);

  const deleteAccountCredentials = useCallback(async () => {
    try {
		await api('/api/account/credentials', WebUIActionStatusSchema, { method: 'DELETE' });
      notify('저장된 CGV 로그인 정보를 삭제했습니다.');
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
    state, userId, account, loading, connection, desktopAvailable: Boolean(bridge),
    retryConnection: initialize,
    reload: loadState, openAuthentication, saveAccountCredentials, restoreAuthentication,
    deleteAccountCredentials, exit, pollStatus,
  };
}
