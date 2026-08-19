import type { CatalogMovie, Preset } from '../../api/types';
import { MonitorEditorPageView } from '../../features/monitors/ui/MonitorEditorPageView';
import type { useMonitors } from '../../features/monitors/useMonitors';

interface MonitorEditorPageProps {
  movies: CatalogMovie[];
  presets: Preset[];
  controller: ReturnType<typeof useMonitors>;
  onBack: () => void;
}

export function MonitorEditorPage({ movies, presets, controller, onBack }: MonitorEditorPageProps) {
  return (
    <MonitorEditorPageView
      builder={{
        movies,
        presets,
        form: controller.form,
        submitting: controller.submitting,
        onChange: controller.setForm,
        onSubmit: controller.requestCreate,
      }}
      onBack={onBack}
    />
  );
}
