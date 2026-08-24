import { Group, Stack, Text } from '@mantine/core';
import { SecondaryButton } from '../../../components/core/Actions';
import { StatusIndicator } from '../../../components/core/StatusIndicator';
import type { WebUIAccountState } from '../../../api/proto';
import { accountAuthenticated } from '../../../api/resources';

interface AccountSettingsViewProps {
	account: WebUIAccountState;
  onAuthenticate: () => void;
}

export function AccountSettingsView({ account, onAuthenticate }: AccountSettingsViewProps) {
	const authenticated = accountAuthenticated(account);

  return (
    <Stack gap="md">
      <Group justify="space-between">
        <Stack gap={2}>
			<StatusIndicator label="CGV" color={authenticated ? 'green' : 'gray'} muted={!authenticated} />
          <Text size="xs" c="dimmed">
			좌석 선택과 예매를 시작하려면 브라우저에서 CGV 로그인을 직접 완료하세요.
          </Text>
        </Stack>
		<SecondaryButton onClick={onAuthenticate}>{authenticated ? '브라우저에서 확인' : '직접 로그인'}</SecondaryButton>
      </Group>
    </Stack>
  );
}
