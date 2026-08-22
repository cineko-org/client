import type { CatalogIndex } from '../../api/proto';
import { PresetPageView } from '../../features/presets/ui/PresetPageView';
import type { usePresets } from '../../features/presets/usePresets';

interface PresetEditorPageProps {
  catalog: CatalogIndex;
  controller: ReturnType<typeof usePresets>;
  onBack: () => void;
  onRefreshCatalog: () => void;
}

export function PresetEditorPage({ catalog, controller, onBack, onRefreshCatalog }: PresetEditorPageProps) {
  return (
    <PresetPageView
      catalog={catalog}
      form={controller.form}
      region={controller.region}
      theater={controller.theater}
      auditoriumId={controller.auditoriumId}
      auditoriums={controller.auditoriums}
      seatMap={controller.seatMap}
      pickedSeats={controller.pickedSeats}
      catalogMessage={controller.catalogMessage}
      seatMapLoadState={controller.seatMapLoadState}
      loadingCatalog={controller.loadingCatalog}
      saving={controller.saving}
      onBack={onBack}
      onRefreshCatalog={onRefreshCatalog}
      onFormChange={controller.setForm}
      onRegionChange={controller.setRegion}
      onTheaterChange={controller.setTheater}
      onAuditoriumChange={controller.setAuditorium}
      onToggleSeat={controller.toggleSeat}
      onClearSeats={controller.clearSeats}
      onSave={controller.save}
      onReset={controller.reset}
    />
  );
}
