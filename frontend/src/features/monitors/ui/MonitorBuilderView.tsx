import { Box, Group, Stack, Switch } from '@mantine/core';
import { PrimaryButton } from '../../../components/core/Actions';
import { Columns } from '../../../components/core/Columns';
import { NumberField, SelectField } from '../../../components/core/Fields';
import type { Movie, Preset } from '../../../api/proto';
import { seatTypeOptions, type MonitorForm } from '../model';
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
          <Columns>
            <NumberField
              label="예매 인원"
              required
              min={1}
              max={8}
              value={form.seatCount}
              onChange={(value) => onChange({ ...form, seatCount: typeof value === 'number' ? value : 1 })}
            />
            <SelectField
              label="좌석 타입"
              required
              data={seatTypeOptions}
              value={form.seatType}
              allowDeselect={false}
              onChange={(seatType) => onChange({ ...form, seatType: seatType || 'standard' })}
            />
          </Columns>
          <ScheduleView form={form} onChange={onChange} />
          <Switch
            checked={form.watchCancellationSeats}
            onChange={(event) => onChange({ ...form, watchCancellationSeats: event.currentTarget.checked })}
            label="취소표 감시"
            description="켜면 매칭되는 상영 회차를 브라우저 탭에서 계속 확인합니다. 끄면 신규 예매 오픈만 감지합니다."
          />
          <Group justify="flex-end" align="center">
            <PrimaryButton w={{ base: '100%', sm: 'auto' }} size="md" type="submit" color={editing ? 'cineko' : 'red'} loading={submitting}>{editing ? '변경 저장' : '모니터 시작'}</PrimaryButton>
          </Group>
        </Stack>
      </Box>
  );
}
