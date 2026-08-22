import { Badge, Box, Group, Stack, Text } from '@mantine/core';
import { IconArrowLeft } from '@tabler/icons-react';
import { PrimaryButton, SecondaryButton } from '../../../components/core/Actions';
import { Columns } from '../../../components/core/Columns';
import { NumberField, SelectField, TextField } from '../../../components/core/Fields';
import { PageHeader } from '../../../components/core/PageHeader';
import { Section } from '../../../components/core/Section';
import type { Auditorium, CatalogIndex, Snapshot } from '../../../api/proto';
import type { PresetForm, SeatMapLoadState, SeatType } from '../model';
import { catalogRegions, catalogTheaters } from '../model';
import { SeatMapView } from './SeatMapView';

export interface PresetPageViewProps {
  catalog: CatalogIndex;
  form: PresetForm;
  region: string;
  theater: string;
  auditoriumId: string;
  auditoriums: Auditorium[];
  seatMap: Snapshot | null;
  pickedSeats: string[];
  catalogMessage: string;
  seatMapLoadState: SeatMapLoadState;
  loadingCatalog: boolean;
  saving: boolean;
  onBack: () => void;
  onRefreshCatalog: () => void;
  onFormChange: (form: PresetForm) => void;
  onRegionChange: (value: string) => void;
  onTheaterChange: (value: string) => void;
  onAuditoriumChange: (value: string) => void;
  onToggleSeat: (label: string) => void;
  onClearSeats: () => void;
  onSave: () => void;
  onReset: () => void;
}

