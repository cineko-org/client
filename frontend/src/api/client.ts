import { fromJson, toJson, type JsonValue, type Message } from '@bufbuild/protobuf';
import type { GenMessage } from '@bufbuild/protobuf/codegenv2';
import { APIErrorResponseSchema, WebUISeatMapResponseSchema, type WebUISeatMapResponse } from './proto';
import type { DesktopBridge } from './desktop';

const requestIDHeader = 'X-Request-Id';

type ClientLogLevel = 'info' | 'warn' | 'error';

function now(): number {
	return typeof performance !== 'undefined' && typeof performance.now === 'function' ? performance.now() : Date.now();
}

function routePath(path: string): string {
	try {
		return new URL(path, typeof window !== 'undefined' ? window.location.href : 'http://cineko.local').pathname;
	} catch {
		return path.split('?')[0] || path;
	}
}

function withRequestID(path: string, requestID: string): string {
	return `${path}${path.includes('?') ? '&' : '?'}request_id=${encodeURIComponent(requestID)}`;
}

function byteLength(value: string | undefined): number | undefined {
	if (value === undefined) return undefined;
	return typeof TextEncoder === 'function' ? new TextEncoder().encode(value).byteLength : value.length;
}

function rawError(error: unknown): string {
	if (error instanceof Error) return error.stack || error.message;
	if (typeof error === 'string') return error;
	try {
		return JSON.stringify(error);
	} catch {
		return String(error);
	}
}

export function createRequestID(): string {
	if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') return crypto.randomUUID();
	return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
}

/** Emits the same JSON event shape to Wails' terminal logger and web console. */
export function logClientEvent(
	level: ClientLogLevel,
	event: string,
	fields: Record<string, unknown> = {},
): void {
	const message = JSON.stringify({ service: 'client', event, ...fields });
	const logger = level === 'error'
		? window.runtime?.LogError
		: level === 'warn' ? window.runtime?.LogWarning : window.runtime?.LogInfo;
	if (typeof logger === 'function') {
		logger(message);
	} else if (level === 'error') {
		console.error(message);
	} else if (level === 'warn') {
		console.warn(message);
	} else {
		console.info(message);
	}
	if (level === 'info') return;
	const { scenario = '', operation = '', expected = '', observed = '', ...details } = fields;
	const payload = JSON.stringify({
		level, event,
		scenario: String(scenario), operation: String(operation),
		expected: String(expected), observed: String(observed),
		fields: details,
	});
	const bridge = desktopBridge();
	if (bridge && typeof bridge.RecordClientLog === 'function') {
		void bridge.RecordClientLog(payload).catch(() => undefined);
		return;
	}
	if (typeof navigator !== 'undefined' && typeof navigator.sendBeacon === 'function') {
		navigator.sendBeacon(`/api/logs/client?request_id=${encodeURIComponent(createRequestID())}`, new Blob([payload], { type: 'application/json' }));
	}
}

export class APIError extends Error {
	constructor(message: string, readonly status: number, readonly diagnosticCause?: unknown) {
		super(message);
		this.name = 'APIError';
	}
}

