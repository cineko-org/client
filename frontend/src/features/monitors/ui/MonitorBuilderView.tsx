import { Box, Group, Stack } from '@mantine/core';
import { PrimaryButton } from '../../../components/core/Actions';
import { SelectField } from '../../../components/core/Fields';
import type { Movie, Preset } from '../../../api/proto';
import type { MonitorForm } from '../model';
import { ScheduleView } from './ScheduleView';
import { MoviePicker } from './MoviePicker';

export interface MonitorBuilderViewProps {
  movies: Movie[];
  presets: Preset[];
  form: MonitorForm;
  submitting: boolean;
  onChange: (form: MonitorForm) => void;
  onSubmit: () => void;
  editing?: boolean;
}

export function MonitorBuilderView(props: MonitorBuilderViewProps) {
  const { movies, presets, form, submitting, onChange, onSubmit, editing = false } = props;
  return (
      <Box component="form" onSubmit={(event) => { event.preventDefault(); onSubmit(); }}>
        <Stack gap="xl">
          <MoviePicker movies={movies} value={form.movieId} onChange={(movie) => onChange({ ...form, movieId: movie.id, movie: movie.title })} />
          <SelectField label="좌석 프리셋" placeholder="좌석 프리셋을 선택하세요" required data={presets.map((preset) => ({ value: preset.id, label: preset.name }))} value={form.presetId} onChange={(presetId) => onChange({ ...form, presetId: presetId || '' })} />
          <ScheduleView form={form} onChange={onChange} />
          <Group justify="flex-end" align="center">
            <PrimaryButton w={{ base: '100%', sm: 'auto' }} size="md" type="submit" color={editing ? 'cineko' : 'red'} loading={submitting}>{editing ? '변경 저장' : '모니터 시작'}</PrimaryButton>
          </Group>
        </Stack>
      </Box>
  );
}
