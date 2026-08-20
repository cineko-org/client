import type { DesktopBridge } from './types';

export class APIError extends Error {
	constructor(message: string, readonly status: number) {
		super(message);
		this.name = 'APIError';
	}
}

export async function api<T>(path: string, options: Omit<RequestInit, 'body'> & { body?: unknown } = {}): Promise<T> {
  const headers = new Headers(options.headers);
  let body = options.body as BodyInit | null | undefined;
  if (options.body !== undefined) {
    headers.set('Content-Type', 'application/json');
    body = JSON.stringify(options.body);
  }
  const response = await fetch(path, { ...options, headers, body });
  const value = (await response.json().catch(() => ({}))) as T & { error?: string };
	if (!response.ok) throw new APIError(value.error || `요청 실패 (${response.status})`, response.status);
  return value;
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
	if (/proxy|soxy|socks/.test(normalized)) return '프록시 설정이나 연결 상태를 확인하세요.';
	if (/credential|authenticate|login|unauthorized/.test(normalized)) return '로그인에 실패했습니다. 로그인 정보를 확인하고 다시 시도하세요.';
	if (/update|release|artifact|download/.test(normalized)) return '업데이트를 완료하지 못했습니다. 네트워크 연결을 확인하세요.';
	if (/central|network|fetch|connect|dial|timeout/.test(normalized)) return 'Cineko 서비스에 연결할 수 없습니다. 잠시 후 다시 시도하세요.';
	return '요청을 처리하지 못했습니다. 잠시 후 다시 시도하세요.';
}