export function PresetPageView(props: PresetPageViewProps) {
  const {
    catalog,
    form,
    region,
    theater,
    auditoriumId,
    auditoriums,
    seatMap,
    pickedSeats,
    catalogMessage,
    seatMapLoadState,
    loadingCatalog,
    saving,
    onBack,
    onRefreshCatalog,
    onFormChange,
    onRegionChange,
    onTheaterChange,
    onAuditoriumChange,
    onToggleSeat,
    onClearSeats,
    onSave,
    onReset,
  } = props;
  const theaters = catalog.theaters;
  const regions = catalogRegions(theaters);
  const theaterOptions = catalogTheaters(theaters, region);
  const selectedAuditorium = auditoriums.find((item) => item.id === auditoriumId);

  return (
    <Stack gap="xl">
      <PageHeader
        title={form.id ? '프리셋 편집' : '새 프리셋'}
        description="상영관과 원하는 좌석 조건을 정합니다."
        actions={
          <SecondaryButton leftSection={<IconArrowLeft size={16} />} onClick={onBack}>
            프리셋 목록
          </SecondaryButton>
        }
      />
      <Group justify="space-between" bg="dark.8" p="md" wrap="wrap">
        <Stack gap={2}>
          <Text fw={600}>CGV 영화관 목록</Text>
          <Text size="xs" c="dimmed">
            {catalog.generation > 0 ? 'Central에서 불러온 목록' : '목록을 먼저 불러오세요.'}
          </Text>
        </Stack>
        <SecondaryButton onClick={onRefreshCatalog}>목록 새로고침</SecondaryButton>
      </Group>

      <Section title={form.id ? '좌석 프리셋 편집' : '새 좌석 프리셋'} description="지역, 지점, 상영관을 순서대로 선택하세요.">
        <Stack gap="xl">
          <Columns>
            <SelectField label="지역" placeholder="지역 선택" data={regions} value={region} onChange={(value) => onRegionChange(value || '')} />
            <SelectField
              label="CGV 지점"
              placeholder="지점 선택"
              data={theaterOptions}
              value={theater}
              onChange={(value) => void onTheaterChange(value || '')}
              disabled={!region}
            />
            <SelectField
              label="상영관"
              placeholder="상영관 선택"
              data={auditoriums.map((item) => ({
                value: item.id,
                label: item.name,
              }))}
              value={auditoriumId}
              onChange={(value) => void onAuditoriumChange(value || '')}
              disabled={!theater}
            />
          </Columns>
          {catalogMessage ? (
            <Group gap="xs">
              {seatMapLoadState !== 'idle' ? (
                <Badge
                  color={seatMapLoadState === 'cached' ? 'green' : seatMapLoadState === 'pending' || seatMapLoadState === 'loading' ? 'yellow' : 'red'}
                  variant="light"
                >
                  {seatMapLoadState === 'cached' ? '저장된 배치' : seatMapLoadState === 'pending' || seatMapLoadState === 'loading' ? '준비 중' : '오류'}
                </Badge>
              ) : null}
              <Text size="sm" c={seatMapLoadState === 'error' ? 'red.4' : 'dimmed'}>
                {catalogMessage}
              </Text>
            </Group>
          ) : null}

          <Columns gap="xl">
            <SeatMapView
              seatMap={seatMap}
              pickedSeats={pickedSeats}
              auditoriumName={selectedAuditorium?.name}
              reportedCapacity={selectedAuditorium?.capacity}
              emptyMessage={
                !auditoriumId
                  ? '상영관을 선택하면 좌석 배치를 불러옵니다.'
                  : loadingCatalog
                    ? '좌석 배치를 불러오는 중입니다.'
                    : '좌석 배치 분석을 기다리고 있습니다.'
              }
              onToggleSeat={onToggleSeat}
              onClear={onClearSeats}
            />
            <Box
              component="form"
              onSubmit={(event) => {
                event.preventDefault();
                onSave();
              }}
            >
              <Stack gap="md">
                <Group justify="space-between">
                  <Text fw={700}>선호 규칙</Text>
                  {seatMap ? (
                    <Text size="xs" c="dimmed">
                      {seatMap.layout?.seats.length ?? 0}석 · {seatMap.layout?.zones.length ?? 0}존
                    </Text>
                  ) : null}
                </Group>
                <TextField
                  label="이름"
                  placeholder="용산 IMAX 중앙 2석"
                  required
                  value={form.name}
                  onChange={(event) => onFormChange({ ...form, name: event.currentTarget.value })}
                />
                <Columns>
                  <NumberField
                    label="인원"
                    min={1}
                    max={8}
                    value={form.seatCount}
                    onChange={(value) =>
                      onFormChange({
                        ...form,
                        seatCount: typeof value === 'number' ? value : 1,
                      })
                    }
                  />
                  <SelectField
                    label="좌석 타입"
                    data={[
                      { value: 'standard', label: '일반석' },
                      { value: 'wheelchair', label: '장애인/이동식' },
                      { value: 'companion', label: '보호자석' },
                      { value: 'recliner', label: '리클라이너' },
                      { value: 'motion', label: '4DX/모션' },
                      { value: 'prime', label: '프라임석' },
                      { value: 'premium', label: '프리미엄석' },
                      { value: 'couple', label: '커플석' },
                      { value: 'bed', label: '침대형 좌석' },
                    ]}
                    value={form.seatType}
                    onChange={(value) =>
                      onFormChange({
                        ...form,
                        seatType: (value || 'standard') as SeatType,
                      })
                    }
                    allowDeselect={false}
                  />
                </Columns>
                <TextField
                  label="선호 행"
                  placeholder="H, I, G"
                  value={form.preferredRows}
                  onChange={(event) =>
                    onFormChange({
                      ...form,
                      preferredRows: event.currentTarget.value,
                    })
                  }
                />
                <Text size="sm" c="dimmed">
                  {pickedSeats.length === 0
                    ? `예매 시 CGV의 최신 좌석에서 ${form.seatCount === 1 ? '가장 적합한 1석' : `붙어 있는 ${form.seatCount}석`}을 자동으로 찾습니다.`
                    : form.seatCount === 1
                      ? '선택한 후보 좌석을 순서대로 확인합니다.'
                      : `선택한 후보 안에서 붙어 있는 ${form.seatCount}석만 찾습니다.`}
                </Text>
                <Group justify="flex-end">
                  <SecondaryButton type="button" onClick={onReset}>
                    새로 작성
                  </SecondaryButton>
                  <PrimaryButton type="submit" loading={saving} disabled={!seatMap || loadingCatalog}>
                    좌석 프리셋 저장
                  </PrimaryButton>
                </Group>
              </Stack>
            </Box>
          </Columns>
        </Stack>
      </Section>
    </Stack>
  );
}
