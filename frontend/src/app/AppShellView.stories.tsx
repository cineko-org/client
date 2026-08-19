import type { Meta, StoryObj } from '@storybook/react-vite';
import { AppShellView } from '../features/shell/ui/AppShellView';
import { HomePage } from './HomePage';

const noop = () => undefined;

const meta = {
  title: 'Client/Application',
  component: AppShellView,
  args: {
    activeSection: 'home',
    loading: false,
    connection: { status: 'ready', message: '', lastSuccessfulAt: '2026-08-12T08:00:00Z', retrying: false },
    account: { status: 'authenticated', authenticated: true },
    network: { mode: 'soxy', soxyUrl: 'https://proxy.example.com' },
    desktopAvailable: true,
    unreadNotices: 2,
    feedback: null,
    onNavigate: noop,
    onImport: noop,
    onExport: noop,
    onExit: noop,
    onOpenNotifications: noop,
    onOpenSettings: noop,
    onDismissFeedback: noop,
    onRetryConnection: noop,
    children: <HomePage monitors={4} runningMonitors={2} presets={5} reservations={1} onMonitors={noop} onNewMonitor={noop} />,
  },
} satisfies Meta<typeof AppShellView>;

export default meta;
type Story = StoryObj<typeof meta>;
export const Home: Story = {};
export const DirectAndSignedOut: Story = { args: { account: { status: 'unauthenticated', authenticated: false }, network: { mode: 'direct' }, unreadNotices: 0 } };
export const Loading: Story = { args: { loading: true } };
export const Feedback: Story = { args: { feedback: { tone: 'success', message: '프록시 설정을 저장했습니다.' } } };
export const CentralUnavailable: Story = {
  args: { connection: { status: 'unavailable', message: 'connection refused', lastSuccessfulAt: '', retrying: false } },
};
export const CentralStale: Story = {
  args: { connection: { status: 'stale', message: 'timeout', lastSuccessfulAt: '2026-08-12T08:00:00Z', retrying: false } },
};
export const CentralRetrying: Story = {
  args: { connection: { status: 'unavailable', message: 'connection refused', lastSuccessfulAt: '', retrying: true } },
};
