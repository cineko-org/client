import { Group, Stack, Text } from '@mantine/core';
import { PrimaryButton, SecondaryButton } from '../../../components/core/Actions';
import { PasswordField, SelectField, TextAreaField, TextField } from '../../../components/core/Fields';
import type { NetworkSettings } from '../../../api/proto';
import { networkUsageDescription, type NetworkForm, type SettingsLoadState } from '../model';

export interface ProxySettingsViewProps {
  available: boolean;
  settings: NetworkSettings;
  form: NetworkForm;
  loadState: SettingsLoadState;
  saving: boolean;
  onChange: (form: NetworkForm) => void;
  onReload: () => void;
  onSave: () => void;
}

export function ProxySettingsView({
  available, settings, form, loadState, saving, onChange, onReload, onSave,
}: ProxySettingsViewProps) {
  const editable = available && loadState === 'ready' && !saving;
  return (
    <Stack gap="md">
        <Text fw={600}>프록시</Text>
        <Text size="sm" c="dimmed">현재: {networkUsageDescription(settings)} · 저장 전에 실제 연결을 확인합니다.</Text>
        {!available ? <Text c="yellow" size="sm">데스크톱 앱에서만 설정할 수 있습니다.</Text> : null}
        {loadState === 'loading' || loadState === 'idle' ? <Text c="dimmed" size="sm">저장된 연결 설정을 불러오는 중입니다.</Text> : null}
        {loadState === 'error' ? (
          <Group justify="space-between">
            <Text c="red" size="sm">저장된 연결 설정을 불러오지 못했습니다.</Text>
            <SecondaryButton size="xs" onClick={onReload}>다시 불러오기</SecondaryButton>
          </Group>
        ) : null}
        <SelectField
          label="연결 방식"
          data={[
            { value: 'direct', label: '사용 안 함' },
            { value: 'proxy', label: 'HTTP(S) / SOCKS5' },
          ]}
          value={form.mode}
          onChange={(value) => onChange({ ...form, mode: (value || 'direct') as NetworkForm['mode'] })}
          allowDeselect={false}
          disabled={!editable}
        />
        {form.mode === 'proxy' ? <>
          <TextAreaField
            label="프록시 주소"
            description="한 줄에 하나씩 입력하세요. http://, https://, socks5://를 지원합니다."
            placeholder={'socks5://127.0.0.1:1080\nhttps://proxy.example:8443'}
            value={form.proxyUrls}
            onChange={(event) => onChange({ ...form, proxyUrls: event.currentTarget.value })}
            disabled={!editable}
          />
          <TextField label="공통 사용자 이름" value={form.proxyUsername}
            onChange={(event) => onChange({ ...form, proxyUsername: event.currentTarget.value })} disabled={!editable} />
          <PasswordField label="공통 비밀번호"
            placeholder={settings.mode.case === 'proxy' && settings.mode.value.hasPassword ? '저장된 비밀번호 유지' : undefined}
            value={form.proxyPassword}
            onChange={(event) => onChange({ ...form, proxyPassword: event.currentTarget.value })} disabled={!editable} />
        </> : null}
        <Group justify="flex-end">
          <PrimaryButton onClick={onSave} loading={saving} disabled={!editable}>프록시 설정 저장</PrimaryButton>
        </Group>
    </Stack>
  );
}
