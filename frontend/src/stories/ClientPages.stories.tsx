import type { Meta, StoryObj } from '@storybook/react-vite';
import { Box } from '@mantine/core';
import { useState } from 'react';
import { HomePage } from '../app/HomePage';
import { MonitorsPage } from '../app/pages/MonitorsPage';
import { SettingsPage } from '../app/pages/SettingsPage';
import { PresetsPage } from '../app/pages/PresetsPage';
import { MonitorDetailPageView } from '../features/monitors/ui/MonitorDetailPageView';
import { MonitorEditorPageView } from '../features/monitors/ui/MonitorEditorPageView';
import { PresetPageView } from '../features/presets/ui/PresetPageView';
import { initialMonitorForm } from '../features/monitors/model';
import { initialPresetForm } from '../features/presets/model';
import type { MonitorForm } from '../features/monitors/model';
import type { PresetForm } from '../features/presets/model';
import {
	auditoriums, authenticatedAccount, catalog, directNetwork, monitors, noop, presets, proxyNetwork,
	reservations, seatMap, unauthenticatedAccount, checkingAccount,
} from './fixtures';
import { imaxSeatMapFixture } from './liveSeatMaps';

const meta = { title: 'Client/Pages' } satisfies Meta;
export default meta;
type Story = StoryObj;

function Canvas({ children }: { children: React.ReactNode }) {
  return <Box bg="dark.9" mih="100dvh" p={{ base: 'md', md: 32, xl: 48 }}>{children}</Box>;
}

const monitorList = {
  monitors, deleteId: null, mutationId: null, onRetry: noop, onStop: noop, onDeleteRequest: noop,
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
  render: () => <Canvas><MonitorDetailPageView monitor={monitors[0]} mutating={false} onBack={noop} onEdit={noop} onRetry={noop} onStop={noop} /></Canvas>,
};

export const AwaitingPaymentMonitor: Story = {
  render: () => <Canvas><MonitorDetailPageView monitor={monitors[1]} mutating={false} onBack={noop} onEdit={noop} onRetry={noop} onStop={noop} /></Canvas>,
};

export const MobileAwaitingPaymentMonitor: Story = {
  globals: { viewport: { value: 'phone', isRotated: false } },
  render: () => <Canvas><MonitorDetailPageView monitor={monitors[1]} mutating={false} onBack={noop} onEdit={noop} onRetry={noop} onStop={noop} /></Canvas>,
};

export const UnknownPaymentResult: Story = {
  render: () => <Canvas><MonitorDetailPageView monitor={monitors[2]} mutating={false} onBack={noop} onEdit={noop} onRetry={noop} onStop={noop} /></Canvas>,
};

export const NewMonitor: Story = {
  render: () => <NewMonitorStory />,
};

export const MobileNewMonitor: Story = {
  globals: { viewport: { value: 'phone', isRotated: false } },
  render: () => <NewMonitorStory />,
};

function NewMonitorStory() {
  const [form, setForm] = useState<MonitorForm>({ ...initialMonitorForm, presetId: presets[0].id, weekdays: ['4'] });
  return (
    <Canvas><MonitorEditorPageView onBack={noop} builder={{ movies: catalog.movies, presets, form, submitting: false, onChange: setForm, onSubmit: noop }} /></Canvas>
  );
}

export const EditMonitor: Story = {
  render: () => (
    <Canvas><MonitorEditorPageView onBack={noop} builder={{
      movies: catalog.movies, presets,
      form: { ...initialMonitorForm, id: monitors[0].id, movieId: monitors[0].movieId, movie: monitors[0].movieTitle, presetId: presets[0].id, weekdays: monitors[0].targetWeekdays.map(String), earliestTime: '18:00', latestTime: '23:30' },
      submitting: false, onChange: noop, onSubmit: noop,
    }} /></Canvas>
  ),
};

export const Presets: Story = {
  render: () => <Canvas><PresetsPage presets={presets} deleteId={null} onNew={noop} onEdit={noop} onDeleteRequest={noop} onDelete={noop} /></Canvas>,
};

export const NewPreset: Story = {
  render: () => <NewPresetStory />,
};

export const MobileNewPreset: Story = {
  globals: { viewport: { value: 'phone', isRotated: false } },
  render: () => <NewPresetStory />,
};

