import { create } from '@bufbuild/protobuf';
import { CatalogIndexSchema } from '../api/proto';
import { monitorStatus, reservationStatus } from '../api/resources';
import { HomePage } from './HomePage';
import { MonitorDetailPageView } from '../features/monitors/ui/MonitorDetailPageView';
import { useApplicationState } from '../features/application/useApplicationState';
import { useMonitors } from '../features/monitors/useMonitors';
import { usePresets } from '../features/presets/usePresets';
import { useReservations } from '../features/reservations/useReservations';
import { useNetworkSettings } from '../features/settings/useNetworkSettings';
import { useHookSettings } from '../features/settings/useHookSettings';
import { MonitorEditorPage } from './pages/MonitorEditorPage';
import { MonitorsPage } from './pages/MonitorsPage';
import { OperationsPage } from './pages/OperationsPage';
import { PresetEditorPage } from './pages/PresetEditorPage';
import { PresetsPage } from './pages/PresetsPage';
import { SettingsPage } from './pages/SettingsPage';
import type { Route } from './router';
import { useEditorRouteInitialization } from './useEditorRouteInitialization';

interface AppRoutesProps {
  route: Route;
  application: ReturnType<typeof useApplicationState>;
  monitors: ReturnType<typeof useMonitors>;
  presets: ReturnType<typeof usePresets>;
  reservations: ReturnType<typeof useReservations>;
  network: ReturnType<typeof useNetworkSettings>;
  hooks: ReturnType<typeof useHookSettings>;
  onNavigate: (route: Route) => void;
  onMonitors: () => void;
  onPresets: () => void;
}

export function AppRoutes({
  route, application, monitors, presets, reservations, network, hooks,
  onNavigate, onMonitors, onPresets,
}: AppRoutesProps) {
  const catalog = application.state.catalog ?? create(CatalogIndexSchema);
  const newMonitor = () => {
		onNavigate({ name: 'monitor-new' });
  };
  const editMonitor = (id: string) => {
		if (monitors.monitors.some((monitor) => monitor.id === id)) onNavigate({ name: 'monitor-edit', monitorId: id });
  };
  const newPreset = () => {
		onNavigate({ name: 'preset-new' });
  };
  const editPreset = (id: string) => {
		if (presets.presets.some((preset) => preset.id === id)) onNavigate({ name: 'preset-edit', presetId: id });
  };

  switch (route.name) {
	case 'operations':
		return <OperationsPage />;
    case 'monitors':
      return (
        <MonitorsPage
          monitors={{
            monitors: monitors.monitors,
            deleteId: monitors.deleteId,
            mutationId: monitors.mutationId,
            onRetry: monitors.retry,
            onStop: monitors.stop,
            onToggleCancellationWatch: monitors.toggleCancellationWatch,
            onDeleteRequest: monitors.setDeleteId,
            onDelete: monitors.remove,
            onOpen: (monitorId) => onNavigate({ name: 'monitor-detail', monitorId }),
            onEdit: editMonitor,
          }}
          reservations={{
            reservations: reservations.reservations,
            cancelId: reservations.cancelId,
            cancelling: reservations.cancelling,
            onReviewCancellation: reservations.reviewCancellation,
            onCancelRequest: reservations.setCancelId,
            onConfirmCancellation: reservations.confirmCancellation,
          }}
          onNew={newMonitor}
        />
      );
	case 'monitor-new':
		return (
			<MonitorEditorRoute
				monitorId={null}
				movies={catalog.movies}
          presets={presets.presets}
          controller={monitors}
          onBack={onMonitors}
			/>
		);
	case 'monitor-edit':
		return (
			<MonitorEditorRoute
				monitorId={route.monitorId}
				movies={catalog.movies}
				presets={presets.presets}
				controller={monitors}
				onBack={onMonitors}
			/>
		);
    case 'monitor-detail': {
      const monitorId = route.monitorId;
      const monitor = monitors.monitors.find((item) => item.id === monitorId);
      return (
        <MonitorDetailPageView
          monitor={monitor}
          mutating={monitors.mutationId === monitorId}
          onBack={onMonitors}
          onEdit={() => editMonitor(monitorId)}
          onRetry={() => monitors.retry(monitorId)}
          onStop={() => void monitors.stop(monitorId)}
          onToggleCancellationWatch={() => void monitors.toggleCancellationWatch(monitorId)}
        />
      );
    }
    case 'presets':
      return (
        <PresetsPage
          presets={presets.presets}
          deleteId={presets.deleteId}
          onNew={newPreset}
          onEdit={editPreset}
          onDeleteRequest={presets.setDeleteId}
          onDelete={presets.remove}
        />
      );
	case 'preset-new':
		return (
			<PresetEditorRoute
				presetId={null}
				catalog={catalog}
          controller={presets}
          onBack={onPresets}
          onRefreshCatalog={() => void application.reload()}
			/>
		);
	case 'preset-edit':
		return (
			<PresetEditorRoute
				presetId={route.presetId}
				catalog={catalog}
				controller={presets}
				onBack={onPresets}
				onRefreshCatalog={() => void application.reload()}
			/>
		);
    case 'settings':
      return (
        <SettingsPage
          available={network.bridgeAvailable}
          settings={network.settings}
          account={application.account}
          form={network.form}
          loadState={network.loadState}
          saving={network.saving}
          onChange={network.setForm}
          onReload={() => void network.load()}
          onSave={() => void network.save()}
          onAuthenticate={() => void application.openAuthentication()}
          hookAvailable={hooks.available}
          hookForms={hooks.forms}
          hookLoadState={hooks.loadState}
          hookSaving={hooks.saving}
          onHookAdd={hooks.add}
          onHookReload={() => void hooks.load()}
          onHookChange={hooks.change}
          onHookRemove={hooks.remove}
          onHookSave={() => void hooks.save()}
        />
      );
    case 'home':
      return (
        <HomePage
          monitors={monitors.monitors.length}
          runningMonitors={monitors.monitors.filter((item) => monitorStatus(item) === 'running').length}
          presets={presets.presets.length}
          reservations={reservations.reservations.filter((item) => reservationStatus(item) === 'booked').length}
          onMonitors={onMonitors}
          onNewMonitor={newMonitor}
        />
      );
  }
}

interface MonitorEditorRouteProps {
	monitorId: string | null;
	movies: Parameters<typeof MonitorEditorPage>[0]['movies'];
	presets: Parameters<typeof MonitorEditorPage>[0]['presets'];
	controller: ReturnType<typeof useMonitors>;
	onBack: () => void;
}

function MonitorEditorRoute({ monitorId, controller, ...props }: MonitorEditorRouteProps) {
	const { edit, newMonitor } = controller;
	useEditorRouteInitialization(monitorId, edit, newMonitor);
	if (monitorId && controller.form.id !== monitorId) return null;
	return <MonitorEditorPage {...props} controller={controller} />;
}

interface PresetEditorRouteProps {
	presetId: string | null;
	catalog: Parameters<typeof PresetEditorPage>[0]['catalog'];
	controller: ReturnType<typeof usePresets>;
	onBack: () => void;
	onRefreshCatalog: () => void;
}

function PresetEditorRoute({ presetId, controller, ...props }: PresetEditorRouteProps) {
	const { edit, newPreset } = controller;
	useEditorRouteInitialization(presetId, edit, newPreset);
	if (presetId && controller.form.id !== presetId) return null;
	return <PresetEditorPage {...props} controller={controller} />;
}
