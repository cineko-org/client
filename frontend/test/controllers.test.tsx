import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { AppState, DesktopBridge, Monitor } from '../src/api/types';
import { emptyAppState } from '../src/features/application/model';
import { useApplicationState } from '../src/features/application/useApplicationState';
import { useMonitorCommands } from '../src/features/monitors/useMonitorCommands';
import { useMonitorEditor } from '../src/features/monitors/useMonitorEditor';
import { useNotifications } from '../src/features/notifications/useNotifications';
import { usePresetCatalog } from '../src/features/presets/usePresetCatalog';
import { useHookSettings } from '../src/features/settings/useHookSettings';
import { useNetworkSettings } from '../src/features/settings/useNetworkSettings';

afterEach(() => {
	vi.unstubAllGlobals();
	delete window.go;
});

const response = (value: unknown, status = 200) => new Response(JSON.stringify(value), {
	status,
	headers: { 'Content-Type': 'application/json' },
});

describe('monitor editor controller', () => {
	it('retries one monitor create command with the same idempotency key', async () => {
		const monitor = { id: 'command', userId: 'user', status: 'pending' } as Monitor;
		const fetchMock = vi.fn<typeof fetch>()
			.mockResolvedValueOnce(response({ error: 'temporary failure' }, 503))
			.mockResolvedValueOnce(response(monitor, 202));
		vi.stubGlobal('fetch', fetchMock);
		const reload = vi.fn<() => Promise<AppState>>().mockResolvedValue(emptyAppState);
		const notify = vi.fn<(message: string) => void>();
		const onSaved = vi.fn<() => void>();
		const { result } = renderHook(() => useMonitorEditor(
			emptyAppState, 'user', reload, notify, onSaved,
		));
		act(() => result.current.setForm({
			...result.current.form,
			movie: 'Movie', presetId: 'preset', dates: ['2026-08-20'],
		}));

		await act(async () => result.current.requestCreate());
		await act(async () => result.current.requestCreate());

		expect(fetchMock).toHaveBeenCalledTimes(2);
		const first = JSON.parse(String(fetchMock.mock.calls[0][1]?.body)) as { idempotencyKey: string };
		const second = JSON.parse(String(fetchMock.mock.calls[1][1]?.body)) as { idempotencyKey: string };
		expect(first.idempotencyKey).toBeTruthy();
		expect(second.idempotencyKey).toBe(first.idempotencyKey);
		expect(fetchMock.mock.calls[0][0]).toBe('/api/monitors');
		expect(onSaved).toHaveBeenCalledOnce();
	});
});

describe('application connection controller', () => {
	it('distinguishes unavailable from stale data and recovers on retry', async () => {
		const fetchMock = vi.fn<typeof fetch>().mockRejectedValue(new Error('central unavailable'));
		vi.stubGlobal('fetch', fetchMock);
		const notify = vi.fn<(message: string) => void>();
		const loadNotices = vi.fn<(userId: string) => Promise<void>>().mockResolvedValue(undefined);
		const { result } = renderHook(() => useApplicationState(notify, loadNotices));

		await waitFor(() => expect(result.current.connection.status).toBe('unavailable'));
		expect(result.current.connection.lastSuccessfulAt).toBe('');

		fetchMock.mockImplementation((input) => {
			const path = String(input);
			if (path.startsWith('/api/state')) return Promise.resolve(response(emptyAppState));
			if (path === '/api/status') return Promise.resolve(response({}));
			if (path === '/api/account') return Promise.resolve(response({ status: 'unauthenticated', authenticated: false }));
			return Promise.resolve(response([]));
		});
		await act(async () => result.current.retryConnection());
		await waitFor(() => expect(result.current.connection.status).toBe('ready'));
		expect(result.current.connection.lastSuccessfulAt).toBeTruthy();

		fetchMock.mockRejectedValueOnce(new Error('central timeout'));
		await act(async () => {
			await expect(result.current.reload()).rejects.toThrow('central timeout');
		});
		expect(result.current.connection.status).toBe('stale');
		expect(result.current.state).toEqual(emptyAppState);
	});
});

describe('monitor command controller', () => {
	it('requires acknowledgement for payment retries and locks concurrent mutations', async () => {
		const monitors = [
			{ id: 'triggered', userId: 'user', status: 'triggered' },
			{ id: 'pending', userId: 'user', status: 'pending' },
		] as Monitor[];
		let resolveRun: ((value: Response) => void) | undefined;
		const fetchMock = vi.fn<typeof fetch>(() => new Promise<Response>((resolve) => { resolveRun = resolve; }));
		vi.stubGlobal('fetch', fetchMock);
		const reload = vi.fn<() => Promise<AppState>>().mockResolvedValue(emptyAppState);
		const notify = vi.fn<(message: string) => void>();
		const { result } = renderHook(() => useMonitorCommands(monitors, 'user', reload, notify));

		act(() => result.current.retry('triggered'));
		expect(fetchMock).not.toHaveBeenCalled();
		expect(result.current.retryMonitor?.id).toBe('triggered');
		await act(async () => result.current.confirmRetry());
		expect(fetchMock).not.toHaveBeenCalled();

		act(() => result.current.setRetryAcknowledged(true));
		let retry: Promise<void>;
		act(() => { retry = result.current.confirmRetry(); });
		await waitFor(() => expect(result.current.mutationId).toBe('triggered'));
		act(() => result.current.retry('pending'));
		expect(fetchMock).toHaveBeenCalledTimes(1);
		resolveRun?.(response({ status: 'started' }, 202));
		await act(async () => retry!);
		expect(result.current.mutationId).toBeNull();
	});
});

