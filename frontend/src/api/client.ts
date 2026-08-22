import { fromJson, toJson, type JsonValue, type Message } from '@bufbuild/protobuf';
import type { GenMessage } from '@bufbuild/protobuf/codegenv2';
import { APIErrorResponseSchema, WatchSeatMapResponseSchema, type WatchSeatMapResponse } from './proto';
import type { DesktopBridge } from './desktop';

export class APIError extends Error {
	constructor(message: string, readonly status: number) {
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
	if ((requestSchema === undefined) !== (requestMessage === undefined)) {
		throw new TypeError('A generated request schema and message must be provided together.');
	}
	if (options.body !== undefined) {
		throw new TypeError('API request bodies must use a generated request schema and message.');
	}
	const body = requestSchema === undefined || requestMessage === undefined
		? undefined
		: JSON.stringify(toJson(requestSchema, requestMessage));
	if (requestSchema !== undefined) {
		headers.set('Content-Type', 'application/json');
	}
	const response = await fetch(path, { ...options, headers, body });
	const text = await response.text();
	let value: JsonValue = {};
	if (text.trim() !== '') {
		try {
			value = JSON.parse(text);
		} catch {
			throw new APIError(`요청 실패 (${response.status})`, response.status);
		}
	}
	if (!response.ok) {
		const error = fromJson(APIErrorResponseSchema, value, { ignoreUnknownFields: false });
		throw new APIError(error.error?.message || `요청 실패 (${response.status})`, response.status);
	}
	return fromJson(responseSchema, value, { ignoreUnknownFields: false });
}

/** Opens one typed server-sent event stream and rejects non-ProtoJSON data. */
export function watchProtoEvent<Response extends Message>(
	path: string,
	eventType: string,
	responseSchema: GenMessage<Response>,
	onMessage: (message: Response) => void,
	onError: (error: Error) => void,
): () => void {
	const source = new EventSource(path);
	source.addEventListener(eventType, (event) => {
		try {
			const value = JSON.parse(event.data) as JsonValue;
			onMessage(fromJson(responseSchema, value, { ignoreUnknownFields: false }));
		} catch {
			source.close();
			onError(new Error('Cineko가 올바르지 않은 좌석 배치 상태를 보냈습니다.'));
		}
	});
	source.addEventListener('error', () => onError(new Error('Cineko 좌석 배치 연결이 끊어졌습니다.')));
	return () => source.close();
}

/** Watches the generated seat-map response over Wails events or HTTP SSE. */
export function watchSeatMap(
	auditoriumId: string,
	onMessage: (message: WatchSeatMapResponse) => void,
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
				onMessage(fromJson(WatchSeatMapResponseSchema, value, { ignoreUnknownFields: false }));
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
		WatchSeatMapResponseSchema,
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
	if (/authentication(?: is)? required|login(?: is)? required/.test(normalized)) return 'CGV 로그인이 필요합니다. 로그인 후 모니터를 다시 실행하세요.';
	if (/proxy|soxy|socks/.test(normalized)) return '프록시 설정이나 연결 상태를 확인하세요.';
	if (/credential|authentication|authenticate|login|unauthorized/.test(normalized)) return '로그인에 실패했습니다. 로그인 정보를 확인하고 다시 시도하세요.';
	if (/update|release|artifact|download/.test(normalized)) return '업데이트를 완료하지 못했습니다. 네트워크 연결을 확인하세요.';
	if (/central|network|fetch|connect|dial|timeout/.test(normalized)) return 'Cineko 서비스에 연결할 수 없습니다. 잠시 후 다시 시도하세요.';
	return '요청을 처리하지 못했습니다. 잠시 후 다시 시도하세요.';
}
