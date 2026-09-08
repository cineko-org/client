import { useCallback, useEffect, useRef, useState } from 'react';
import { createRequestID, logClientEvent } from '../../api/client';
import { useExclusiveOperation } from '../../shared/useExclusiveOperation';
import { emptyOperationsLogSnapshot, emptyNetworkCaptureSnapshot, type NetworkCaptureSnapshot, type OperationsLogSnapshot, type OperationsMinimumLevel } from './model';

interface LogState { phase: 'loading' | 'ready' | 'error'; snapshot: OperationsLogSnapshot; network: NetworkCaptureSnapshot; error: string }
type DetailState = { phase: 'idle' } | { phase: 'loading'; id: string } | { phase: 'ready'; value: Record<string, unknown> } | { phase: 'error'; error: string };
const emptyState: LogState = { phase: 'loading', snapshot: emptyOperationsLogSnapshot, network: emptyNetworkCaptureSnapshot, error: '' };

function isSnapshot(value: unknown): value is OperationsLogSnapshot {
  if (typeof value !== 'object' || value === null) return false;
  const candidate = value as Partial<OperationsLogSnapshot>;
  return Array.isArray(candidate.entries) && Array.isArray(candidate.aggregates) && typeof candidate.matching === 'number' && typeof candidate.warnings === 'number' && typeof candidate.errors === 'number';
}
function isNetworkSnapshot(value: unknown): value is NetworkCaptureSnapshot {
  if (typeof value !== 'object' || value === null) return false;
  const candidate = value as Partial<NetworkCaptureSnapshot>;
  return Array.isArray(candidate.entries) && typeof candidate.matching === 'number' && typeof candidate.statistics === 'object' && candidate.statistics !== null && typeof candidate.statistics.provider_sent === 'number' && typeof candidate.statistics.status_429 === 'number';
}

export function useOperationsLogs() {
  const [minimumLevel, updateMinimumLevel] = useState<OperationsMinimumLevel>('warn');
  const [state, setState] = useState<LogState>(emptyState);
  const [detail, setDetail] = useState<DetailState>({ phase: 'idle' });
  const readOwner = useRef<AbortController | null>(null);
  const detailOwner = useRef<AbortController | null>(null);
  const failureLogged = useRef(false);
  const mounted = useRef(false);
  const { active, start, isRunning } = useExclusiveOperation();
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const setMinimumLevel = useCallback((level: OperationsMinimumLevel) => {
    if (isRunning()) return;
    setDetail({ phase: 'idle' });
    updateMinimumLevel(level);
  }, [isRunning]);
  const cancelReads = useCallback(() => {
    readOwner.current?.abort(); readOwner.current = null;
    detailOwner.current?.abort(); detailOwner.current = null;
  }, []);

  const load = useCallback(async () => {
    if (!mounted.current || readOwner.current || isRunning()) return;
    const controller = new AbortController();
    readOwner.current = controller;
    const deadline = window.setTimeout(() => controller.abort(), 10_000);
    try {
      const init = { signal: controller.signal, headers: { 'X-Request-Id': createRequestID() } };
      const [response, networkResponse] = await Promise.all([
        fetch(`/api/logs?min_level=${minimumLevel}&limit=300`, init), fetch('/api/logs/network?outcome=failed&limit=100', init),
      ]);
      if (!response.ok || !networkResponse.ok) throw new Error(`log snapshot request failed: ${response.status}/${networkResponse.status}`);
      const [snapshot, network]: unknown[] = await Promise.all([response.json(), networkResponse.json()]);
      if (!isSnapshot(snapshot) || !isNetworkSnapshot(network)) throw new Error('log snapshot response contract changed');
      if (readOwner.current !== controller || controller.signal.aborted) return;
      setState({ phase: 'ready', snapshot, network, error: '' });
      failureLogged.current = false;
    } catch (caught) {
      if (readOwner.current !== controller) return;
      setState((current) => ({ ...current, phase: 'error', error: '로컬 로그를 불러오지 못했습니다.' }));
      if (!failureLogged.current) {
        failureLogged.current = true;
        logClientEvent('error', 'operations.logs.load.failed', { scenario: 'operations', operation: 'load_log_snapshot', error: String(caught) });
      }
    } finally {
      window.clearTimeout(deadline);
      controller.abort();
      if (readOwner.current === controller) readOwner.current = null;
    }
  }, [isRunning, minimumLevel]);

  useEffect(() => {
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => { await load(); if (!disposed) timer = setTimeout(() => void poll(), 5000); };
    timer = setTimeout(() => void poll(), 0);
    return () => { disposed = true; clearTimeout(timer); cancelReads(); };
  }, [cancelReads, load]);

  const reload = useCallback(() => {
    if (!readOwner.current && !isRunning()) setState((current) => ({ ...current, phase: 'loading' }));
    return load();
  }, [isRunning, load]);

  const inspectNetwork = useCallback(async (id: string) => {
    if (isRunning()) return;
    detailOwner.current?.abort();
    const controller = new AbortController();
    detailOwner.current = controller;
    const deadline = window.setTimeout(() => controller.abort(), 10_000);
    setDetail({ phase: 'loading', id });
    try {
      const response = await fetch(`/api/logs/network/${encodeURIComponent(id)}`, { signal: controller.signal, headers: { 'X-Request-Id': createRequestID() } });
      if (!response.ok) throw new Error(`network capture request failed with ${response.status}`);
      const value: unknown = await response.json();
      if (typeof value !== 'object' || value === null) throw new Error('network capture response contract changed');
      if (detailOwner.current === controller && !controller.signal.aborted) setDetail({ phase: 'ready', value: value as Record<string, unknown> });
    } catch (caught) {
      if (detailOwner.current === controller) setDetail({ phase: 'error', error: `네트워크 상세를 불러오지 못했습니다: ${String(caught)}` });
    } finally {
      window.clearTimeout(deadline);
      controller.abort();
      if (detailOwner.current === controller) detailOwner.current = null;
    }
  }, [isRunning]);

  const clearLogs = useCallback(async () => {
    const release = start('clear');
    if (!release) return;
    cancelReads();
    setDetail({ phase: 'idle' });
    try {
      const response = await fetch('/api/logs', { method: 'DELETE', headers: { 'X-Request-Id': createRequestID() } });
      if (!response.ok) throw new Error(`log clear request failed with ${response.status}`);
      if (!mounted.current) return;
      setState({ ...emptyState, phase: 'ready' });
      release();
      await load();
    } catch (caught) {
      if (!mounted.current) return;
      setState((current) => ({ ...current, phase: 'error', error: `로컬 로그를 비우지 못했습니다: ${String(caught)}` }));
      throw caught;
    } finally { release(); }
  }, [cancelReads, load, start]);

  return { minimumLevel, setMinimumLevel, snapshot: state.snapshot, network: state.network,
    selectedNetwork: detail.phase === 'ready' ? detail.value : null, selectedNetworkID: detail.phase === 'loading' ? detail.id : '',
    inspectNetwork, loading: state.phase === 'loading', clearing: active === 'clear',
    error: detail.phase === 'error' ? detail.error : state.error, reload, clearLogs };
}