describe('settings controllers', () => {
	it('does not save network or hook forms before stored settings finish loading', async () => {
		let resolveNetwork: ((value: { mode: 'direct' }) => void) | undefined;
		let resolveHooks: ((value: { targets: [] }) => void) | undefined;
		const bridge = {
			GetNetworkSettings: vi.fn<DesktopBridge['GetNetworkSettings']>(() => new Promise((resolve) => { resolveNetwork = resolve; })),
			SaveNetworkSettings: vi.fn<DesktopBridge['SaveNetworkSettings']>(),
			GetHookSettings: vi.fn<DesktopBridge['GetHookSettings']>(() => new Promise((resolve) => { resolveHooks = resolve; })),
			SaveHookSettings: vi.fn<DesktopBridge['SaveHookSettings']>(),
		} as unknown as DesktopBridge;
		window.go = { main: { DesktopApp: bridge } };
		const notify = vi.fn<(message: string) => void>();
		const { result } = renderHook(() => ({
			network: useNetworkSettings(true, notify),
			hooks: useHookSettings(true, notify),
		}));

		await waitFor(() => {
			expect(result.current.network.loadState).toBe('loading');
			expect(result.current.hooks.loadState).toBe('loading');
		});
		await act(async () => {
			expect(await result.current.network.save()).toBe(false);
			await result.current.hooks.save();
		});
		expect(bridge.SaveNetworkSettings).not.toHaveBeenCalled();
		expect(bridge.SaveHookSettings).not.toHaveBeenCalled();

		resolveNetwork?.({ mode: 'direct' });
		resolveHooks?.({ targets: [] });
		await waitFor(() => {
			expect(result.current.network.loadState).toBe('ready');
			expect(result.current.hooks.loadState).toBe('ready');
		});
	});
});

describe('notification controller', () => {
	it('loads durable events and persists read and clear actions', async () => {
		const fetchMock = vi.fn<typeof fetch>()
			.mockResolvedValueOnce(response([{
				id: 'event', userId: 'user', kind: 'monitor.completed', tone: 'success',
				message: 'done', createdAt: '2026-08-10T00:00:00Z',
			}]))
			.mockResolvedValue(response({ status: 'ok' }));
		vi.stubGlobal('fetch', fetchMock);
		const { result } = renderHook(() => useNotifications());

		await act(async () => result.current.load('user'));
		expect(result.current.notices).toEqual([expect.objectContaining({ id: 'event', read: false })]);
		act(() => result.current.markRead());
		expect(result.current.notices[0].read).toBe(true);
		act(() => result.current.clear());
		expect(result.current.notices).toEqual([]);
		expect(fetchMock.mock.calls.map(([path]) => path)).toEqual([
			'/api/events?user=user', '/api/events/read', '/api/events',
		]);
	});
});

describe('preset catalog controller', () => {
	it('aborts an older theater request before applying the newer selection', async () => {
		let call = 0;
		const fetchMock = vi.fn<typeof fetch>((_input, init) => {
			call++;
			if (call === 1) {
				return new Promise<Response>((_resolve, reject) => {
					init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
				});
			}
			return Promise.resolve(response([{ id: 'second', theaterId: 'theater-b', sourceKey: '서울/B/IMAX', name: 'IMAX', capacity: 1, seatMapVersion: '' }]));
		});
		vi.stubGlobal('fetch', fetchMock);
		const state: AppState = {
			...emptyAppState,
			catalog: {
				...emptyAppState.catalog,
				theaters: [
					{ id: 'theater-a', providerId: 'cgv', sourceKey: '서울/A', region: '서울', name: 'A' },
					{ id: 'theater-b', providerId: 'cgv', sourceKey: '서울/B', region: '서울', name: 'B' },
				],
			},
		};
		const reload = vi.fn<() => Promise<AppState>>().mockResolvedValue(state);
		const notify = vi.fn<(message: string) => void>();
		const { result } = renderHook(() => usePresetCatalog(state, reload, notify));
		act(() => result.current.setRegion('서울'));
		let first: Promise<void>;
		act(() => { first = result.current.setTheater('A'); });
		await act(async () => result.current.setTheater('B'));
		await act(async () => first!);
		expect(result.current.theater).toBe('B');
		expect(result.current.activeTheaterId).toBe('theater-b');
		expect(result.current.auditoriums.map((item) => item.id)).toEqual(['second']);
		expect(notify).not.toHaveBeenCalled();
	});

	it('clears catalog loading immediately when reset aborts discovery', async () => {
		const fetchMock = vi.fn<typeof fetch>((_input, init) => new Promise<Response>((_resolve, reject) => {
			init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
		}));
		vi.stubGlobal('fetch', fetchMock);
		const reload = vi.fn<() => Promise<AppState>>().mockResolvedValue(emptyAppState);
		const notify = vi.fn<(message: string) => void>();
		const { result } = renderHook(() => usePresetCatalog(emptyAppState, reload, notify));
		act(() => result.current.setRegion('서울'));
		await act(async () => result.current.setTheater('용산'));
		let discovery: Promise<void>;
		act(() => { discovery = result.current.discoverAuditoriums(); });
		await waitFor(() => expect(result.current.loadingCatalog).toBe(true));
		act(() => result.current.reset());
		expect(result.current.loadingCatalog).toBe(false);
		await act(async () => discovery!);
		expect(result.current.catalogMessage).toBe('');
		expect(notify).not.toHaveBeenCalled();
	});
});
