import { create, toJson } from '@bufbuild/protobuf';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { api, errorMessage, watchProtoEvent } from '../src/api/client';
import { ResourceSchema } from '../src/api/proto';

afterEach(() => {
	vi.unstubAllGlobals();
	delete window.runtime;
});

describe('client error messages', () => {
	it('surfaces authentication-required execution failures distinctly', () => {
		expect(errorMessage(new Error('CGV authentication is required'))).toBe('CGV 로그인이 필요합니다. 로그인 후 예매 찾기를 다시 시작하세요.');
	});
});

describe('client HTTP logging', () => {
	it('propagates a request ID and logs raw fetch failures through Wails', async () => {
		const logError = vi.fn<(message: string) => void>();
		window.runtime = { LogInfo: logError, LogError: logError };
		vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockRejectedValue(new Error('local web server: connection refused')));

		await expect(api('/api/presets', ResourceSchema)).rejects.toThrow('connection refused');

		const attempted = JSON.parse(logError.mock.calls[0][0]) as Record<string, unknown>;
		const failed = JSON.parse(logError.mock.calls[1][0]) as Record<string, unknown>;
		expect(attempted.event).toBe('http.client.request.attempted');
		expect(failed.event).toBe('http.client.request.completed');
		expect(failed.ok).toBe(false);
		expect(failed.error).toContain('local web server: connection refused');
		expect(failed.request_id).toBe(attempted.request_id);
	});

	it('logs successful response status and byte counts', async () => {
		const logInfo = vi.fn<(message: string) => void>();
		window.runtime = { LogInfo: logInfo };
		const resource = create(ResourceSchema);
		const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(toJson(ResourceSchema, resource)), {
			status: 200,
			headers: { 'Content-Type': 'application/json', 'X-Request-Id': 'server-request-id' },
		}));
		vi.stubGlobal('fetch', fetchMock);

		await api('/api/presets', ResourceSchema);

		const requestHeaders = new Headers(fetchMock.mock.calls[0][1]?.headers);
		expect(requestHeaders.get('X-Request-Id')).toBeTruthy();
		const attempted = JSON.parse(logInfo.mock.calls[0][0]) as Record<string, unknown>;
		const completed = JSON.parse(logInfo.mock.calls[1][0]) as Record<string, unknown>;
		expect(completed.event).toBe('http.client.request.completed');
		expect(completed.status).toBe(200);
		expect(completed.response_bytes).toBeGreaterThan(0);
		expect(completed.request_id).toBe('server-request-id');
		expect(attempted.request_id).not.toBe('');
	});

	it('keeps raw response decode errors in the terminal completion event', async () => {
		const logError = vi.fn<(message: string) => void>();
		window.runtime = { LogError: logError };
		vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValue(new Response('{not-json', {
			status: 200,
			headers: { 'Content-Type': 'application/json' },
		})));

		await expect(api('/api/presets', ResourceSchema)).rejects.toThrow('요청 실패 (200)');

		const completed = JSON.parse(logError.mock.calls[logError.mock.calls.length - 1]?.[0] ?? '{}') as Record<string, unknown>;
		expect(completed.event).toBe('http.client.request.completed');
		expect(String(completed.error)).toMatch(/JSON|token|property/i);
	});

	it('correlates EventSource lifecycle and connection errors', () => {
		class TestEventSource {
			static instance: TestEventSource;
			readonly listeners = new Map<string, EventListener>();
			closed = false;
			constructor(readonly url: string) { TestEventSource.instance = this; }
			addEventListener(type: string, listener: EventListenerOrEventListenerObject) {
				this.listeners.set(type, typeof listener === 'function' ? listener : (event) => listener.handleEvent(event));
			}
			close() { this.closed = true; }
			emit(type: string) { this.listeners.get(type)?.(new Event(type)); }
		}
		const logInfo = vi.fn<(message: string) => void>();
		const logError = vi.fn<(message: string) => void>();
		window.runtime = { LogInfo: logInfo, LogError: logError };
		vi.stubGlobal('EventSource', TestEventSource);
		const onError = vi.fn<(error: Error) => void>();
		const stop = watchProtoEvent('/api/catalog/seat-map:watch?auditoriumId=a', 'cineko.seat-map', ResourceSchema, vi.fn(), onError);
		TestEventSource.instance.emit('error');
		stop();

		const attempted = JSON.parse(logInfo.mock.calls[0][0]) as Record<string, unknown>;
		const completed = JSON.parse(logError.mock.calls[0][0]) as Record<string, unknown>;
		expect(TestEventSource.instance.url).toMatch(/request_id=/);
		expect(attempted.event).toBe('http.client.request.attempted');
		expect(completed.event).toBe('http.client.request.completed');
		expect(completed.status).toBe(0);
		expect(completed.error).toContain('연결이 끊어졌습니다');
		expect(onError).toHaveBeenCalledOnce();
		expect(TestEventSource.instance.closed).toBe(true);
	});
});
