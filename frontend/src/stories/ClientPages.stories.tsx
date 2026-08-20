import type { Meta, StoryObj } from '@storybook/react-vite';
import { Box } from '@mantine/core';
import { HomePage } from '../app/HomePage';
import { MonitorsPage } from '../app/pages/MonitorsPage';
import { SettingsPage } from '../app/pages/SettingsPage';
import { PresetsPage } from '../app/pages/PresetsPage';
import { MonitorDetailPageView } from '../features/monitors/ui/MonitorDetailPageView';
import { MonitorEditorPageView } from '../features/monitors/ui/MonitorEditorPageView';
import { PresetPageView } from '../features/presets/ui/PresetPageView';
import { initialMonitorForm } from '../features/monitors/model';
import { initialPresetForm } from '../features/presets/model';
import { auditoriums, catalog, monitors, noop, presets, reservations, seatMap } from './fixtures';
import { imaxSeatMapFixture } from './liveSeatMaps';

const meta = { title: 'Client/Pages' } satisfies Meta;
export default meta;
type Story = StoryObj;

function Canvas({ children }: { children: React.ReactNode }) {
  return <Box bg="dark.9" mih="100dvh" p={{ base: 'md', md: 32, xl: 48 }}>{children}</Box>;
}

const monitorList = {
  monitors, deleteId: null, mutationId: null, onRetry: noop, onDeleteRequest: noop,
  onDelete: noop, onOpen: noop, onEdit: noop,
};
const reservationList = {
  reservations, cancelId: null, cancelling: false, onReviewCancellation: noop,
  onCancelRequest: noop, onConfirmCancellation: noop,
};

export const Home: Story = {
  render: () => <Canvas><HomePage monitors={3} runningMonitors={1} presets={2} reservations={1} onMonitors={noop} onNewMonitor={noop} /></Canvas>,
};

export const Monitors: Story = {
  render: () => <Canvas><MonitorsPage monitors={monitorList} reservations={reservationList} onNew={noop} /></Canvas>,
};

export const MonitorDetail: Story = {
  render: () => <Canvas><MonitorDetailPageView monitor={monitors[0]} mutating={false} onBack={noop} onEdit={noop} onRetry={noop} /></Canvas>,
};

export const AwaitingPaymentMonitor: Story = {
  render: () => <Canvas><MonitorDetailPageView monitor={monitors[1]} mutating={false} onBack={noop} onEdit={noop} onRetry={noop} /></Canvas>,
};

export const UnknownPaymentResult: Story = {
  render: () => <Canvas><MonitorDetailPageView monitor={monitors[2]} mutating={false} onBack={noop} onEdit={noop} onRetry={noop} /></Canvas>,
};

export const NewMonitor: Story = {
  render: () => (
    <Canvas>
      <MonitorEditorPageView
        onBack={noop}
        builder={{
          movies: catalog.movies, presets, form: { ...initialMonitorForm, movieId: catalog.movies[0].id, movie: catalog.movies[0].title, presetId: presets[0].id, dates: ['2026-08-20'] },
          submitting: false, onChange: noop, onSubmit: noop,
        }}
      />
    </Canvas>
  ),
};

export const EditMonitor: Story = {
  render: () => (
    <Canvas><MonitorEditorPageView onBack={noop} builder={{
      movies: catalog.movies, presets,
      form: { ...initialMonitorForm, id: monitors[0].id, movieId: monitors[0].movieId, movie: monitors[0].movie, presetId: presets[0].id, dates: monitors[0].targetDates, earliestTime: '18:00', latestTime: '23:30' },
      submitting: false, onChange: noop, onSubmit: noop,
    }} /></Canvas>
  ),
};

export const Presets: Story = {
  render: () => <Canvas><PresetsPage presets={presets} deleteId={null} onNew={noop} onEdit={noop} onDeleteRequest={noop} onDelete={noop} /></Canvas>,
};

export const NewPreset: Story = {
  render: () => (
    <Canvas>
      <PresetPageView
        catalog={catalog} form={{ ...initialPresetForm, name: '용산 IMAX 중앙 2석', seatCount: 2, preferredRows: 'H, I' }}
        region="서울" theater="용산아이파크몰" auditoriumId={imaxSeatMapFixture.seatMap.auditoriumId} auditoriums={auditoriums}
        seatMap={seatMap} pickedSeats={imaxSeatMapFixture.pickedSeats} catalogDates="" catalogMessage="좌석 배치를 불러왔습니다."
        loadingCatalog={false} saving={false} forceCapture={false}
        onBack={noop} onRefreshCatalog={noop} onFormChange={noop} onRegionChange={noop} onTheaterChange={noop}
        onAuditoriumChange={noop} onCatalogDatesChange={noop} onDiscover={noop} onCapture={noop}
        onForceCaptureChange={noop} onToggleSeat={noop} onClearSeats={noop} onSave={noop} onReset={noop}
      />
    </Canvas>
  ),
};

