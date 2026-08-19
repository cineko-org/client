import { Stack } from '@mantine/core';
import { SecondaryButton } from '../../../components/core/Actions';
import { PageHeader } from '../../../components/core/PageHeader';
import { Section } from '../../../components/core/Section';
import { MonitorBuilderView, type MonitorBuilderViewProps } from './MonitorBuilderView';

export function MonitorEditorPageView({ builder, onBack }: { builder: MonitorBuilderViewProps; onBack: () => void }) {
  const editing = Boolean(builder.form.id);
  return <Stack gap="xl"><PageHeader title={editing ? '모니터 편집' : '새 모니터'} description="영화, 일정, 좌석 조건을 설정합니다." actions={<SecondaryButton onClick={onBack}>모니터 목록</SecondaryButton>} /><Section title="모니터 조건"><MonitorBuilderView {...builder} editing={editing} /></Section></Stack>;
}
