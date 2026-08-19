import { Group, PasswordInput, Stack, Text, TextInput } from '@mantine/core';
import { useEffect, useState } from 'react';
import { DangerButton, PrimaryButton, SecondaryButton } from '../../../components/core/Actions';
import { StatusIndicator } from '../../../components/core/StatusIndicator';
import type { AccountState } from '../../../api/types';

interface AccountSettingsViewProps {
  account: AccountState;
  onAuthenticate: () => void;
  onSave: (id: string, password: string) => void;
  onRestore: () => void;
  onDelete: () => void;
}

export function AccountSettingsView({
  account, onAuthenticate, onSave, onRestore, onDelete,
}: AccountSettingsViewProps) {
  const [id, setId] = useState(account.accountId ?? '');
  const [password, setPassword] = useState('');

  useEffect(() => setId(account.accountId ?? ''), [account.accountId]);

  return (
    <Stack gap="md">
      <Group justify="space-between">
        <Stack gap={2}>
          <StatusIndicator label="CGV" color={account.authenticated ? 'green' : 'gray'} muted={!account.authenticated} />
          <Text size="xs" c="dimmed">
            {account.credentialsSaved ? '로그인이 풀리면 이 기기에서 다시 로그인합니다.' : '로그인하지 않아도 좌석 선택까지 진행할 수 있습니다.'}
          </Text>
        </Stack>
        <SecondaryButton onClick={onAuthenticate}>{account.authenticated ? '브라우저에서 확인' : '직접 로그인'}</SecondaryButton>
      </Group>

      {account.credentialsSaved ? (
        <Group justify="space-between" align="flex-end" wrap="wrap">
          <TextInput label="저장된 계정" value={account.accountId ?? ''} readOnly style={{ flex: 1 }} />
          <Group gap="xs">
            <SecondaryButton onClick={onRestore}>지금 다시 로그인</SecondaryButton>
            <DangerButton onClick={onDelete}>저장 정보 삭제</DangerButton>
          </Group>
        </Group>
      ) : (
        <Group align="flex-end" wrap="wrap">
          <TextInput label="CJ ONE 아이디" value={id} onChange={(event) => setId(event.currentTarget.value)} style={{ flex: 1 }} />
          <PasswordInput label="비밀번호" value={password} onChange={(event) => setPassword(event.currentTarget.value)} style={{ flex: 1 }} />
          <PrimaryButton disabled={!id.trim() || !password} onClick={() => {
            onSave(id, password);
            setPassword('');
          }}>저장하고 로그인</PrimaryButton>
        </Group>
      )}
    </Stack>
  );
}
