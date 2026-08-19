import { Box, Group, Stack, Text } from '@mantine/core';
import { PrimaryButton } from '../../../components/core/Actions';
import { Columns } from '../../../components/core/Columns';
import { NumberField, SelectField } from '../../../components/core/Fields';
import type { CatalogMovie, Preset } from '../../../api/types';
import type { MonitorForm } from '../model';
import { ScheduleView } from './ScheduleView';

export interface MonitorBuilderViewProps {
  movies: CatalogMovie[];
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
          <Columns>
            <SelectField label="영화" placeholder="영화 선택" data={movies.map((movie) => movie.title)} value={form.movie} onChange={(movie) => onChange({ ...form, movie: movie || '' })} />
            <SelectField label="좌석 프리셋" placeholder="프리셋 선택" data={presets.map((preset) => ({ value: preset.id, label: preset.name }))} value={form.presetId} onChange={(presetId) => onChange({ ...form, presetId: presetId || '' })} />
          </Columns>
          <Stack gap="xs">
            <Text fw={600}>확인 간격</Text>
            <Columns>
              <NumberField label="최소" value={form.pollMinMinutes} onChange={(value) => onChange({ ...form, pollMinMinutes: typeof value === 'number' ? value : 3 })} min={1} suffix="분" />
              <NumberField label="최대" value={form.pollMaxMinutes} onChange={(value) => onChange({ ...form, pollMaxMinutes: typeof value === 'number' ? value : 8 })} min={2} suffix="분" />
            </Columns>
            <Text size="xs" c="dimmed">매회 이 범위에서 새 간격을 선택합니다.</Text>
          </Stack>
          <ScheduleView form={form} onChange={onChange} />
          <Group justify="flex-end" align="center">
            <PrimaryButton type="submit" color={editing ? 'cineko' : 'red'} loading={submitting}>{editing ? '변경 저장' : '모니터 시작'}</PrimaryButton>
          </Group>
        </Stack>
      </Box>
  );
}