function NewPresetStory() {
  const [form, setForm] = useState<PresetForm>({ ...initialPresetForm, name: '용산 IMAX 중앙 좌석' });
  const [region, setRegion] = useState('');
  const [theater, setTheater] = useState('');
  const [auditoriumId, setAuditoriumId] = useState('');
  const [pickedSeats, setPickedSeats] = useState<string[]>([]);
  const availableAuditoriums = theater === '용산아이파크몰' ? auditoriums : [];
  return (
    <Canvas><PresetPageView
      catalog={catalog} form={form} region={region} theater={theater} auditoriumId={auditoriumId}
      auditoriums={availableAuditoriums} seatMap={auditoriumId ? seatMap : null} pickedSeats={pickedSeats}
      catalogMessage={theater && availableAuditoriums.length === 0 ? '이 Story에서는 용산아이파크몰 좌석만 준비되어 있습니다.' : ''}
      seatMapLoadState={auditoriumId ? 'cached' : 'idle'}
      loadingCatalog={false} saving={false} onBack={noop} onRefreshCatalog={noop}
      onFormChange={setForm} onRegionChange={(value) => { setRegion(value); setTheater(''); setAuditoriumId(''); }}
      onTheaterChange={(value) => { setTheater(value); setAuditoriumId(''); }} onAuditoriumChange={setAuditoriumId}
      onToggleSeat={(label) => setPickedSeats((current) => current.includes(label) ? current.filter((item) => item !== label) : [...current, label])}
      onClearSeats={() => setPickedSeats([])} onSave={noop} onReset={() => { setForm(initialPresetForm); setPickedSeats([]); }}
    /></Canvas>
  );
}

export const EditPreset: Story = {
  render: () => (
    <Canvas><PresetPageView
      catalog={catalog} form={{ ...initialPresetForm, id: presets[0].id, name: presets[0].name }}
      region="서울" theater="용산아이파크몰" auditoriumId={imaxSeatMapFixture.seatMap.auditoriumId} auditoriums={auditoriums} seatMap={seatMap}
      pickedSeats={imaxSeatMapFixture.pickedSeats} catalogMessage="" seatMapLoadState="cached" loadingCatalog={false} saving={false}
      onBack={noop} onRefreshCatalog={noop} onFormChange={noop} onRegionChange={noop} onTheaterChange={noop}
      onAuditoriumChange={noop} onToggleSeat={noop} onClearSeats={noop} onSave={noop} onReset={noop}
    /></Canvas>
  ),
};

export const Settings: Story = {
  render: () => (
    <Canvas>
      <SettingsPage
        available account={authenticatedAccount}
        settings={directNetwork}
        form={{ mode: 'direct', proxyUrls: '', proxyUsername: '', proxyPassword: '' }}
        loadState="ready" saving={false} onChange={noop} onReload={noop} onSave={noop} onAuthenticate={noop}
        hookAvailable hookLoadState="ready" hookSaving={false} onHookAdd={noop} onHookReload={noop} onHookChange={noop} onHookRemove={noop} onHookSave={noop}
        hookForms={[{ id: 'discord', name: '예매 알림', kind: 'discord', url: 'https://discord.com/api/webhooks/…', secret: '', eventKinds: ['monitor.completed', 'reservation.cancelled'], enabled: true }]}
      />
    </Canvas>
  ),
};

export const StandardProxySettings: Story = {
  render: () => (
    <Canvas><SettingsPage
      available account={unauthenticatedAccount}
      settings={proxyNetwork}
      form={{ mode: 'proxy', proxyUrls: 'socks5://127.0.0.1:1080', proxyUsername: 'cineko', proxyPassword: '' }}
      loadState="ready" saving={false} onChange={noop} onReload={noop} onSave={noop} onAuthenticate={noop}
      hookAvailable hookForms={[]} hookLoadState="ready" hookSaving={false} onHookAdd={noop} onHookReload={noop} onHookChange={noop} onHookRemove={noop} onHookSave={noop}
    /></Canvas>
  ),
};

export const SettingsLoading: Story = {
  render: () => (
    <Canvas><SettingsPage
      available account={checkingAccount} settings={directNetwork}
      form={{ mode: 'direct', proxyUrls: '', proxyUsername: '', proxyPassword: '' }}
      loadState="loading" saving={false} onChange={noop} onReload={noop} onSave={noop} onAuthenticate={noop}
      hookAvailable hookForms={[]} hookLoadState="loading" hookSaving={false} onHookAdd={noop} onHookReload={noop}
      onHookChange={noop} onHookRemove={noop} onHookSave={noop}
    /></Canvas>
  ),
};

export const SettingsLoadFailed: Story = {
  render: () => (
    <Canvas><SettingsPage
      available account={checkingAccount} settings={directNetwork}
      form={{ mode: 'direct', proxyUrls: '', proxyUsername: '', proxyPassword: '' }}
      loadState="error" saving={false} onChange={noop} onReload={noop} onSave={noop} onAuthenticate={noop}
      hookAvailable hookForms={[]} hookLoadState="error" hookSaving={false} onHookAdd={noop} onHookReload={noop}
      onHookChange={noop} onHookRemove={noop} onHookSave={noop}
    /></Canvas>
  ),
};
