import { useCallback, useEffect, useRef, useState } from 'react';
import { create } from '@bufbuild/protobuf';
import { desktopBridge, errorMessage } from '../../api/client';
import { decodeDesktopProto, encodeDesktopProto } from '../../api/desktop';
import { DirectNetworkSchema, NetworkSettingsSchema, type NetworkSettings } from '../../api/proto';
import type { Notify } from '../../components/core/feedback';
import { useExclusiveOperation } from '../../shared/useExclusiveOperation';
import {
  networkForm, networkIndicatorState, networkSettingsInput, type NetworkForm, type SettingsLoadState,
} from './model';

type NetworkPhase = 'unavailable' | 'idle' | 'loading' | 'ready' | 'load_error' | 'saving' | 'save_error';
interface NetworkState { phase: NetworkPhase; settings: NetworkSettings }

export function useNetworkSettings(opened: boolean, notify: Notify) {
  const bridge = desktopBridge();
  const [state, setState] = useState<NetworkState>(() => ({
    phase: bridge ? 'idle' : 'unavailable',
    settings: create(NetworkSettingsSchema, { mode: { case: 'direct', value: create(DirectNetworkSchema) } }),
  }));
  const [form, updateForm] = useState<NetworkForm>(networkForm());
  const { start, isRunning } = useExclusiveOperation();
  const setForm = useCallback((next: NetworkForm) => { if (!isRunning()) updateForm(next); }, [isRunning]);
  const initialized = useRef(false);
  const saving = state.phase === 'saving';
  const loadState: SettingsLoadState = state.phase === 'load_error' ? 'error'
    : state.phase === 'saving' || state.phase === 'save_error' ? 'ready' : state.phase;

  const load = useCallback(async () => {
    if (!bridge) {
      setState((current) => ({ ...current, phase: 'unavailable' }));
      return;
    }
    const release = start('load');
    if (!release) return;
    setState((current) => ({ ...current, phase: 'loading' }));
    try {
	  const value = decodeDesktopProto(NetworkSettingsSchema, await bridge.GetNetworkSettings());
      setState({ settings: value, phase: 'ready' });
      updateForm(networkForm(value));
    } catch (error) {
      setState((current) => ({ ...current, phase: 'load_error' }));
      notify(errorMessage(error), { tone: 'error' });
    } finally { release(); }
  }, [bridge, notify, start]);

  useEffect(() => {
    if (!opened && initialized.current) return undefined;
    let active = true;
    queueMicrotask(() => {
      if (active) {
        initialized.current = true;
        void load();
      }
    });
    return () => { active = false; };
  }, [load, opened]);

  const save = useCallback(async (next: NetworkForm = form) => {
    if (!bridge) {
      notify('데스크톱 앱에서만 연결 설정을 저장할 수 있습니다.', { tone: 'error' });
      return false;
    }
    if (loadState !== 'ready' || saving) {
      notify('저장된 연결 설정을 먼저 불러오세요.', { tone: 'error' });
      return false;
    }
    const release = start('save');
    if (!release) return false;
    setState((current) => ({ ...current, phase: 'saving' }));
    try {
	  const input = networkSettingsInput(next);
	  const value = decodeDesktopProto(
	    NetworkSettingsSchema,
	    await bridge.SaveNetworkSettings(encodeDesktopProto(NetworkSettingsSchema, input)),
	  );
      setState({ settings: value, phase: 'ready' });
      updateForm(networkForm(value));
      notify(value.mode.case === 'direct' ? '프록시를 사용하지 않습니다.' : '프록시 연결을 확인하고 저장했습니다.');
      return true;
    } catch (error) {
      setState((current) => ({ ...current, phase: 'save_error' }));
      notify(errorMessage(error), { tone: 'error' });
      return false;
    } finally { release(); }
  }, [bridge, form, loadState, notify, saving, start]);

  return { bridgeAvailable: Boolean(bridge), settings: state.settings, form, setForm, loadState, saving, load, save,
    indicatorState: networkIndicatorState(state.settings, loadState, saving, state.phase === 'save_error') };
}
