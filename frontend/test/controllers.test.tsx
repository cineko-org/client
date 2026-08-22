import { create, toJson, type Message } from '@bufbuild/protobuf';
import type { GenMessage } from '@bufbuild/protobuf/codegenv2';
import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { DesktopBridge } from '../src/api/desktop';
import { encodeDesktopProto } from '../src/api/desktop';
import {
	AppEventSchema, AuditoriumIdentitySchema, AuditoriumResponseSchema, AuditoriumSchema, CatalogIndexSchema,
	CgvAuditoriumIdentitySchema, CgvTheaterIdentitySchema, DirectNetworkSchema,
	MonitorSchema, MonitorStateSchema, NetworkSettingsSchema, ResolutionSchema,
	ResourceSchema, SettingsSchema, StateSchema as CollectionStateSchema, TheaterIdentitySchema, TheaterSchema, WebUIAccountStateSchema,
	WatchSeatMapResponseSchema, WebUIActionStatusSchema, WebUIResourceListSchema, WebUIStateSchema, WebUITaskStatusResponseSchema,
} from '../src/api/proto';
import type { WebUIState } from '../src/api/proto';
import { emptyAppState } from '../src/features/application/model';
import { useApplicationState } from '../src/features/application/useApplicationState';
import { useMonitorCommands } from '../src/features/monitors/useMonitorCommands';
import { useMonitorEditor } from '../src/features/monitors/useMonitorEditor';
import { useNotifications } from '../src/features/notifications/useNotifications';
import { usePresetCatalog } from '../src/features/presets/usePresetCatalog';
import { useHookSettings } from '../src/features/settings/useHookSettings';
import { useNetworkSettings } from '../src/features/settings/useNetworkSettings';

afterEach(() => {
	vi.useRealTimers();
	vi.unstubAllGlobals();
	delete window.go;
	delete window.runtime;
});

const response = (value: unknown, status = 200) => new Response(JSON.stringify(value), {
	status,
	headers: { 'Content-Type': 'application/json' },
});

const protoResponse = <T extends Message>(schema: GenMessage<T>, message: T, status = 200) => response(toJson(schema, message), status);

const testTheaterIdentity = (siteNo: string) => create(TheaterIdentitySchema, {
	provider: { case: 'cgv', value: create(CgvTheaterIdentitySchema, { siteNo }) },
});

const testAuditoriumIdentity = (siteNo: string, screenNo: string) => create(AuditoriumIdentitySchema, {
	provider: { case: 'cgv', value: create(CgvAuditoriumIdentitySchema, { siteNo, screenNo }) },
});

const emitSeatMapResponse = (source: { emit(type: string, data: string): void }, value: unknown) => {
	source.emit('cineko.seat-map', JSON.stringify(value));
};

const readySeatMapResponse = (auditoriumId: string, label = 'A1') => ({
	resolution: {
		snapshot: {
			id: `snapshot-${auditoriumId}`,
			auditoriumId,
			layoutHash: 'a'.repeat(64),
			capacity: 1,
			layout: { seats: [{ id: `seat-${auditoriumId}`, auditoriumId, label }] },
			observedAt: '2026-08-23T00:00:00Z',
		},
		state: { idle: {} },
	},
});

function ForbiddenEventSource() { throw new Error('EventSource must not be used'); }

