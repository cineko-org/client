import { create } from '@bufbuild/protobuf';
import { MantineProvider } from '@mantine/core';
import { act, cleanup, render, renderHook, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { StatusIndicator } from '../src/components/core/StatusIndicator';
import { accountIndicatorState } from '../src/shared/application';
import { networkIndicatorState } from '../src/features/settings/model';
import { useNetworkSettings } from '../src/features/settings/useNetworkSettings';
import { encodeDesktopProto, type DesktopBridge } from '../src/api/desktop';
import { NetworkSettingsSchema, WebUIAccountStateSchema } from '../src/api/proto';
import { directNetwork, proxyNetwork } from '../src/stories/fixtures';

beforeEach(() => {
  vi.stubGlobal('matchMedia', vi.fn<(query: string) => MediaQueryList>().mockImplementation((query) => ({
    matches: false, media: query, onchange: null,
    addEventListener: () => undefined, removeEventListener: () => undefined,
    addListener: () => undefined, removeListener: () => undefined, dispatchEvent: () => true,
  })));
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); delete window.go; });

describe('service status lights', () => {
  it.each([
    ['checking', '확인 중', 'orange'], ['off', '꺼짐', '#000000'],
    ['failed', '실패', 'red'], ['ready', '정상', 'green'],
  ] as const)('renders %s with its specified color and accessible status', (state, label, color) => {
    render(<MantineProvider><StatusIndicator label="CGV" state={state} /></MantineProvider>);
    const light = screen.getByRole('img', { name: `CGV: ${label}` });
    expect(light.closest('[style*="--indicator-color"]')?.getAttribute('style')).toContain(color);
  });

  it('distinguishes checking, signed out, failure and authenticated CGV states', () => {
    const cases = ['checking', 'unauthenticated', 'error', 'authenticated'] as const;
    expect(cases.map((value) => accountIndicatorState(create(WebUIAccountStateSchema, { state: { case: value, value: {} } })))).toEqual(['checking', 'off', 'failed', 'ready']);
  });

  it('shows proxy checking and failures instead of falsely showing the default direct mode', () => {
    expect(networkIndicatorState(directNetwork, 'idle')).toBe('checking');
    expect(networkIndicatorState(directNetwork, 'loading')).toBe('checking');
    expect(networkIndicatorState(directNetwork, 'ready')).toBe('off');
    expect(networkIndicatorState(proxyNetwork, 'ready')).toBe('ready');
    expect(networkIndicatorState(create(NetworkSettingsSchema), 'ready')).toBe('failed');
    expect(networkIndicatorState(proxyNetwork, 'ready', true)).toBe('checking');
    expect(networkIndicatorState(proxyNetwork, 'error')).toBe('failed');
    expect(networkIndicatorState(proxyNetwork, 'ready', false, true)).toBe('failed');
  });

  it('loads proxy state on startup without opening settings and recovers from a failed save', async () => {
    let finishLoad: ((value: string) => void) | undefined;
    const get = vi.fn<() => Promise<string>>().mockImplementation(() => new Promise((resolve) => { finishLoad = resolve; }));
    const save = vi.fn<() => Promise<string>>()
      .mockRejectedValueOnce(new Error('proxy connection failed'))
      .mockResolvedValue(encodeDesktopProto(NetworkSettingsSchema, proxyNetwork));
    window.go = { main: { DesktopApp: { GetNetworkSettings: get, SaveNetworkSettings: save } as unknown as DesktopBridge } };
    const notify = vi.fn<(message: string) => void>();
    const { result, rerender } = renderHook(() => useNetworkSettings(false, notify));
    expect(result.current.indicatorState).toBe('checking');
    await waitFor(() => expect(get).toHaveBeenCalledOnce());
    await act(async () => finishLoad?.(encodeDesktopProto(NetworkSettingsSchema, proxyNetwork)));
    expect(result.current.indicatorState).toBe('ready');
    rerender();
    expect(get).toHaveBeenCalledOnce();
    await act(async () => { await result.current.save(); });
    expect(result.current.indicatorState).toBe('failed');
    await act(async () => { await result.current.save(); });
    expect(result.current.indicatorState).toBe('ready');
    expect(get).toHaveBeenCalledOnce();
  });
});
