import { useCallback, useState } from 'react';
import { DatesProvider } from '@mantine/dates';
import { MantineProvider } from '@mantine/core';
import { useApplicationState } from '../features/application/useApplicationState';
import { useMonitors } from '../features/monitors/useMonitors';
import { MonitorRetryDialog } from '../features/monitors/ui/MonitorRetryDialog';
import { unreadNoticeCount } from '../features/notifications/model';
import { NotificationDrawerView } from '../features/notifications/ui/NotificationDrawerView';
import { useNotifications } from '../features/notifications/useNotifications';
import { usePresets } from '../features/presets/usePresets';
import { useReservations } from '../features/reservations/useReservations';
import { useNetworkSettings } from '../features/settings/useNetworkSettings';
import { useHookSettings } from '../features/settings/useHookSettings';
import { AppShellView } from '../features/shell/ui/AppShellView';
import { AppRoutes } from './AppRoutes';
import { cinekoTheme } from './theme';
import { useRouter } from './useRouter';

function CinekoApplication() {
  const {
    route, activeSection, navigate, navigateSection, openSettings,
  } = useRouter();
  const [notificationsOpened, setNotificationsOpened] = useState(false);
  const notifications = useNotifications();
	const application = useApplicationState(notifications.notify, notifications.load);
  const goToMonitors = useCallback(() => navigate({ name: 'monitors' }), [navigate]);
  const goToPresets = useCallback(() => navigate({ name: 'presets' }), [navigate]);
  const network = useNetworkSettings(route.name === 'settings', notifications.notify);
  const hooks = useHookSettings(route.name === 'settings', notifications.notify);
  const presets = usePresets(
    application.state, application.userId, application.reload,
    notifications.notify, goToPresets,
  );
  const monitors = useMonitors(
    application.state, application.userId, application.reload, notifications.notify, goToMonitors,
  );
  const reservations = useReservations(
    application.state, application.userId, application.reload, notifications.notify,
  );
  const showNotifications = () => {
    notifications.markRead();
    setNotificationsOpened(true);
  };

  return (
    <>
      <AppShellView
        activeSection={activeSection}
        loading={application.loading}
        connection={application.connection}
        account={application.account}
        network={network.settings}
        desktopAvailable={application.desktopAvailable}
        unreadNotices={unreadNoticeCount(notifications.notices)}
        feedback={notifications.feedback}
        onNavigate={navigateSection}
        onExit={application.exit}
        onOpenNotifications={showNotifications}
        onOpenSettings={openSettings}
        onDismissFeedback={notifications.dismissFeedback}
        onRetryConnection={application.retryConnection}
      >
        <AppRoutes
          route={route}
          application={application}
          monitors={monitors}
          presets={presets}
          reservations={reservations}
          network={network}
          hooks={hooks}
          onNavigate={navigate}
          onMonitors={goToMonitors}
          onPresets={goToPresets}
        />
      </AppShellView>
      <NotificationDrawerView
        opened={notificationsOpened}
        notices={notifications.notices}
        onClose={() => setNotificationsOpened(false)}
        onClear={notifications.clear}
      />
      <MonitorRetryDialog
        monitor={monitors.retryMonitor}
        acknowledged={monitors.retryAcknowledged}
        submitting={Boolean(monitors.mutationId)}
        onAcknowledgedChange={monitors.setRetryAcknowledged}
        onClose={monitors.cancelRetry}
        onConfirm={() => void monitors.confirmRetry()}
      />
    </>
  );
}

export function App() {
  return (
    <MantineProvider forceColorScheme="dark" theme={cinekoTheme}>
      <DatesProvider settings={{ locale: 'ko', firstDayOfWeek: 0, weekendDays: [0, 6] }}>
        <CinekoApplication />
      </DatesProvider>
    </MantineProvider>
  );
}
