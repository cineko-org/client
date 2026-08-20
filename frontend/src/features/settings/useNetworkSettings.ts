import { useCallback, useEffect, useState } from 'react';
import { desktopBridge, errorMessage } from '../../api/client';
import type { NetworkSettings } from '../../api/types';
import type { Notify } from '../../components/core/feedback';
import {
  networkForm, networkSettingsInput, type NetworkForm, type SettingsLoadState,
} from './model';

export function useNetworkSettings(opened: boolean, notify: Notify) {
  const [settings, setSettings] = useState<NetworkSettings>({ mode: 'direct' });
  const [form, setForm] = useState<NetworkForm>(networkForm());
  const [saving, setSaving] = useState(false);
  const bridge = desktopBridge();
  const [loadState, setLoadState] = useState<SettingsLoadState>(bridge ? 'idle' : 'unavailable');

  const load = useCallback(async () => {
    if (!bridge) {
      setLoadState('unavailable');
      return;
    }
    setLoadState('loading');
    try {
      const value = await bridge.GetNetworkSettings();
      setSettings(value);
      setForm(networkForm(value));
      setLoadState('ready');
    } catch (error) {
      setLoadState('error');
      notify(errorMessage(error), { tone: 'error' });
    }
  }, [bridge, notify]);

  useEffect(() => {
    if (!opened) return undefined;
    let active = true;
    queueMicrotask(() => {
      if (active) void load();
    });
    return () => { active = false; };
  }, [load, opened]);

  const save = useCallback(async (next: NetworkForm = form) => {
    if (!bridge) {
      notify('데스크톱 앱에서만 연결 설정을 저장할 수 있습니다.', { tone: 'error' });
      return false;
    }
    if (loadState !== 'ready') {
      notify('저장된 연결 설정을 먼저 불러오세요.', { tone: 'error' });
      return false;
    }
    setSaving(true);
    try {
      const value = await bridge.SaveNetworkSettings(networkSettingsInput(next));
      setSettings(value);
      setForm(networkForm(value));
      notify(value.mode === 'direct' ? '프록시를 사용하지 않습니다.' : '프록시 연결을 확인하고 저장했습니다.');
      return true;
    } catch (error) {
      notify(errorMessage(error), { tone: 'error' });
      return false;
    } finally {
      setSaving(false);
    }
  }, [bridge, form, loadState, notify]);

  return { bridgeAvailable: Boolean(bridge), settings, form, setForm, loadState, saving, load, save };
}
