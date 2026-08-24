import { Stack } from '@mantine/core';
import { PrimaryButton } from '../../components/core/Actions';
import { PageHeader } from '../../components/core/PageHeader';
import { PresetListView, type PresetListViewProps } from '../../features/presets/ui/PresetListView';

export function PresetsPage(props: PresetListViewProps) {
  return (
    <Stack gap="xl">
      <PageHeader title="좌석 프리셋" description="상영관별 후보 좌석을 저장하고 관리합니다." actions={<PrimaryButton onClick={props.onNew}>좌석 프리셋 추가</PrimaryButton>} />
      <PresetListView {...props} />
    </Stack>
  );
}