export async function api<Response extends Message, Request extends Message = never>(
	path: string,
	responseSchema: GenMessage<Response>,
	options: RequestInit = {},
	requestSchema?: GenMessage<Request>,
	requestMessage?: Request,
): Promise<Response> {
	const headers = new Headers(options.headers);
	const requestID = headers.get(requestIDHeader) || createRequestID();
	const method = (options.method || 'GET').toUpperCase();
	const route = routePath(path);
	const started = now();
	let requestBytes: number | undefined;
	let responseBytes: number | undefined;
	let responseStatus: number | undefined;
	let requestBody: string | undefined;
	let responseBody: string | undefined;
	let attempted = false;
	try {
		if ((requestSchema === undefined) !== (requestMessage === undefined)) {
			throw new TypeError('A generated request schema and message must be provided together.');
		}
		if (options.body !== undefined) {
			throw new TypeError('API request bodies must use a generated request schema and message.');
		}
		requestBody = requestSchema === undefined || requestMessage === undefined
			? undefined
			: JSON.stringify(toJson(requestSchema, requestMessage));
		requestBytes = byteLength(requestBody);
		if (requestSchema !== undefined) {
			headers.set('Content-Type', 'application/json');
		}
		headers.set(requestIDHeader, requestID);
		logClientEvent('info', 'http.client.request.attempted', {
			request_id: requestID, method, route, path: route,
			request_bytes: requestBytes, request_body: requestBody,
		});
		attempted = true;
		const response = await fetch(path, { ...options, method, headers, body: requestBody });
		responseStatus = response.status;
		responseBody = await response.text();
		responseBytes = byteLength(responseBody);
		let value: JsonValue = {};
		if (responseBody.trim() !== '') {
			try {
				value = JSON.parse(responseBody);
			} catch (error) {
				throw new APIError(`요청 실패 (${response.status})`, response.status, error);
			}
		}
		if (!response.ok) {
			const error = fromJson(APIErrorResponseSchema, value, { ignoreUnknownFields: false });
			throw new APIError(error.error?.message || `요청 실패 (${response.status})`, response.status);
		}
		const result = fromJson(responseSchema, value, { ignoreUnknownFields: false });
		logClientEvent('info', 'http.client.request.completed', {
			request_id: response.headers.get(requestIDHeader) || requestID,
			method, route, path: route, status: response.status,
			duration_ms: now() - started, request_bytes: requestBytes, response_bytes: responseBytes,
		});
		return result;
	} catch (error) {
		const diagnosticError = error instanceof APIError && error.diagnosticCause !== undefined
			? error.diagnosticCause
			: error;
		logClientEvent('error', 'http.client.request.completed', {
			request_id: requestID, method, route, path: route, status: responseStatus ?? 0,
			duration_ms: now() - started, request_bytes: requestBytes, response_bytes: responseBytes,
			request_body: requestBody, response_body: responseBody,
			ok: false, phase: attempted ? 'request' : 'validation',
			error_name: error instanceof Error ? error.name : typeof error,
			error_message: error instanceof Error ? error.message : String(error),
			error: rawError(diagnosticError),
		});
		throw error;
	}
}

/** Opens one typed server-sent event stream and rejects non-ProtoJSON data. */
export function watchProtoEvent<Response extends Message>(
	path: string,
	eventType: string,
	responseSchema: GenMessage<Response>,
	onMessage: (message: Response) => void,
	onError: (error: Error) => void,
): () => void {
	const requestID = createRequestID();
	const route = routePath(path);
	const started = now();
	let responseBytes = 0;
	let completed = false;
	const finish = (status: number, error?: unknown) => {
		if (completed) return;
		completed = true;
		logClientEvent(error === undefined && status < 400 ? 'info' : 'error', 'http.client.request.completed', {
			request_id: requestID, method: 'GET', route, path: route, status,
			duration_ms: now() - started, response_bytes: responseBytes,
			error: error === undefined ? undefined : rawError(error),
		});
	};
	logClientEvent('info', 'http.client.request.attempted', {
		request_id: requestID, method: 'GET', route, path: route,
	});
	let source: EventSource;
	try {
		source = new EventSource(withRequestID(path, requestID));
	} catch (error) {
		finish(0, error);
		throw error;
	}
	source.addEventListener(eventType, (event) => {
		try {
			responseBytes += byteLength(event.data) ?? 0;
			const value = JSON.parse(event.data) as JsonValue;
			onMessage(fromJson(responseSchema, value, { ignoreUnknownFields: false }));
		} catch (error) {
			source.close();
			finish(200, error);
			onError(new Error('Cineko가 올바르지 않은 좌석 배치 상태를 보냈습니다.'));
		}
	});
	source.addEventListener('error', () => {
		const error = new Error('Cineko 좌석 배치 연결이 끊어졌습니다.');
		finish(0, error);
		onError(error);
	});
	return () => {
		source.close();
		finish(200);
	};
}

