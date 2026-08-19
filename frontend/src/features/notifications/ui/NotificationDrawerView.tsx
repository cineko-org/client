import { Divider, Drawer, Group, Stack, Text, ThemeIcon } from '@mantine/core';
import { IconAlertTriangle, IconCheck, IconInfoCircle } from '@tabler/icons-react';
import { SecondaryButton } from '../../../components/core/Actions';
import type { Notice, NoticeTone } from '../model';

export interface NotificationDrawerViewProps {
  opened: boolean;
  notices: Notice[];
  onClose: () => void;
  onClear: () => void;
}

const toneColor: Record<NoticeTone, string> = { info: 'blue', success: 'green', warning: 'yellow', error: 'red' };

function NoticeIcon({ tone }: { tone: NoticeTone }) {
  const icon = tone === 'success'
    ? <IconCheck size={16} />
    : tone === 'warning' || tone === 'error'
      ? <IconAlertTriangle size={16} />
      : <IconInfoCircle size={16} />;
  return <ThemeIcon radius={0} color={toneColor[tone]} variant="light" size="md">{icon}</ThemeIcon>;
}

export function NotificationDrawerView({ opened, notices, onClose, onClear }: NotificationDrawerViewProps) {
  return (
    <Drawer
      opened={opened}
      onClose={onClose}
      position="right"
      title="알림"
      size={420}
      closeButtonProps={{ 'aria-label': '알림 닫기' }}
    >
      <Stack gap="lg">
        <Group justify="flex-end">
          <SecondaryButton size="xs" onClick={onClear} disabled={notices.length === 0}>모두 지우기</SecondaryButton>
        </Group>
        {notices.length === 0 ? <Text size="sm" c="dimmed">새 알림이 없습니다.</Text> : (
          <Stack gap="md">
            {notices.map((notice, index) => (
              <Stack key={notice.id} gap="md">
                {index > 0 ? <Divider /> : null}
                <Group align="flex-start" gap="sm" wrap="nowrap">
                  <NoticeIcon tone={notice.tone} />
                  <Stack gap={2} style={{ flex: 1 }}>
                    <Text size="sm">{notice.message}</Text>
                    <Text size="xs" c="dimmed">{new Date(notice.createdAt).toLocaleString('ko-KR')}</Text>
                  </Stack>
                </Group>
              </Stack>
            ))}
          </Stack>
        )}
      </Stack>
    </Drawer>
  );
}
