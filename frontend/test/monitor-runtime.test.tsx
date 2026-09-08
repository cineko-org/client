import { create } from '@bufbuild/protobuf';
import { MantineProvider } from '@mantine/core';
import { cleanup, fireEvent, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MonitorSchema, MonitorStateSchema } from '../src/api/proto';
import { monitorPresentation, type MonitoringRuntime } from '../src/features/monitors/runtime';
import { readApplicationRuntime } from '../src/features/application/runtime';
import { MonitorListView } from '../src/features/monitors/ui/MonitorListView';
import { MonitorDetailPageView } from '../src/features/monitors/ui/MonitorDetailPageView';

beforeEach(() => {
  vi.stubGlobal('matchMedia', vi.fn().mockImplementation((query: string) => ({
    matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => true,
  })));
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

const monitor = create(MonitorSchema, {
  id: 'odyssey', movieTitle: '오디세이', seatCount: 2,
  state: create(MonitorStateSchema, { state: { case: 'pending', value: {} } }),
});

describe('actual monitoring status', () => {
  it.each(['stopped', 'checking', 'login_required', 'preparation_failed', 'scan_failed', 'rate_limited', 'unavailable'] as const)('does not show enabled intent as active while %s', (state) => {
    const display = monitorPresentation(monitor, { state, reason: 'blocked' });
    expect(display.active).toBe(false);
    expect(display.canStart).toBe(!['stopped', 'unavailable'].includes(state));
    expect(display.reason).toBe('blocked');
  });

  it('labels pending and executing separately, with stopped and payment states preserved', () => {
    const ready: MonitoringRuntime = { state: 'ready', reason: '' };
    expect(monitorPresentation(monitor, ready).label).toBe('신규 일정 감시 중');
    expect(monitorPresentation(monitor, ready).active).toBe(true);
    const running = create(MonitorSchema, { state: { state: { case: 'running', value: {} } } });
    expect(monitorPresentation(running, ready).label).toBe('좌석 확인 중');
    const stopped = create(MonitorSchema, { state: { state: { case: 'stopped', value: {} } } });
    expect(monitorPresentation(stopped, ready).active).toBe(false);
    const payment = create(MonitorSchema, { state: { state: { case: 'triggered', value: {} } } });
    expect(monitorPresentation(payment, { state: 'rate_limited', reason: 'Retry-After' }).label).toBe('결제 확인 필요');
  });

  it.each(['list', 'detail'])('%s can enable then stop without a restart, including a 429 wait', (view) => {
    const runtime: MonitoringRuntime = { state: 'rate_limited', reason: 'Retry-After' };
    const onStop = vi.fn<() => void>();
    const onRetry = vi.fn<() => void>();
    const props = { runtime, onStop, onRetry, onEdit: vi.fn<() => void>(), onToggleCancellationWatch: vi.fn<() => void>() };
    const stopped = create(MonitorSchema, { ...monitor, state: { state: { case: 'stopped', value: {} } } });
    const tree = (current = monitor) => <MantineProvider>{view === 'list'
      ? <MonitorListView {...props} monitors={[current]} mutationId={null} deleteId={null} onOpen={vi.fn<() => void>()} onDelete={vi.fn<() => void>()} onDeleteRequest={vi.fn<() => void>()} />
      : <MonitorDetailPageView {...props} monitor={current} mutating={false} onBack={vi.fn<() => void>()} />
    }</MantineProvider>;
    const result = render(tree(stopped));
    expect(result.getByText('중지됨')).not.toBeNull();
    const start = result.getByRole('button', { name: '켜기', exact: true }) as HTMLButtonElement;
    expect(start.disabled).toBe(false);
    fireEvent.click(start);
    expect(onRetry).toHaveBeenCalledOnce();
    result.rerender(tree());
    expect(result.getByText('요청 제한으로 휴식 중')).not.toBeNull();
    expect(result.queryByRole('button', { name: '켜기', exact: true })).toBeNull();
    fireEvent.click(result.getByRole('button', { name: '끄기', exact: true }));
    expect(onStop).toHaveBeenCalledOnce();
    result.rerender(tree(stopped));
    expect(result.getByText('중지됨')).not.toBeNull();
  });

  it('reads only the local snapshot and fails closed if the response is invalid', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ state: 'ready', reason: '', account: { authenticated: {} }, tasks: { tasks: [] } })));
    vi.stubGlobal('fetch', fetchMock);
    expect((await readApplicationRuntime(new AbortController().signal)).state).toBe('ready');
    expect(fetchMock).toHaveBeenCalledOnce();
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/runtime');
    fetchMock.mockResolvedValue(new Response('{}'));
    await expect(readApplicationRuntime(new AbortController().signal)).rejects.toThrow('invalid application runtime');
  });
});
