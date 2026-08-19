import type { ReactNode } from 'react';
import { Alert, AppShell, Box, Center, Group, Indicator, Loader, Notification, Stack, Text, UnstyledButton } from '@mantine/core';
import { IconBell, IconDoorExit, IconFileExport, IconFileImport, IconSettings } from '@tabler/icons-react';
import { IconAction, SecondaryButton } from '../../../components/core/Actions';
import { StatusIndicator } from '../../../components/core/StatusIndicator';
import type { AccountState, ApplicationConnection, NetworkSettings } from '../../../api/types';

interface ShellFeedback {
  message: string;
  tone: 'info' | 'success' | 'warning' | 'error';
}

export interface AppShellViewProps {
  activeSection: MainSection | null;
  loading: boolean;
  connection: ApplicationConnection;
  account: AccountState;
  network: NetworkSettings;
  desktopAvailable: boolean;
  unreadNotices: number;
  feedback: ShellFeedback | null;
  children: ReactNode;
  onNavigate: (section: MainSection) => void;
  onImport: () => void;
  onExport: () => void;
  onExit: () => void;
  onOpenNotifications: () => void;
  onOpenSettings: () => void;
  onDismissFeedback: () => void;
  onRetryConnection: () => void;
}

export type MainSection = 'home' | 'monitors' | 'presets';

const feedbackColor = { info: 'blue', success: 'green', warning: 'yellow', error: 'red' } as const;

export function AppShellView(props: AppShellViewProps) {
  const {
    activeSection, loading, connection, account, network, desktopAvailable, unreadNotices, feedback,
    children, onNavigate, onImport, onExport, onExit,
    onOpenNotifications, onOpenSettings, onDismissFeedback, onRetryConnection,
  } = props;
  return (
    <AppShell header={{ height: 64 }} withBorder={false} bg="dark.9">
      <AppShell.Header bg="dark.9" px={{ base: 'md', md: 32, xl: 48 }} withBorder>
        <Group h="100%" justify="space-between" wrap="nowrap">
          <Group gap="xl" wrap="nowrap" aria-label="주요 화면">
            {([['home', '홈'], ['monitors', '예매 모니터'], ['presets', '프리셋']] as const).map(([value, label]) => {
              const active = activeSection === value;
              return (
              <UnstyledButton key={value} onClick={() => onNavigate(value)} aria-current={active ? 'page' : undefined}>
                <Text size="sm" fw={active ? 700 : 500} c={active ? 'gray.0' : 'gray.6'}>{label}</Text>
              </UnstyledButton>
              );
            })}
          </Group>
          <Group gap="xl" wrap="nowrap">
            <Group gap="md" wrap="nowrap" visibleFrom="md">
              <StatusIndicator label="프록시" color={network.mode !== 'direct' ? 'green' : 'gray'} muted={network.mode === 'direct'} />
              <StatusIndicator label="CGV" color={account.authenticated ? 'green' : 'gray'} muted={!account.authenticated} />
            </Group>
            <Group gap={4} wrap="nowrap">
              {desktopAvailable ? <IconAction label="가져오기" icon={<IconFileImport size={19} />} onClick={onImport} /> : null}
              {desktopAvailable ? <IconAction label="내보내기" icon={<IconFileExport size={19} />} onClick={onExport} /> : null}
              <Indicator
                disabled={unreadNotices === 0}
                label={Math.min(unreadNotices, 9)}
                size={16}
                radius={999}
                color="red"
                offset={4}
              >
                <IconAction label="알림" icon={<IconBell size={19} />} onClick={onOpenNotifications} />
              </Indicator>
              <IconAction label="설정" icon={<IconSettings size={19} />} onClick={onOpenSettings} />
              {desktopAvailable ? <IconAction label="나가기" icon={<IconDoorExit size={19} />} onClick={onExit} /> : null}
            </Group>
          </Group>
        </Group>
      </AppShell.Header>
      <AppShell.Main
        bg="dark.9"
        pt="calc(64px + var(--mantine-spacing-xl))"
        px={{ base: 'md', md: 32, xl: 48 }}
        pb={{ base: 32, xl: 48 }}
      >
        <Box maw={1440} mx="auto" aria-busy={loading || connection.retrying}>
          {loading ? (
            <Center mih="calc(100dvh - 64px - var(--mantine-spacing-xl) - 48px)">
              <Stack gap="sm" align="center">
                <Loader size="sm" color="gray" />
                <Text size="sm" c="dimmed">Cineko를 준비하고 있습니다.</Text>
              </Stack>
            </Center>
          ) : (
            <Stack gap="xl">
              {connection.status === 'unavailable' || connection.status === 'stale' ? (
                <Alert
                  color={connection.status === 'unavailable' ? 'red' : 'yellow'}
                  title={connection.status === 'unavailable' ? 'Central에 연결할 수 없습니다.' : 'Central 연결이 끊어져 이전 정보를 표시합니다.'}
                  role="alert"
                >
                  <Group justify="space-between" align="flex-end" wrap="wrap">
                    <Stack gap={2}>
                      <Text size="sm">{connection.status === 'unavailable'
                        ? '연결을 복구한 뒤 다시 시도하세요. 연결 전에는 최신 정보를 불러올 수 없습니다.'
                        : '표시된 정보가 최신이 아닐 수 있습니다. 연결을 복구한 뒤 다시 확인하세요.'}</Text>
                      {connection.lastSuccessfulAt ? (
                        <Text size="xs" c="dimmed">마지막 동기화 {new Date(connection.lastSuccessfulAt).toLocaleString('ko-KR')}</Text>
                      ) : null}
                    </Stack>
                    <SecondaryButton loading={connection.retrying} onClick={onRetryConnection}>다시 연결</SecondaryButton>
                  </Group>
                </Alert>
              ) : null}
              {children}
            </Stack>
          )}
        </Box>
      </AppShell.Main>
      {feedback ? (
        <Notification
          pos="fixed"
          right={24}
          bottom={24}
          w={380}
          color={feedbackColor[feedback.tone]}
          onClose={onDismissFeedback}
          withCloseButton
          style={{ zIndex: 500 }}
        >
          {feedback.message}
        </Notification>
      ) : null}
    </AppShell>
  );
}