/** Watches the generated seat-map response over Wails events or HTTP SSE. */
export function watchSeatMap(
	auditoriumId: string,
	onMessage: (message: WebUISeatMapResponse) => void,
	onError: (error: Error) => void,
): () => void {
	const bridge = desktopBridge();
	const eventsOn = window.runtime?.EventsOn;
	if (bridge && eventsOn && typeof bridge.WatchSeatMap === 'function' && typeof bridge.StopSeatMapWatch === 'function') {
		let stopped = false;
		let unsubscribeMessage: (() => void) | undefined;
		let unsubscribeError: (() => void) | undefined;
		const stop = () => {
			if (stopped) return;
			stopped = true;
			unsubscribeMessage?.();
			unsubscribeError?.();
			void bridge.StopSeatMapWatch().catch(() => undefined);
		};
		unsubscribeMessage = eventsOn('cineko.seat-map', (...args) => {
			if (stopped) return;
			try {
				if (typeof args[0] !== 'string') throw new TypeError('seat-map payload must be ProtoJSON');
				const value = JSON.parse(args[0]) as JsonValue;
				onMessage(fromJson(WebUISeatMapResponseSchema, value, { ignoreUnknownFields: false }));
			} catch {
				stop();
				onError(new Error('Cineko가 올바르지 않은 좌석 배치 상태를 보냈습니다.'));
			}
		});
		unsubscribeError = eventsOn('cineko.seat-map.error', () => {
			if (!stopped) onError(new Error('Cineko 좌석 배치 연결이 끊어졌습니다.'));
		});
		void bridge.WatchSeatMap(auditoriumId).catch(() => {
			if (!stopped) {
				stop();
				onError(new Error('Cineko 좌석 배치 연결을 시작하지 못했습니다.'));
			}
		});
		return stop;
	}
	return watchProtoEvent(
		`/api/catalog/seat-map:watch?auditoriumId=${encodeURIComponent(auditoriumId)}`,
		'cineko.seat-map',
		WebUISeatMapResponseSchema,
		onMessage,
		onError,
	);
}

export function isRevisionConflict(error: unknown): boolean {
	return error instanceof APIError && error.status === 409;
}

export function desktopBridge(): DesktopBridge | null {
	return window.go?.main?.DesktopApp ?? null;
}

export function errorMessage(error: unknown): string {
	const message = error instanceof Error ? error.message : String(error);
	if (/[가-힣]/.test(message) && !/(https?:\/\/|\/v1\/|\/api\/|dial tcp|status code|HTTP \d)/i.test(message)) {
		return message;
	}
	if (error instanceof APIError) {
		if (error.status === 401 || error.status === 403) return '연결 인증이 만료되었습니다. 앱을 다시 시작하세요.';
		if (error.status === 409) return '다른 변경사항이 먼저 저장되었습니다. 새로고침 후 다시 시도하세요.';
		if (error.status === 429) return '요청이 많습니다. 잠시 후 다시 시도하세요.';
	}
	const normalized = message.toLowerCase();
	if (/authentication(?: is)? required|login(?: is)? required/.test(normalized)) return 'CGV 로그인이 필요합니다. 로그인 후 예매 찾기를 다시 시작하세요.';
	if (/proxy|soxy|socks/.test(normalized)) return '프록시 설정이나 연결 상태를 확인하세요.';
	if (/credential|authentication|authenticate|login|unauthorized/.test(normalized)) return '로그인에 실패했습니다. 로그인 정보를 확인하고 다시 시도하세요.';
	if (/update|release|artifact|download/.test(normalized)) return '업데이트를 완료하지 못했습니다. 네트워크 연결을 확인하세요.';
	if (/network|fetch|connect|dial|timeout/.test(normalized)) return 'Cineko 로컬 서비스에 연결할 수 없습니다. 잠시 후 다시 시도하세요.';
	return '요청을 처리하지 못했습니다. 잠시 후 다시 시도하세요.';
}