describe('monitor editor controller', () => {
	it('retries one monitor create command with the same idempotency key', async () => {
		const monitor = create(MonitorSchema, {
			id: 'command', userId: 'user', movieId: 'movie', movieTitle: 'Movie', presetId: 'preset',
			state: create(MonitorStateSchema, { state: { case: 'pending', value: {} } }),
		});
		const resource = create(ResourceSchema, { resource: { case: 'monitor', value: monitor } });
		const fetchMock = vi.fn<typeof fetch>()
			.mockResolvedValueOnce(response({ error: { message: 'temporary failure' } }, 503))
			.mockResolvedValueOnce(protoResponse(ResourceSchema, resource, 202));
		vi.stubGlobal('fetch', fetchMock);
		const reload = vi.fn<() => Promise<WebUIState>>().mockResolvedValue(emptyAppState);
		const notify = vi.fn<(message: string) => void>();
		const onSaved = vi.fn<() => void>();
		const { result } = renderHook(() => useMonitorEditor(
			emptyAppState, 'user', reload, notify, onSaved,
		));
		act(() => result.current.setForm({
			...result.current.form,
			movieId: 'movie', movie: 'Movie', presetId: 'preset', dates: ['2026-08-20'],
		}));

		await act(async () => result.current.requestCreate());
		await act(async () => result.current.requestCreate());

		expect(fetchMock).toHaveBeenCalledTimes(2);
		const first = JSON.parse(String(fetchMock.mock.calls[0][1]?.body)) as { mutation?: { commandId?: string } };
		const second = JSON.parse(String(fetchMock.mock.calls[1][1]?.body)) as { mutation?: { commandId?: string } };
		expect(first.mutation?.commandId).toBeTruthy();
		expect(second.mutation?.commandId).toBe(first.mutation?.commandId);
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
			if (path.startsWith('/api/state')) return Promise.resolve(protoResponse(WebUIStateSchema, emptyAppState));
			if (path === '/api/status') return Promise.resolve(protoResponse(WebUITaskStatusResponseSchema, create(WebUITaskStatusResponseSchema)));
			if (path === '/api/account') return Promise.resolve(protoResponse(WebUIAccountStateSchema, create(WebUIAccountStateSchema, { state: { case: 'unauthenticated', value: {} } })));
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
		const triggered = create(MonitorSchema, {
			id: 'triggered', userId: 'user', state: create(MonitorStateSchema, { state: { case: 'triggered', value: {} } }),
		});
		const pending = create(MonitorSchema, {
			id: 'pending', userId: 'user', state: create(MonitorStateSchema, { state: { case: 'pending', value: {} } }),
		});
		const state = create(WebUIStateSchema, {
			userId: 'user',
			resources: [
				create(ResourceSchema, { resource: { case: 'monitor', value: triggered } }),
				create(ResourceSchema, { resource: { case: 'monitor', value: pending } }),
			],
		});
		let resolveRun: ((value: Response) => void) | undefined;
		const fetchMock = vi.fn<typeof fetch>(() => new Promise<Response>((resolve) => { resolveRun = resolve; }));
		vi.stubGlobal('fetch', fetchMock);
		const reload = vi.fn<() => Promise<WebUIState>>().mockResolvedValue(emptyAppState);
		const notify = vi.fn<(message: string) => void>();
		const { result } = renderHook(() => useMonitorCommands(state, 'user', reload, notify));

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
		resolveRun?.(protoResponse(WebUIActionStatusSchema, create(WebUIActionStatusSchema, { result: { case: 'started', value: {} } }), 202));
		await act(async () => retry!);
		expect(result.current.mutationId).toBeNull();
	});
});

describe('settings controllers', () => {
	it('does not save network or hook forms before stored settings finish loading', async () => {
		let resolveNetwork: ((value: string) => void) | undefined;
		let resolveHooks: ((value: string) => void) | undefined;
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

		resolveNetwork?.(encodeDesktopProto(NetworkSettingsSchema, create(NetworkSettingsSchema, {
			mode: { case: 'direct', value: create(DirectNetworkSchema) },
		})));
		resolveHooks?.(encodeDesktopProto(SettingsSchema, create(SettingsSchema)));
		await waitFor(() => {
			expect(result.current.network.loadState).toBe('ready');
			expect(result.current.hooks.loadState).toBe('ready');
		});
	});
});

