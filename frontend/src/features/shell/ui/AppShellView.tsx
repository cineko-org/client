import { useState, type ReactNode } from 'react';
import { ActionIcon, Alert, AppShell, Box, Center, Divider, Group, Indicator, Loader, NavLink, Notification, SimpleGrid, Stack, Text, Tooltip, UnstyledButton } from '@mantine/core';
import {
  IconBell,
  IconBookmark,
  IconDoorExit,
  IconHome,
  IconLayoutSidebarLeftCollapse,
  IconLayoutSidebarRightCollapse,
  IconRadar,
  IconSettings,
} from '@tabler/icons-react';
import { IconAction, SecondaryButton } from '../../../components/core/Actions';
import { StatusIndicator } from '../../../components/core/StatusIndicator';
import type { WebUIAccountState } from '../../../api/proto';
import { accountAuthenticated } from '../../../api/resources';
import type { ApplicationConnection } from '../../../shared/application';
import type { NetworkSettings } from '../../../api/proto';

interface ShellFeedback {
  message: string;
  tone: 'info' | 'success' | 'warning' | 'error';
}

export interface AppShellViewProps {
  activeSection: MainSection | null;
  loading: boolean;
  connection: ApplicationConnection;
	account: WebUIAccountState;
  network: NetworkSettings;
  desktopAvailable: boolean;
  unreadNotices: number;
  feedback: ShellFeedback | null;
  children: ReactNode;
  onNavigate: (section: MainSection) => void;
  onExit: () => void;
  onOpenNotifications: () => void;
  onOpenSettings: () => void;
  onDismissFeedback: () => void;
  onRetryConnection: () => void;
}

export type MainSection = 'home' | 'monitors' | 'presets';

const feedbackColor = { info: 'blue', success: 'green', warning: 'yellow', error: 'red' } as const;

const navigation = [
  { section: 'home', label: '홈', icon: IconHome },
  { section: 'monitors', label: '예매 모니터', icon: IconRadar },
  { section: 'presets', label: '프리셋', icon: IconBookmark },
] as const;

interface ShellNavigationProps {
  activeSection: MainSection | null;
  collapsed: boolean;
  onNavigate: (section: MainSection) => void;
}

/** Renders the same navigation contract for expanded and icon-only sidebars. */
function ShellNavigation({ activeSection, collapsed, onNavigate }: ShellNavigationProps) {
  return (
    <Stack gap={4} align={collapsed ? 'center' : 'stretch'}>
      {navigation.map(({ section, label, icon: Icon }) => {
        const active = activeSection === section;
        if (collapsed) {
          return (
            <Tooltip
              key={section}
              label={label}
              position="right"
              events={{ hover: true, focus: true, touch: false }}
            >
              <ActionIcon
                aria-label={label}
                aria-current={active ? 'page' : undefined}
                color="gray"
                radius={0}
                size={48}
                variant={active ? 'filled' : 'subtle'}
                onClick={() => onNavigate(section)}
              >
                <Icon size={20} stroke={1.8} />
              </ActionIcon>
            </Tooltip>
          );
        }
        return (
          <NavLink
            key={section}
            component="button"
            type="button"
            aria-label={label}
            active={active}
            label={label}
            leftSection={<Icon size={20} stroke={1.8} />}
            onClick={() => onNavigate(section)}
            color="gray"
            variant="filled"
            mih={48}
          />
        );
      })}
    </Stack>
  );
}

