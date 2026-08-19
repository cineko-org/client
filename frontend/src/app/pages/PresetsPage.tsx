import { Stack } from '@mantine/core';
import { PrimaryButton } from '../../components/core/Actions';
import { PageHeader } from '../../components/core/PageHeader';
import { PresetListView, type PresetListViewProps } from '../../features/presets/ui/PresetListView';

export function PresetsPage(props: PresetListViewProps) {
  return (
    <Stack gap="xl">
      <PageHeader title="프리셋" description="저장된 좌석 조건을 관리합니다." actions={<PrimaryButton onClick={props.onNew}>새 프리셋</PrimaryButton>} />
      <PresetListView {...props} />
    </Stack>
  );
}