describe('notification controller', () => {
	it('loads durable events and persists read and clear actions', async () => {
		const event = create(AppEventSchema, {
			id: 'event', userId: 'user', kind: 'monitor.completed', tone: { case: 'success', value: {} },
			message: 'done', createdAt: { seconds: 1_786_320_000n, nanos: 0 },
		});
		const events = create(WebUIResourceListSchema, {
			resources: [create(ResourceSchema, { resource: { case: 'appEvent', value: event } })],
		});
		const fetchMock = vi.fn<typeof fetch>()
			.mockResolvedValueOnce(protoResponse(WebUIResourceListSchema, events))
			.mockResolvedValue(protoResponse(WebUIActionStatusSchema, create(WebUIActionStatusSchema, { result: { case: 'completed', value: {} } })));
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
	class FakeEventSource {
		static instances: FakeEventSource[] = [];
		onerror: ((event: Event) => void) | null = null;
		readonly listeners = new Map<string, EventListener>();
		closed = false;

		constructor(readonly url: string) {
			FakeEventSource.instances.push(this);
		}

		addEventListener(type: string, listener: EventListenerOrEventListenerObject) {
			this.listeners.set(type, typeof listener === 'function' ? listener : (event) => listener.handleEvent(event));
		}

		close() { this.closed = true; }

		emit(type: string, data: string) {
			this.listeners.get(type)?.(new MessageEvent(type, { data }));
		}
	}

	it('aborts an older theater request before applying the newer selection', async () => {
		let call = 0;
		const fetchMock = vi.fn<typeof fetch>((_input, init) => {
			call++;
			if (call === 1) {
				return new Promise<Response>((_resolve, reject) => {
					init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
				});
			}
			return Promise.resolve(protoResponse(AuditoriumResponseSchema, create(AuditoriumResponseSchema, {
				auditoriums: [create(AuditoriumSchema, { id: 'second', theaterId: 'theater-b', identity: testAuditoriumIdentity('2', '1'), name: 'IMAX', capacity: 1 })],
			})));
		});
		vi.stubGlobal('fetch', fetchMock);
		const state = create(WebUIStateSchema, {
			userId: 'user',
			catalog: create(CatalogIndexSchema, {
				theaters: [
					create(TheaterSchema, { id: 'theater-a', providerId: 'cgv', identity: testTheaterIdentity('1'), region: '서울', name: 'A' }),
					create(TheaterSchema, { id: 'theater-b', providerId: 'cgv', identity: testTheaterIdentity('2'), region: '서울', name: 'B' }),
				],
			}),
		});
		const notify = vi.fn<(message: string) => void>();
		const { result } = renderHook(() => usePresetCatalog(state, notify));
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

	it('uses one typed stream for auditorium state without client-side polling', async () => {
		FakeEventSource.instances = [];
		vi.stubGlobal('EventSource', FakeEventSource);
		const notify = vi.fn<(message: string) => void>();
		const state = create(WebUIStateSchema, {
			userId: 'user',
			catalog: create(CatalogIndexSchema, {
				auditoriums: [create(AuditoriumSchema, {
					id: 'auditorium-1', theaterId: 'theater-1',
					identity: testAuditoriumIdentity('1', '1'), name: 'IMAX', capacity: 1,
					currentLayoutHash: 'catalog-hash',
				})],
			}),
		});
		const { result } = renderHook(() => usePresetCatalog(state, notify));
		let pending: Promise<void>;
		act(() => { pending = result.current.setAuditorium('auditorium-1'); });
		const source = FakeEventSource.instances[0];
		expect(source.url).toBe('/api/catalog/seat-map:watch?auditoriumId=auditorium-1');
		const resolution = create(ResolutionSchema, {
			state: create(CollectionStateSchema, { state: { case: 'queued', value: {} } }),
		});
		act(() => source.emit('cineko.seat-map', JSON.stringify(toJson(
			WatchSeatMapResponseSchema,
			create(WatchSeatMapResponseSchema, { resolution }),
		))));
		await act(async () => pending!);
		expect(FakeEventSource.instances).toHaveLength(1);
		expect(result.current.catalogMessage).toBe('좌석 배치 수집을 기다리고 있습니다.');
		expect(result.current.seatMap).toBeNull();
		expect(notify).not.toHaveBeenCalled();
	});

	it('renders a ready snapshot and rejects an idle state without one', async () => {
		FakeEventSource.instances = [];
		vi.stubGlobal('EventSource', FakeEventSource);
		const { result } = renderHook(() => usePresetCatalog(create(WebUIStateSchema), vi.fn()));
		let ready: Promise<void>;
		act(() => { ready = result.current.setAuditorium('ready'); });
		act(() => emitSeatMapResponse(FakeEventSource.instances[0], readySeatMapResponse('ready')));
		await act(async () => ready!);
		expect(result.current.seatMap?.layout?.seats[0].label).toBe('A1');
		expect(result.current.catalogMessage).toBe('저장된 좌석 배치를 불러왔습니다.');

		let invalid: Promise<void>;
		act(() => { invalid = result.current.setAuditorium('invalid'); });
		const invalidSource = FakeEventSource.instances[1];
		act(() => emitSeatMapResponse(invalidSource, { resolution: { state: { idle: {} } } }));
		await act(async () => invalid!);
		expect(invalidSource.closed).toBe(true);
		expect(result.current.seatMap).toBeNull();
		expect(result.current.catalogMessage).toBe('Cineko가 올바르지 않은 좌석 배치 상태를 보냈습니다.');
		expect(result.current.loadingCatalog).toBe(false);
	});

	it('does not apply an event queued by a closed older stream', async () => {
		FakeEventSource.instances = [];
		vi.stubGlobal('EventSource', FakeEventSource);
		const { result } = renderHook(() => usePresetCatalog(create(WebUIStateSchema), vi.fn()));
		let first: Promise<void>;
		act(() => { first = result.current.setAuditorium('first'); });
		const firstSource = FakeEventSource.instances[0];
		act(() => emitSeatMapResponse(firstSource, {
			resolution: { state: { queued: { queuedAt: '2026-08-23T00:00:00Z', trigger: { operatorRequest: {} } } } },
		}));
		await act(async () => first!);

		let second: Promise<void>;
		act(() => { second = result.current.setAuditorium('second'); });
		const secondSource = FakeEventSource.instances[1];
		expect(firstSource.closed).toBe(true);
		act(() => emitSeatMapResponse(firstSource, readySeatMapResponse('first', 'OLD')));
		expect(result.current.seatMap).toBeNull();
		act(() => emitSeatMapResponse(secondSource, readySeatMapResponse('second', 'NEW')));
		await act(async () => second!);
		expect(result.current.seatMap?.auditoriumId).toBe('second');
		expect(result.current.seatMap?.layout?.seats[0].label).toBe('NEW');
	});

	it('uses the Wails runtime bridge instead of EventSource on desktop', async () => {
		const listeners = new Map<string, (...args: unknown[]) => void>();
		const bridge = {
			WatchSeatMap: vi.fn<DesktopBridge['WatchSeatMap']>().mockResolvedValue(undefined),
			StopSeatMapWatch: vi.fn<DesktopBridge['StopSeatMapWatch']>().mockResolvedValue(undefined),
		} as unknown as DesktopBridge;
		window.go = { main: { DesktopApp: bridge } };
		window.runtime = { EventsOn: (name, callback) => {
			listeners.set(name, callback);
			return () => { listeners.delete(name); };
		} };
		vi.stubGlobal('EventSource', ForbiddenEventSource);
		const { result, unmount } = renderHook(() => usePresetCatalog(create(WebUIStateSchema), vi.fn()));
		let pending: Promise<void>;
		act(() => { pending = result.current.setAuditorium('desktop'); });
		expect(bridge.WatchSeatMap).toHaveBeenCalledWith('desktop');
		act(() => listeners.get('cineko.seat-map')?.(JSON.stringify(readySeatMapResponse('desktop'))));
		await act(async () => pending!);
		expect(result.current.seatMap?.auditoriumId).toBe('desktop');
		expect(result.current.catalogMessage).toBe('저장된 좌석 배치를 불러왔습니다.');
		unmount();
		await waitFor(() => expect(bridge.StopSeatMapWatch).toHaveBeenCalledOnce());
	});

});
