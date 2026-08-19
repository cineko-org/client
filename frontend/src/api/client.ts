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
  return error instanceof Error ? error.message : String(error);
}
