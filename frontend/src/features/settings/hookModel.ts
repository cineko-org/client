import type { HookSettings, HookSettingsInput, HookTargetInput } from '../../api/types';

export interface HookTargetForm extends HookTargetInput {
  hasSecret?: boolean;
}

export const hookEventGroups = [
  {
    label: '예매 모니터',
    events: [
      { kind: 'monitor.completed', label: '원하는 예매 조건을 찾았을 때' },
      { kind: 'monitor.failed', label: '예매 모니터에 문제가 생겼을 때' },
      { kind: 'monitor.stopped', label: '예매 모니터가 중지됐을 때' },
    ],
  },
  {
    label: '예매',
    events: [
      { kind: 'payment.expired', label: '결제 대기 시간이 끝났을 때' },
      { kind: 'reservation.cancelled', label: '예매 취소가 완료됐을 때' },
    ],
  },
] as const;

export const hookEventKinds = hookEventGroups.flatMap((group) => group.events.map((event) => event.kind));

export function hookForms(settings?: HookSettings): HookTargetForm[] {
  return (settings?.targets ?? []).map((target) => ({
    id: target.id, name: target.name, kind: target.kind, url: target.url,
    secret: '', eventKinds: [...target.eventKinds], enabled: target.enabled,
    hasSecret: target.hasSecret,
  }));
}

export function newHookForm(): HookTargetForm {
  return {
    id: crypto.randomUUID(), name: '', kind: 'discord', url: '', secret: '',
    eventKinds: [], enabled: true,
  };
}

export function hookSettingsInput(forms: HookTargetForm[]): HookSettingsInput {
  return {
    targets: forms.map(({ hasSecret: _hasSecret, eventKinds, ...target }) => ({
      ...target,
      name: target.name.trim(),
      url: target.url.trim(),
      eventKinds: [...new Set(eventKinds)],
    })),
  };
}

export function selectAllHookEvents(selected: boolean): string[] {
  return selected ? [] : [...hookEventKinds];
}

export function toggleHookEvent(eventKinds: string[], kind: string, selected: boolean): string[] {
  const next = selected
    ? [...eventKinds, kind]
    : eventKinds.filter((eventKind) => eventKind !== kind);
  return [...new Set(next)];
}