export const EditPreset: Story = {
  render: () => (
    <Canvas><PresetPageView
      catalog={catalog} form={{ ...initialPresetForm, id: presets[0].id, name: presets[0].name, seatCount: 2, preferredRows: 'H, I' }}
      region="서울" theater="용산아이파크몰" auditoriumId={imaxSeatMapFixture.seatMap.auditoriumId} auditoriums={auditoriums} seatMap={seatMap}
      pickedSeats={imaxSeatMapFixture.pickedSeats} catalogDates="2026-08-20" catalogMessage="" loadingCatalog={false} saving={false} forceCapture={false}
      onBack={noop} onRefreshCatalog={noop} onFormChange={noop} onRegionChange={noop} onTheaterChange={noop}
      onAuditoriumChange={noop} onCatalogDatesChange={noop} onDiscover={noop} onCapture={noop}
      onForceCaptureChange={noop} onToggleSeat={noop} onClearSeats={noop} onSave={noop} onReset={noop}
    /></Canvas>
  ),
};

export const Settings: Story = {
  render: () => (
    <Canvas>
      <SettingsPage
        available account={{ status: 'authenticated', authenticated: true }}
        settings={{ mode: 'soxy', soxyUrl: 'https://proxy.example.com', hasApiToken: true, soxySessionTtl: '30m' }}
        form={{ mode: 'soxy', proxyUrls: '', proxyUsername: '', proxyPassword: '', soxyUrl: 'https://proxy.example.com', soxyToken: '', sessionTtl: '30m' }}
        loadState="ready" saving={false} onChange={noop} onReload={noop} onSave={noop} onAuthenticate={noop}
        onSaveAccountCredentials={noop} onRestoreAuthentication={noop} onDeleteAccountCredentials={noop}
        hookAvailable hookLoadState="ready" hookSaving={false} onHookAdd={noop} onHookReload={noop} onHookChange={noop} onHookRemove={noop} onHookSave={noop}
        hookForms={[{ id: 'discord', name: '예매 알림', kind: 'discord', url: 'https://discord.com/api/webhooks/…', secret: '', eventKinds: ['monitor.completed', 'reservation.cancelled'], enabled: true }]}
      />
    </Canvas>
  ),
};

export const StandardProxySettings: Story = {
  render: () => (
    <Canvas><SettingsPage
      available account={{ status: 'unauthenticated', authenticated: false }}
      settings={{ mode: 'proxy', proxyUrls: ['socks5://127.0.0.1:1080'], hasProxyPassword: true }}
      form={{ mode: 'proxy', proxyUrls: 'socks5://127.0.0.1:1080', proxyUsername: 'cineko', proxyPassword: '', soxyUrl: '', soxyToken: '', sessionTtl: '30m' }}
      loadState="ready" saving={false} onChange={noop} onReload={noop} onSave={noop} onAuthenticate={noop}
      onSaveAccountCredentials={noop} onRestoreAuthentication={noop} onDeleteAccountCredentials={noop}
      hookAvailable hookForms={[]} hookLoadState="ready" hookSaving={false} onHookAdd={noop} onHookReload={noop} onHookChange={noop} onHookRemove={noop} onHookSave={noop}
    /></Canvas>
  ),
};

export const SettingsLoading: Story = {
  render: () => (
    <Canvas><SettingsPage
      available account={{ status: 'checking', authenticated: false }} settings={{ mode: 'direct' }}
      form={{ mode: 'direct', proxyUrls: '', proxyUsername: '', proxyPassword: '', soxyUrl: '', soxyToken: '', sessionTtl: '30m' }}
      loadState="loading" saving={false} onChange={noop} onReload={noop} onSave={noop} onAuthenticate={noop}
      onSaveAccountCredentials={noop} onRestoreAuthentication={noop} onDeleteAccountCredentials={noop}
      hookAvailable hookForms={[]} hookLoadState="loading" hookSaving={false} onHookAdd={noop} onHookReload={noop}
      onHookChange={noop} onHookRemove={noop} onHookSave={noop}
    /></Canvas>
  ),
};

export const SettingsLoadFailed: Story = {
  render: () => (
    <Canvas><SettingsPage
      available account={{ status: 'error', authenticated: false }} settings={{ mode: 'direct' }}
      form={{ mode: 'direct', proxyUrls: '', proxyUsername: '', proxyPassword: '', soxyUrl: '', soxyToken: '', sessionTtl: '30m' }}
      loadState="error" saving={false} onChange={noop} onReload={noop} onSave={noop} onAuthenticate={noop}
      onSaveAccountCredentials={noop} onRestoreAuthentication={noop} onDeleteAccountCredentials={noop}
      hookAvailable hookForms={[]} hookLoadState="error" hookSaving={false} onHookAdd={noop} onHookReload={noop}
      onHookChange={noop} onHookRemove={noop} onHookSave={noop}
    /></Canvas>
  ),
};
