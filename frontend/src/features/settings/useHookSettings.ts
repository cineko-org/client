import { useCallback, useEffect, useState } from 'react';
import { create } from '@bufbuild/protobuf';
import { desktopBridge, errorMessage } from '../../api/client';
import { decodeDesktopProto, encodeDesktopProto } from '../../api/desktop';
import { SettingsSchema } from '../../api/proto';
import type { Notify } from '../../components/core/feedback';
import { useExclusiveOperation } from '../../shared/useExclusiveOperation';
import { hookForms, hookSettingsInput, newHookForm, type HookTargetForm } from './hookModel';
import type { SettingsLoadState } from './model';

export function useHookSettings(opened: boolean, notify: Notify) {
  const [forms, setForms] = useState<HookTargetForm[]>([]);
  const { active, start, isRunning } = useExclusiveOperation();
  const saving = active === 'save';
  const bridge = desktopBridge();
  const [loadState, setLoadState] = useState<SettingsLoadState>(bridge ? 'idle' : 'unavailable');

  const load = useCallback(async () => {
    if (!bridge) {
      setLoadState('unavailable');
      return;
    }
    const release = start('load');
    if (!release) return;
    setLoadState('loading');
    try {
	  const settings = decodeDesktopProto(SettingsSchema, await bridge.GetHookSettings());
	  setForms(hookForms(settings.webhooks));
      setLoadState('ready');
    } catch {
      setLoadState('error');
      notify('외부 알림 설정을 불러오지 못했습니다.', { tone: 'error' });
    } finally { release(); }
  }, [bridge, notify, start]);

  useEffect(() => {
    if (!opened) return undefined;
    let mounted = true;
    queueMicrotask(() => {
      if (mounted) void load();
    });
    return () => { mounted = false; };
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
    const release = start('save');
    if (!release) return;
    try {
	  const input = create(SettingsSchema, { webhooks: hookSettingsInput(forms) });
	  const saved = decodeDesktopProto(
	    SettingsSchema,
	    await bridge.SaveHookSettings(encodeDesktopProto(SettingsSchema, input)),
	  );
	  setForms(hookForms(saved.webhooks));
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
      release();
    }
  }, [bridge, forms, loadState, notify, start]);

  const add = useCallback(() => { if (!isRunning()) setForms((current) => [...current, newHookForm()]); }, [isRunning]);
  const change = useCallback((index: number, value: HookTargetForm) => {
    if (isRunning()) return;
    setForms((current) => current.map((item, itemIndex) => itemIndex === index ? value : item));
  }, [isRunning]);
  const remove = useCallback((index: number) => {
    if (isRunning()) return;
    setForms((current) => current.filter((_item, itemIndex) => itemIndex !== index));
  }, [isRunning]);

  return { forms, loadState, saving, available: Boolean(bridge), load, add, change, remove, save };
}
