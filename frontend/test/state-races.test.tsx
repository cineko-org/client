import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { create } from '@bufbuild/protobuf';
import { useOperationsLogs } from '../src/features/operations/useOperationsLogs';
import { useNotifications } from '../src/features/notifications/useNotifications';
import { useReservations } from '../src/features/reservations/useReservations';
import { useHookSettings } from '../src/features/settings/useHookSettings';
import { useNetworkSettings } from '../src/features/settings/useNetworkSettings';
import { useMonitorEditor } from '../src/features/monitors/useMonitorEditor';
import { catalogReducer, emptyCatalogState, catalogPresentation } from '../src/features/presets/catalogState';
import { ResourceSchema, ReservationSchema, WebUIStateSchema, NetworkSettingsSchema, SettingsSchema, SnapshotSchema } from '../src/api/proto';
import { encodeDesktopProto, type DesktopBridge } from '../src/api/desktop';
import { emptyAppState } from '../src/features/application/model';
import { emptyNetworkCaptureSnapshot, emptyOperationsLogSnapshot } from '../src/features/operations/model';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); delete window.go; });
const response = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}
const notification = { resources: [{ appEvent: { id: 'notice', userId: 'local-user', message: 'saved', info: {} } }] };

describe('controller state races', () => {
  it('keeps the newest network detail when an older response arrives later', async () => {
    const a = deferred<Response>(), b = deferred<Response>();
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockImplementation(async (input) => {
      const path = String(input);
      if (path.endsWith('/A')) return a.promise;
      if (path.endsWith('/B')) return b.promise;
      return response(path.includes('/network') ? emptyNetworkCaptureSnapshot : emptyOperationsLogSnapshot);
    }));
    const { result } = renderHook(() => useOperationsLogs());
    await waitFor(() => expect(result.current.loading).toBe(false));
    let first: Promise<void>, second: Promise<void>;
    act(() => { first = result.current.inspectNetwork('A'); second = result.current.inspectNetwork('B'); });
    await act(async () => { b.resolve(response({ id: 'B' })); await second; });
    await act(async () => { a.resolve(response({ id: 'A' })); await first; });
    expect(result.current.selectedNetwork).toEqual({ id: 'B' });
  });

  it('does not erase notifications when the server rejects clear', async () => {
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockImplementation(async (_input, init) => init?.method === 'DELETE'
      ? response({ error: { message: 'cannot clear' } }, 500) : response(notification)));
    const { result } = renderHook(() => useNotifications());
    await act(async () => { await result.current.load('local-user'); });
    await act(async () => { await result.current.clear(); });
    expect(result.current.notices).toHaveLength(1);
    expect(result.current.feedback?.tone).toBe('error');
  });

  it('submits only one cancellation review for three synchronous calls', async () => {
    const pending = deferred<Response>();
    const fetchMock = vi.fn<typeof fetch>().mockImplementation(async () => (await pending.promise).clone());
    vi.stubGlobal('fetch', fetchMock);
    const state = create(WebUIStateSchema, { resources: [create(ResourceSchema, { resource: { case: 'reservation', value: create(ReservationSchema, { id: 'r' }) } })] });
    const reload = vi.fn<() => Promise<typeof state>>().mockResolvedValue(state);
    const notify = vi.fn<(message: string) => void>();
    const { result } = renderHook(() => useReservations(state, 'local-user', reload, notify));
    let calls: Promise<void>[] = [];
    act(() => { calls = Array.from({ length: 3 }, () => result.current.reviewCancellation('r')); });
    const count = fetchMock.mock.calls.length;
    await act(async () => { pending.resolve(response({})); await Promise.all(calls); });
    expect(count).toBe(1);
  });

  it('does not resurrect a cleared detail from a late response', async () => {
    const late = deferred<Response>();
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockImplementation(async (input) => {
      const path = String(input);
      if (path.endsWith('/late')) return late.promise;
      return response(path.includes('/network') ? emptyNetworkCaptureSnapshot : emptyOperationsLogSnapshot);
    }));
    const { result } = renderHook(() => useOperationsLogs());
    await waitFor(() => expect(result.current.loading).toBe(false));
    let read: Promise<void>;
    act(() => { read = result.current.inspectNetwork('late'); });
    await act(async () => result.current.clearLogs());
    await act(async () => { late.resolve(response({ id: 'late' })); await read; });
    expect(result.current.selectedNetwork).toBeNull();
    expect(result.current.error).toBe('');
  });

  it('does not start a refresh after unmounting during log clear', async () => {
    const cleared = deferred<Response>();
    const fetchMock = vi.fn<typeof fetch>().mockImplementation(async (input, init) => {
      if (init?.method === 'DELETE') return cleared.promise;
      return response(String(input).includes('/network') ? emptyNetworkCaptureSnapshot : emptyOperationsLogSnapshot);
    });
    vi.stubGlobal('fetch', fetchMock);
    const { result, unmount } = renderHook(() => useOperationsLogs());
    await waitFor(() => expect(result.current.loading).toBe(false));
    let pending: Promise<void>;
    act(() => { pending = result.current.clearLogs(); });
    unmount();
    const count = fetchMock.mock.calls.length;
    await act(async () => { cleared.resolve(response({})); await pending; });
    expect(fetchMock).toHaveBeenCalledTimes(count);
  });

  it('serializes notification read and clear without applying an older read later', async () => {
    const marked = deferred<Response>();
    let cleared = false;
    const fetchMock = vi.fn<typeof fetch>().mockImplementation(async (_input, init) => {
      if (init?.method === 'POST') return marked.promise;
      if (init?.method === 'DELETE') { cleared = true; return response({ completed: {} }); }
      return response(cleared ? { resources: [] } : notification);
    });
    vi.stubGlobal('fetch', fetchMock);
    const { result } = renderHook(() => useNotifications());
    await act(async () => result.current.load('local-user'));
    let writes: Promise<void>[];
    await act(async () => { writes = [result.current.markRead(), result.current.clear()]; });
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'DELETE')).toHaveLength(0);
    await act(async () => { marked.resolve(response({ completed: {} })); await Promise.all(writes); });
    expect(result.current.notices).toHaveLength(0);
    expect(fetchMock.mock.calls.map(([, init]) => init?.method ?? 'GET')).toEqual(['GET', 'POST', 'GET', 'DELETE', 'GET']);
  });

  it('locks settings reads and writes before the next render', async () => {
    const networkValue = encodeDesktopProto(NetworkSettingsSchema, create(NetworkSettingsSchema, { mode: { case: 'direct', value: {} } }));
    const hookValue = encodeDesktopProto(SettingsSchema, create(SettingsSchema));
    const networkSaved = deferred<string>(), hookSaved = deferred<string>();
    const bridge = {
      GetNetworkSettings: vi.fn<DesktopBridge['GetNetworkSettings']>().mockResolvedValue(networkValue),
      GetHookSettings: vi.fn<DesktopBridge['GetHookSettings']>().mockResolvedValue(hookValue),
      SaveNetworkSettings: vi.fn<DesktopBridge['SaveNetworkSettings']>().mockReturnValue(networkSaved.promise),
      SaveHookSettings: vi.fn<DesktopBridge['SaveHookSettings']>().mockReturnValue(hookSaved.promise),
    };
    window.go = { main: { DesktopApp: bridge as unknown as DesktopBridge } };
    const notify = vi.fn<(message: string) => void>();
    const { result } = renderHook(() => ({ network: useNetworkSettings(true, notify), hooks: useHookSettings(true, notify) }));
    await waitFor(() => { expect(result.current.network.loadState).toBe('ready'); expect(result.current.hooks.loadState).toBe('ready'); });
    let saves: Promise<unknown>[];
    act(() => {
      saves = [result.current.network.save(), result.current.network.save(), result.current.hooks.save(), result.current.hooks.save()];
      void result.current.network.load(); void result.current.hooks.load();
    });
    expect(result.current.network.saving).toBe(true);
    expect(result.current.hooks.saving).toBe(true);
    expect(bridge.GetNetworkSettings).toHaveBeenCalledOnce(); expect(bridge.GetHookSettings).toHaveBeenCalledOnce();
    expect(bridge.SaveNetworkSettings).toHaveBeenCalledOnce(); expect(bridge.SaveHookSettings).toHaveBeenCalledOnce();
    await act(async () => { networkSaved.resolve(networkValue); hookSaved.resolve(hookValue); await Promise.all(saves); });
    expect(result.current.network.saving).toBe(false); expect(result.current.hooks.saving).toBe(false);
  });

  it('completes a confirmed monitor save even when the subsequent refresh fails', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(response({}));
    vi.stubGlobal('fetch', fetchMock);
    const reload = vi.fn<() => Promise<typeof emptyAppState>>().mockRejectedValue(new Error('refresh unavailable'));
    const notify = vi.fn<(message: string, options?: { tone?: string }) => void>();
    const onSaved = vi.fn<() => void>();
    const { result } = renderHook(() => useMonitorEditor(emptyAppState, 'user', reload, notify, onSaved));
    act(() => result.current.setForm({ ...result.current.form, movieId: 'movie', movie: 'Movie', presetId: 'preset', weekdays: ['4'] }));
    await act(async () => result.current.requestCreate());
    expect(fetchMock).toHaveBeenCalledOnce(); expect(reload).toHaveBeenCalledOnce(); expect(onSaved).toHaveBeenCalledOnce();
    expect(notify.mock.calls.some(([message, options]) => message.includes('다시 실행하지 말고') && options?.tone === 'warning')).toBe(true);
    expect(notify.mock.calls.some(([, options]) => options?.tone === 'error')).toBe(false);
  });

  it('keeps preset seats while awaiting a map and resets the entire theater selection together', () => {
    let state = catalogReducer(emptyCatalogState, { type: 'reset', region: '서울', theaterId: 'theater', auditoriumId: 'imax', seats: ['J1', 'J2'] });
    state = catalogReducer(state, { type: 'auditorium', id: 'imax' });
    state = catalogReducer(state, { type: 'resolution', phase: 'queued' });
    expect(state.pickedSeats).toEqual(['J1', 'J2']);
    expect(catalogPresentation(state).seatMapLoadState).toBe('pending');
    state = catalogReducer(state, { type: 'resolution', phase: 'cached', snapshot: create(SnapshotSchema, { auditoriumId: 'imax', layout: { seats: [{ label: 'J1' }] } }) });
    expect(state.pickedSeats).toEqual(['J1']);
    state = catalogReducer(state, { type: 'reset', region: '부산' });
    expect(state).toEqual({ ...emptyCatalogState, region: '부산' });
  });
});
