import type { Meta, StoryObj } from '@storybook/react-vite';
import { Box } from '@mantine/core';
import { HomePage } from '../app/HomePage';
import { MonitorDetailPageView } from '../features/monitors/ui/MonitorDetailPageView';
import { MonitorRetryDialog } from '../features/monitors/ui/MonitorRetryDialog';
import { MonitorListView } from '../features/monitors/ui/MonitorListView';
import { PresetListView } from '../features/presets/ui/PresetListView';
import { ReservationListView } from '../features/reservations/ui/ReservationListView';
import { PresetPageView } from '../features/presets/ui/PresetPageView';
import { initialPresetForm } from '../features/presets/model';
import { catalog, monitors, noop, presets, reservations } from './fixtures';

const meta = { title: 'Client/States' } satisfies Meta;
export default meta;
type Story = StoryObj;
const Canvas = ({ children }: { children: React.ReactNode }) => <Box bg="dark.9" mih="100dvh" p={48}>{children}</Box>;

export const EmptyHome: Story = { render: () => <Canvas><HomePage monitors={0} runningMonitors={0} presets={0} reservations={0} onMonitors={noop} onNewMonitor={noop} /></Canvas> };
export const EmptyMonitors: Story = { render: () => <Canvas><MonitorListView monitors={[]} deleteId={null} mutationId={null} onRetry={noop} onDeleteRequest={noop} onDelete={noop} onOpen={noop} onEdit={noop} /></Canvas> };
export const MonitorDeleteConfirmation: Story = { render: () => <Canvas><MonitorListView monitors={monitors} deleteId={monitors[0].id} mutationId={null} onRetry={noop} onDeleteRequest={noop} onDelete={noop} onOpen={noop} onEdit={noop} /></Canvas> };
export const MissingMonitor: Story = { render: () => <Canvas><MonitorDetailPageView mutating={false} onBack={noop} onEdit={noop} onRetry={noop} /></Canvas> };
export const FailedMonitor: Story = { render: () => <Canvas><MonitorDetailPageView monitor={monitors[3]} mutating={false} onBack={noop} onEdit={noop} onRetry={noop} /></Canvas> };
export const TriggeredRetryConfirmation: Story = { render: () => <Canvas><MonitorRetryDialog monitor={monitors[1]} acknowledged={false} submitting={false} onAcknowledgedChange={noop} onClose={noop} onConfirm={noop} /></Canvas> };
export const UnknownPaymentRetryConfirmation: Story = { render: () => <Canvas><MonitorRetryDialog monitor={monitors[2]} acknowledged submitting={false} onAcknowledgedChange={noop} onClose={noop} onConfirm={noop} /></Canvas> };
export const EmptyPresets: Story = { render: () => <Canvas><PresetListView presets={[]} deleteId={null} onNew={noop} onEdit={noop} onDeleteRequest={noop} onDelete={noop} /></Canvas> };
export const PresetDeleteConfirmation: Story = { render: () => <Canvas><PresetListView presets={presets} deleteId={presets[0].id} onNew={noop} onEdit={noop} onDeleteRequest={noop} onDelete={noop} /></Canvas> };
export const EmptyReservations: Story = { render: () => <Canvas><ReservationListView reservations={[]} cancelId={null} cancelling={false} onReviewCancellation={noop} onCancelRequest={noop} onConfirmCancellation={noop} /></Canvas> };
export const CancellationConfirmation: Story = { render: () => <Canvas><ReservationListView reservations={reservations} cancelId={reservations[0].id} cancelling={false} onReviewCancellation={noop} onCancelRequest={noop} onConfirmCancellation={noop} /></Canvas> };
export const PresetBeforeSeatMap: Story = { render: () => <Canvas><PresetPageView catalog={catalog} form={initialPresetForm} region="" theater="" auditoriumId="" auditoriums={[]} seatMap={null} pickedSeats={[]} catalogMessage="" loadingCatalog={false} saving={false} onBack={noop} onRefreshCatalog={noop} onFormChange={noop} onRegionChange={noop} onTheaterChange={noop} onAuditoriumChange={noop} onToggleSeat={noop} onClearSeats={noop} onSave={noop} onReset={noop} /></Canvas> };
