import { act, cleanup, render, renderHook, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '../src/app/App';
import { useApplicationState } from '../src/features/application/useApplicationState';

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
  vi.stubGlobal('matchMedia', vi.fn<(query: string) => MediaQueryList>().mockImplementation((query) => ({
    matches: false, media: query, onchange: null,
    addEventListener: () => undefined, removeEventListener: () => undefined,
    addListener: () => undefined, removeListener: () => undefined, dispatchEvent: () => true,
  })));
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
});
afterEach(() => { cleanup(); vi.useRealTimers(); vi.unstubAllGlobals(); delete window.go; delete window.runtime; });

const response = (value: unknown) => new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
const checking = { state: 'checking', reason: 'CGV 로그인 상태를 확인하고 있습니다.', account: { checking: {} }, tasks: { tasks: [] } };
const ready = { state: 'ready', reason: '', account: { authenticated: {} }, tasks: { tasks: [] } };

function localResponse(input: RequestInfo | URL): Response {
  const path = String(input);
  if (path.startsWith('/api/state')) return response({ userId: 'local-user', resources: [] });
  if (path === '/api/account') return response({ checking: {} });
  if (path.startsWith('/api/events')) return response({ resources: [] });
  throw new Error(`unexpected request ${path}`);
}

describe('single application runtime owner', () => {
  it('updates actual header and home together, including a second check after green', async () => {
    let snapshot: unknown = checking;
    const fetchMock = vi.fn<typeof fetch>().mockImplementation(async (input) => String(input) === '/api/runtime' ? response(snapshot) : localResponse(input));
    vi.stubGlobal('fetch', fetchMock);
    render(<App />);
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(screen.getByRole('img', { name: 'CGV: 확인 중' })).toBeTruthy();
    expect(screen.getByText(checking.reason)).toBeTruthy();
    expect(screen.queryByRole('img', { name: 'CGV: 정상' })).toBeNull();

    snapshot = ready;
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(screen.getByRole('img', { name: 'CGV: 정상' })).toBeTruthy();
    expect(screen.queryByText(checking.reason)).toBeNull();

    snapshot = checking;
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(screen.getByRole('img', { name: 'CGV: 확인 중' })).toBeTruthy();
    expect(screen.getByText(checking.reason)).toBeTruthy();
    expect(screen.queryByRole('img', { name: 'CGV: 정상' })).toBeNull();
    const paths = fetchMock.mock.calls.map(([path]) => String(path));
    expect(paths.filter((path) => path === '/api/runtime')).toHaveLength(3);
    expect(paths.filter((path) => path === '/api/account')).toHaveLength(1);
    expect(paths.filter((path) => path.startsWith('/api/state'))).toHaveLength(1);
    expect(paths).not.toContain('/api/status');
    expect(paths).not.toContain('/api/monitoring/runtime');

    snapshot = { state: 'login_required', reason: '로그인 확인 실패', account: { error: {} }, tasks: { tasks: [] } };
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(screen.getByRole('img', { name: 'CGV: 실패' })).toBeTruthy();
    expect(screen.getByText('로그인 확인 실패')).toBeTruthy();
    snapshot = ready;
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(screen.getByRole('img', { name: 'CGV: 정상' })).toBeTruthy();
    expect(screen.queryByText('로그인 확인 실패')).toBeNull();
  });

  it('coalesces simultaneous refreshes and stops polling after unmount', async () => {
    let complete: ((value: Response) => void) | undefined;
    let runtimeRequests = 0;
    const fetchMock = vi.fn<typeof fetch>().mockImplementation(async (input) => {
      if (String(input) !== '/api/runtime') return localResponse(input);
      runtimeRequests++;
      if (runtimeRequests === 1) return new Promise<Response>((resolve) => { complete = resolve; });
      return response(ready);
    });
    vi.stubGlobal('fetch', fetchMock);
    const notify = vi.fn<(message: string) => void>();
    const notices = vi.fn<(user: string) => Promise<void>>().mockResolvedValue(undefined);
    const { result, unmount } = renderHook(() => useApplicationState(notify, notices));
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    let pending: Promise<void>[] = [];
    act(() => { pending = Array.from({ length: 10 }, () => result.current.pollStatus()); });
    expect(runtimeRequests).toBe(1);
    await act(async () => { complete?.(response(checking)); await Promise.all(pending); });
    expect(runtimeRequests).toBe(2);
    expect(result.current.runtime.account.state.case).toBe('authenticated');
    unmount();
    await act(async () => { await vi.advanceTimersByTimeAsync(15000); });
    expect(runtimeRequests).toBe(2);
  });
});
