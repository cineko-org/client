import { useCallback, useEffect, useState } from 'react';
import { desktopBridge, errorMessage } from '../../api/client';
import type { Notify } from '../../components/core/feedback';
import { hookForms, hookSettingsInput, newHookForm, type HookTargetForm } from './hookModel';
import type { SettingsLoadState } from './model';

export function useHookSettings(opened: boolean, notify: Notify) {
  const [forms, setForms] = useState<HookTargetForm[]>([]);
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
      setForms(hookForms(await bridge.GetHookSettings()));
      setLoadState('ready');
    } catch {
      setLoadState('error');
      notify('외부 알림 설정을 불러오지 못했습니다.', { tone: 'error' });
    }
  }, [bridge, notify]);

  useEffect(() => {
    if (opened) void load();
  }, [load, opened]);

  const save = useCallback(async () => {
    if (!bridge) {
      notify('외부 알림은 데스크톱 앱에서만 저장할 수 있습니다.', { tone: 'error' });
      return;
    }
    if (loadState !== 'ready') {
      notify('저장된 외부 알림 설정을 먼저 불러오세요.', { tone: 'error' });
      return;
    }
    setSaving(true);
    try {
      const value = await bridge.SaveHookSettings(hookSettingsInput(forms));
      setForms(hookForms(value));
      notify('외부 알림 설정을 저장했습니다.');
    } catch (error) {
      const message = errorMessage(error);
      if (message.includes('invalid Discord webhook URL')) {
        notify('Discord에서 복사한 웹후크 URL을 확인하세요.', { tone: 'error' });
      } else if (message.includes('invalid Slack webhook URL')) {
        notify('Slack에서 복사한 웹후크 URL을 확인하세요.', { tone: 'error' });
      } else if (message.includes('valid URL is required')) {
        notify('알림 URL을 확인하세요.', { tone: 'error' });
      } else if (message.includes('name is required')) {
        notify('알림 이름을 입력하세요.', { tone: 'error' });
      } else {
        notify('외부 알림 설정을 저장하지 못했습니다.', { tone: 'error' });
      }
    } finally {
      setSaving(false);
    }
  }, [bridge, forms, loadState, notify]);

  const add = useCallback(() => setForms((current) => [...current, newHookForm()]), []);
  const change = useCallback((index: number, value: HookTargetForm) => {
    setForms((current) => current.map((item, itemIndex) => itemIndex === index ? value : item));
  }, []);
  const remove = useCallback((index: number) => {
    setForms((current) => current.filter((_item, itemIndex) => itemIndex !== index));
  }, []);

  return { forms, loadState, saving, available: Boolean(bridge), load, add, change, remove, save };
}
