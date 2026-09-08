import { useCallback, useEffect, useState } from 'react';
import { desktopBridge, errorMessage } from '../../api/client';
import { useExclusiveOperation } from '../../shared/useExclusiveOperation';

import { decodeScannerSettings, type ScannerForm, type ScannerState } from './scannerModel';

function scannerError(error: unknown): string {
  const message = error instanceof Error ? error.message : String(error);
  if (/HTTP 401/.test(message)) return 'SOXY 인증에 실패했습니다(401). API 토큰을 확인하세요.';
  if (/HTTP 403/.test(message)) return 'SOXY 접근 권한이 없습니다(403). API 토큰을 확인하세요.';
  return errorMessage(error);
}

export function useScannerSettings() {
  const bridge = desktopBridge();
  const available = Boolean(bridge?.GetScannerSettings && bridge?.SaveScannerSettings);
  const [form, updateForm] = useState<ScannerForm>({ url: '', token: '' });
  const [state, setState] = useState<ScannerState>({ phase: 'loading', hasToken: false, message: '' });
  const { start, isRunning } = useExclusiveOperation();
  const setForm = useCallback((next: ScannerForm) => {
    if (isRunning()) return;
    updateForm(next);
    setState(current => ({ ...current, message: '' }));
  }, [isRunning]);

  const load = useCallback(async () => {
    if (!bridge?.GetScannerSettings) return;
    const release = start('load');
    if (!release) return;
    setState(current => ({ ...current, phase: 'loading', message: '' }));
    try {
      const value = decodeScannerSettings(await bridge.GetScannerSettings());
      updateForm({ url: value.url, token: '' });
      setState({ phase: 'ready', hasToken: value.hasToken, message: '' });
    } catch (error) {
      setState(current => ({ ...current, phase: 'error', message: scannerError(error) }));
    } finally {
      release();
    }
  }, [bridge, start]);

  useEffect(() => {
    let active = true;
    queueMicrotask(() => { if (active) void load(); });
    return () => { active = false; };
  }, [load]);

  const save = useCallback(async () => {
    if (!bridge?.SaveScannerSettings) return;
    const release = start('save');
    if (!release) return;
    setState(current => ({ ...current, phase: 'saving', message: '' }));
    try {
      const value = decodeScannerSettings(await bridge.SaveScannerSettings(JSON.stringify(form)));
      updateForm({ url: value.url, token: '' });
      setState({ phase: 'ready', hasToken: value.hasToken, message: 'SOXY 연결을 확인하고 저장했습니다. 다음 조회부터 적용됩니다.' });
    } catch (error) {
      setState(current => ({ ...current, phase: 'error', message: scannerError(error) }));
    } finally {
      release();
    }
  }, [bridge, form, start]);
  return { available, form, state, setForm, load, save };
}
