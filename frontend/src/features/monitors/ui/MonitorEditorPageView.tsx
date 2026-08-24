import { Stack } from '@mantine/core';
import { SecondaryButton } from '../../../components/core/Actions';
import { PageHeader } from '../../../components/core/PageHeader';
import { MonitorBuilderView, type MonitorBuilderViewProps } from './MonitorBuilderView';

export function MonitorEditorPageView({ builder, onBack }: { builder: MonitorBuilderViewProps; onBack: () => void }) {
  const editing = Boolean(builder.form.id);
  return <Stack gap="xl"><PageHeader title={editing ? '예매 찾기 수정' : '예매 찾기 시작'} description="영화, 좌석 프리셋, 인원과 일정을 설정합니다." actions={<SecondaryButton onClick={onBack}>예매 찾기 목록</SecondaryButton>} /><MonitorBuilderView {...builder} editing={editing} /></Stack>;
}