/** Keeps the three primary destinations reachable with one thumb on phones. */
function MobileNavigation({ activeSection, onNavigate }: Omit<ShellNavigationProps, 'collapsed'>) {
  return (
    <SimpleGrid cols={3} h="100%" spacing={0} aria-label="주요 화면">
      {navigation.map(({ section, label, icon: Icon }) => {
        const active = activeSection === section;
        return (
          <UnstyledButton
            key={section}
            aria-label={label}
            aria-current={active ? 'page' : undefined}
            onClick={() => onNavigate(section)}
            style={{ minWidth: 0 }}
          >
            <Stack h="100%" gap={2} align="center" justify="center">
              <Icon size={21} stroke={active ? 2.1 : 1.7} />
              <Text size="xs" fw={active ? 700 : 500} c={active ? 'gray.0' : 'gray.6'} truncate>{label}</Text>
            </Stack>
          </UnstyledButton>
        );
      })}
    </SimpleGrid>
  );
}

export function AppShellView(props: AppShellViewProps) {
  const {
    activeSection, loading, connection, account, network, desktopAvailable, unreadNotices, feedback,
    children, onNavigate, onExit,
    onOpenNotifications, onOpenSettings, onDismissFeedback, onRetryConnection,
  } = props;
  const [navigationCollapsed, setNavigationCollapsed] = useState(false);
  return (
    <AppShell
      padding={{ base: 16, xs: 24, md: 40, xl: 56 }}
      header={{ height: { base: 56, xs: 64 } }}
      navbar={{
        width: navigationCollapsed ? 76 : { sm: 220, md: 244 },
        breakpoint: 'sm',
        collapsed: { mobile: true },
      }}
      footer={{ height: { base: 64, sm: 0 } }}
      withBorder={false}
      bg="dark.9"
    >
      <AppShell.Header bg="dark.9" px={{ base: 12, sm: 'md', md: 32, xl: 48 }} withBorder>
        <Group h="100%" justify="space-between" wrap="nowrap">
          <Group gap="sm" wrap="nowrap">
            <Text fw={800} lts="0.08em" tt="uppercase" size="sm">Cineko</Text>
            <Text size="xs" c="dimmed" tt="uppercase" fw={700} lts="0.08em" visibleFrom="sm">Client</Text>
          </Group>
          <Group gap="md" wrap="nowrap">
            <Group gap="md" wrap="nowrap" visibleFrom="md">
			  <StatusIndicator label="프록시" color={network.mode.case === 'proxy' ? 'green' : 'gray'} muted={network.mode.case !== 'proxy'} />
			  <StatusIndicator label="CGV" color={accountAuthenticated(account) ? 'green' : 'gray'} muted={!accountAuthenticated(account)} />
            </Group>
            <Group gap={4} wrap="nowrap">
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
      <AppShell.Navbar bg="dark.9" withBorder p={navigationCollapsed ? 12 : 'md'}>
        <ShellNavigation activeSection={activeSection} collapsed={navigationCollapsed} onNavigate={onNavigate} />
        <Box mt="auto">
          <Divider mb="sm" />
          <Group justify={navigationCollapsed ? 'center' : 'flex-end'}>
            <IconAction
              label={navigationCollapsed ? '사이드바 펼치기' : '사이드바 접기'}
              icon={navigationCollapsed ? <IconLayoutSidebarRightCollapse size={20} /> : <IconLayoutSidebarLeftCollapse size={20} />}
              onClick={() => setNavigationCollapsed((value) => !value)}
            />
          </Group>
        </Box>
      </AppShell.Navbar>
      <AppShell.Main bg="dark.9">
        <Box maw={1680} mx="auto" aria-busy={loading || connection.retrying}>
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
      <AppShell.Footer bg="dark.9" withBorder hiddenFrom="sm">
        <MobileNavigation activeSection={activeSection} onNavigate={onNavigate} />
      </AppShell.Footer>
      {feedback ? (
        <Notification
          pos="fixed"
          right={16}
          bottom="calc(var(--app-shell-footer-offset, 0px) + 16px)"
          color={feedbackColor[feedback.tone]}
          onClose={onDismissFeedback}
          withCloseButton
          style={{ zIndex: 500, width: 'min(380px, calc(100vw - 32px))' }}
        >
          {feedback.message}
        </Notification>
      ) : null}
    </AppShell>
  );
}
