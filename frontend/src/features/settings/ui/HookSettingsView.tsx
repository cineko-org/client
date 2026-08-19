import { Anchor, Box, Divider, Group, Stack, Switch, Text } from '@mantine/core';
import { DangerButton, PrimaryButton, SecondaryButton } from '../../../components/core/Actions';
import { CheckField, PasswordField, SelectField, TextField } from '../../../components/core/Fields';
import {
  hookEventGroups, hookEventKinds, selectAllHookEvents, toggleHookEvent, type HookTargetForm,
} from '../hookModel';
import type { SettingsLoadState } from '../model';

interface HookSettingsViewProps {
  available: boolean;
  forms: HookTargetForm[];
  loadState: SettingsLoadState;
  saving: boolean;
  onAdd: () => void;
  onReload: () => void;
  onChange: (index: number, value: HookTargetForm) => void;
  onRemove: (index: number) => void;
  onSave: () => void;
}

const destinationGuides = {
  discord: {
    placeholder: 'https://discord.com/api/webhooks/…',
    instructions: 'Discord 서버 설정 → 연동 → 웹후크에서 새 웹후크를 만든 뒤 “웹후크 URL 복사”를 누르세요.',
    href: 'https://support.discord.com/hc/en-us/articles/228383668-Intro-to-Webhooks',
    linkLabel: 'Discord 웹후크 안내 열기',
  },
  slack: {
    placeholder: 'https://hooks.slack.com/services/…',
    instructions: 'Slack 앱에서 Incoming Webhooks를 켜고 “Add New Webhook to Workspace”로 채널을 고른 뒤 URL을 복사하세요.',
    href: 'https://api.slack.com/messaging/webhooks',
    linkLabel: 'Slack 웹후크 만들기 안내 열기',
  },
  webhook: {
    placeholder: 'https://…',
    instructions: '알림을 받을 서비스에서 제공한 HTTPS 주소를 입력하세요. 직접 만든 연동에만 사용하는 고급 기능입니다.',
  },
} as const;

export function HookSettingsView({
  available, forms, loadState, saving, onAdd, onReload, onChange, onRemove, onSave,
}: HookSettingsViewProps) {
  const editable = available && loadState === 'ready' && !saving;
  return (
    <Stack gap="md">
      <Group justify="space-between">
        <Text fw={600}>외부 알림</Text>
        <SecondaryButton onClick={onAdd} disabled={!editable}>알림 추가</SecondaryButton>
      </Group>
      <Text size="sm" c="dimmed">선택한 일이 생기면 Discord나 Slack으로 알려드립니다.</Text>
      <Text size="xs" c="dimmed">카카오톡은 복사해서 붙이는 알림 URL을 제공하지 않아 이 방식으로 연결할 수 없습니다.</Text>
      {loadState === 'loading' || loadState === 'idle' ? <Text size="sm" c="dimmed">저장된 외부 알림 설정을 불러오는 중입니다.</Text> : null}
      {loadState === 'error' ? (
        <Group justify="space-between">
          <Text size="sm" c="red">저장된 외부 알림 설정을 불러오지 못했습니다.</Text>
          <SecondaryButton size="xs" onClick={onReload}>다시 불러오기</SecondaryButton>
        </Group>
      ) : null}
      {loadState === 'ready' && forms.length === 0 ? <Text size="sm" c="dimmed">등록된 외부 알림이 없습니다.</Text> : null}
      {forms.map((form, index) => {
        const guide = destinationGuides[form.kind];
        const allEvents = form.eventKinds.length === 0;
        const selectedKnownEvents = form.eventKinds.filter((kind) => (
          hookEventKinds.includes(kind as typeof hookEventKinds[number])
        ));
        return (
          <Stack gap="sm" key={form.id}>
            {index > 0 ? <Divider /> : null}
            <Group grow align="flex-start">
              <TextField label="이름" value={form.name}
                onChange={(event) => onChange(index, { ...form, name: event.currentTarget.value })} disabled={!editable} />
              <SelectField label="받을 곳" data={[
                  { value: 'discord', label: 'Discord' },
                  { value: 'slack', label: 'Slack' },
                  { value: 'webhook', label: '직접 연결 (고급)' },
                ]} value={form.kind} allowDeselect={false}
                disabled={!editable}
                onChange={(value) => onChange(index, {
                  ...form, kind: (value || 'discord') as HookTargetForm['kind'], url: '',
                })} />
            </Group>
            <TextField label="알림 URL" description={guide.instructions} value={form.url} placeholder={guide.placeholder}
              onChange={(event) => onChange(index, { ...form, url: event.currentTarget.value })} disabled={!editable} />
            {'href' in guide ? (
              <Anchor size="sm" href={guide.href} target="_blank" rel="noreferrer">{guide.linkLabel}</Anchor>
            ) : null}
            {form.kind === 'webhook' ? <PasswordField label="보안 키"
              description="알림을 받는 서비스에서 별도로 요구할 때만 입력하세요."
              placeholder={form.hasSecret ? '저장된 값 유지' : '선택 사항'} value={form.secret}
              onChange={(event) => onChange(index, { ...form, secret: event.currentTarget.value })} disabled={!editable} /> : null}
            <Stack gap="xs">
              <Box>
                <Text size="sm" fw={500}>어떤 일이 생기면 알릴까요?</Text>
                <Text size="xs" c="dimmed">선택한 일이 생기면 이 알림으로 알려드립니다.</Text>
              </Box>
              <CheckField label="모든 알림 받기" checked={allEvents}
                disabled={!editable}
                onChange={(event) => onChange(index, {
                  ...form, eventKinds: selectAllHookEvents(event.currentTarget.checked),
                })} />
              {hookEventGroups.map((group) => (
                <Stack gap={6} pl="md" key={group.label}>
                  <Text size="xs" fw={600} c="dimmed">{group.label}</Text>
                  {group.events.map((event) => {
                    const checked = allEvents || form.eventKinds.includes(event.kind);
                    const lastSelected = !allEvents && checked && selectedKnownEvents.length === 1;
                    return (
                      <CheckField key={event.kind} label={event.label} checked={checked}
                        disabled={!editable || allEvents || lastSelected}
                        onChange={(changeEvent) => onChange(index, {
                          ...form,
                          eventKinds: toggleHookEvent(form.eventKinds, event.kind, changeEvent.currentTarget.checked),
                        })} />
                    );
                  })}
                </Stack>
              ))}
            </Stack>
            <Group justify="space-between">
              <Switch label="사용" checked={form.enabled}
                onChange={(event) => onChange(index, { ...form, enabled: event.currentTarget.checked })} disabled={!editable} />
              <DangerButton onClick={() => onRemove(index)} disabled={!editable}>삭제</DangerButton>
            </Group>
          </Stack>
        );
      })}
      <Group justify="flex-end">
        <PrimaryButton onClick={onSave} loading={saving} disabled={!editable}>알림 설정 저장</PrimaryButton>
      </Group>
    </Stack>
  );
}
