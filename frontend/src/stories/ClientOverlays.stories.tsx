import type { Meta, StoryObj } from '@storybook/react-vite';
import { Box, Button, Group, Stack, Text } from '@mantine/core';
import { NotificationDrawerView } from '../features/notifications/ui/NotificationDrawerView';
import { noop, notices } from './fixtures';

const meta = { title: 'Client/Overlays' } satisfies Meta;
export default meta;
type Story = StoryObj;

function Backdrop() {
  return <Box bg="dark.9" mih="100dvh" p={48}><Stack><Text fw={700} fz="xl">예매 찾기</Text><Group><Button>예매 찾기 시작</Button></Group></Stack></Box>;
}

export const Notifications: Story = {
  render: () => <><Backdrop /><NotificationDrawerView opened notices={notices} onClose={noop} onClear={noop} /></>,
};

export const EmptyNotifications: Story = {
  render: () => <><Backdrop /><NotificationDrawerView opened notices={[]} onClose={noop} onClear={noop} /></>,
};
