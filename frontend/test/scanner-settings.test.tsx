import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { DesktopBridge } from '../src/api/desktop';
import { useScannerSettings } from '../src/features/settings/useScannerSettings';
import { decodeScannerSettings } from '../src/features/settings/scannerModel';

afterEach(() => { cleanup(); delete window.go; });

it.each(['null', 'false', '{}', '{"url":42,"hasToken":true}', '{"url":"","hasToken":"true"}'])('rejects malformed scanner settings: %s', payload => {
  expect(() => decodeScannerSettings(payload)).toThrow('SOXY 설정 응답');
});

it('keeps unavailable scanner settings distinct from a valid stored token', () => {
  expect(decodeScannerSettings('{"url":"","hasToken":false}')).toEqual({ url: '', hasToken: false });
});

it('loads Soxy without exposing the stored token and serializes save clicks', async () => {
  let finish: ((result: string) => void) | undefined;
  const resultJSON = JSON.stringify({ url: 'http://soxy.local:8080', hasToken: true });
  const save = vi.fn<(input: string) => Promise<string>>().mockImplementation(() => new Promise(resolve => { finish = resolve; }));
  window.go = { main: { DesktopApp: {
    GetScannerSettings: vi.fn<() => Promise<string>>().mockResolvedValue(resultJSON), SaveScannerSettings: save,
  } as unknown as DesktopBridge } };
  const { result } = renderHook(() => useScannerSettings());
  await waitFor(() => expect(result.current.state.phase).toBe('ready'));
  expect(result.current.form).toEqual({ url: 'http://soxy.local:8080', token: '' });
  act(() => { void result.current.save(); void result.current.save(); });
  expect(save).toHaveBeenCalledTimes(1);
  expect(JSON.parse(save.mock.calls[0][0])).toEqual(result.current.form);
  await act(async () => finish?.(resultJSON));
  expect(result.current.state.message).toContain('다음 조회부터 적용');
  expect(result.current.form.token).toBe('');
});

it('keeps an editable form after load or authentication failure', async () => {
  const save = vi.fn<(input: string) => Promise<string>>().mockRejectedValue(new Error('Soxy returned HTTP 401'));
  window.go = { main: { DesktopApp: {
    GetScannerSettings: vi.fn<() => Promise<string>>().mockRejectedValue(new Error('invalid configuration')), SaveScannerSettings: save,
  } as unknown as DesktopBridge } };
  const { result } = renderHook(() => useScannerSettings());
  await waitFor(() => expect(result.current.state.phase).toBe('error'));
  act(() => result.current.setForm({ url: 'http://soxy.local:8080', token: 'replacement' }));
  await act(async () => result.current.save());
  expect(result.current.state.phase).toBe('error');
  expect(result.current.state.message).toContain('401');
  expect(result.current.form.token).toBe('replacement');
});
