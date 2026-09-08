import { Group, Stack, Text } from '@mantine/core';
import { PrimaryButton, SecondaryButton } from '../../../components/core/Actions';
import { PasswordField, TextField } from '../../../components/core/Fields';
import type { ScannerSettingsController } from '../scannerModel';

export function ScannerSettingsView({ controller }: { controller: ScannerSettingsController }) {
  const { available, form, state, setForm, load, save } = controller;
  const busy = state.phase === 'loading' || state.phase === 'saving';
  return <Stack gap="md">
    <Text fw={600}>SOXY</Text>
    <Text size="sm" c="dimmed">일정·영화·포스터 수집에 사용합니다. 로그인·좌석 선택·결제에는 사용하지 않습니다.</Text>
    {!available ? <Text size="sm" c="dimmed">데스크톱 앱에서 설정할 수 있습니다.</Text> : null}
    <TextField label="SOXY 주소" placeholder="http://soxy.example:8080" value={form.url}
      onChange={event => setForm({ ...form, url: event.currentTarget.value })} disabled={!available || busy} />
    <PasswordField label="SOXY API 토큰" value={form.token} autoComplete="off"
      placeholder={state.hasToken ? '저장된 토큰 유지' : 'API 토큰 입력'}
      description={state.hasToken ? '비워 두면 기존 토큰을 유지합니다. 주소를 바꾸면 토큰도 입력하세요.' : undefined}
      onChange={event => setForm({ ...form, token: event.currentTarget.value })} disabled={!available || busy} />
    {state.message ? <Text role="status" size="sm" c={state.phase === 'error' ? 'red' : 'dimmed'}>{state.message}</Text> : null}
    <Group justify="flex-end">
      {state.phase === 'error' ? <SecondaryButton onClick={() => void load()}>다시 불러오기</SecondaryButton> : null}
      <PrimaryButton onClick={() => void save()} loading={state.phase === 'saving'} disabled={!available || busy || !form.url.trim() || (!state.hasToken && !form.token.trim())}>연결 확인 후 저장</PrimaryButton>
    </Group>
  </Stack>;
}
