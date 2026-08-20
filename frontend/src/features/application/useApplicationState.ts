import { useCallback, useEffect, useRef, useState } from 'react';
import { api, desktopBridge, errorMessage } from '../../api/client';
import type { AccountState, AppState, ApplicationConnection, TaskState } from '../../api/types';
import type { Notify } from '../../components/core/feedback';
import { emptyAppState, initialApplicationConnection } from './model';

const checkingAccount: AccountState = { status: 'checking', authenticated: false };

export function useApplicationState(notify: Notify, loadNotices: (userId: string) => Promise<void>) {
  const [state, setState] = useState<AppState>(emptyAppState);
  const [userId, setUserId] = useState('local-user');
  const [account, setAccount] = useState<AccountState>(checkingAccount);
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
      const next = await api<AppState>(`/api/state?user=${encodeURIComponent(activeUserId)}`);
      if (request !== stateRequest.current) return next;
      setState({ ...emptyAppState, ...next });
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
        api<Record<string, TaskState>>('/api/status'),
        api<AccountState>('/api/account'),
      ]);
      if (request !== statusRequest.current) return;
      const running = Object.values(tasks).filter((task) => task.status === 'running').length;
      setAccount(accountState);
      for (const [id, task] of Object.entries(tasks)) {
        const reportKey = `${id}:${task.status}:${task.updatedAt}`;
        if (task.status === 'running' || reportedTasks.current.has(reportKey)) continue;
        reportedTasks.current.add(reportKey);
        if (task.status === 'failed') {
          notify(task.message || `${id} 작업이 실패했습니다.`, { tone: 'error', important: true });
        }
      }
      await Promise.all([loadState(activeUserId), loadNotices(activeUserId)]);
      if (running > 0 || accountState.status === 'checking') {
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
      await api('/api/auth/open', { method: 'POST', body: {} });
      notify('CGV 로그인을 위한 Chrome을 열었습니다.');
      void pollStatus();
    } catch (error) {
      notify(errorMessage(error), { tone: 'error' });
    }
  }, [notify, pollStatus]);

  const saveAccountCredentials = useCallback(async (id: string, password: string) => {
    try {
      await api('/api/account/credentials', { method: 'PUT', body: { id, password } });
      notify('로그인 정보를 안전하게 저장하고 CGV 로그인을 시작했습니다.');
      void pollStatus();
    } catch (error) {
      notify(errorMessage(error), { tone: 'error' });
    }
  }, [notify, pollStatus]);

  const restoreAuthentication = useCallback(async () => {
    try {
      await api('/api/auth/restore', { method: 'POST', body: {} });
      notify('저장된 정보로 CGV 로그인을 시작했습니다.');
      void pollStatus();
    } catch (error) {
      notify(errorMessage(error), { tone: 'error' });
    }
  }, [notify, pollStatus]);

  const deleteAccountCredentials = useCallback(async () => {
    try {
      await api('/api/account/credentials', { method: 'DELETE' });
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
