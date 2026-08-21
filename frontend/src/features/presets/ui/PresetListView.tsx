import { Group, Modal, Stack, Text } from '@mantine/core';
import { DangerButton, PrimaryButton, SecondaryButton } from '../../../components/core/Actions';
import { EmptyState, Section } from '../../../components/core/Section';
import type { Preset } from '../../../api/proto';
import { presetSummary } from '../model';

export interface PresetListViewProps {
  presets: Preset[];
  deleteId: string | null;
  onNew: () => void;
  onEdit: (id: string) => void;
  onDeleteRequest: (id: string | null) => void;
  onDelete: () => void;
}

export function PresetListView({ presets, deleteId, onEdit, onDeleteRequest, onDelete }: PresetListViewProps) {
  return (
    <Section title="저장된 프리셋" actions={<Text size="xs" c="dimmed">{presets.length}개</Text>} subtle>
      {presets.length === 0 ? <EmptyState>저장된 프리셋이 없습니다.</EmptyState> : (
        <Stack gap="xs">
          {presets.map((preset) => (
            <Stack key={preset.id} gap="xs" bg="dark.6" p="md">
              <Group justify="space-between"><Text fw={600}>{preset.name}</Text><Text size="xs" c="dimmed">{preset.seatCount}석</Text></Group>
              <Text size="sm" c="dimmed">{presetSummary(preset)}</Text>
              <Group gap="xs"><SecondaryButton size="xs" onClick={() => onEdit(preset.id)}>편집</SecondaryButton><DangerButton size="xs" onClick={() => onDeleteRequest(preset.id)}>삭제</DangerButton></Group>
            </Stack>
          ))}
        </Stack>
      )}
      <Modal opened={Boolean(deleteId)} onClose={() => onDeleteRequest(null)} title="프리셋 삭제">
        <Stack gap="lg"><Text size="sm">이 좌석 프리셋을 삭제할까요?</Text><Group justify="flex-end"><SecondaryButton onClick={() => onDeleteRequest(null)}>취소</SecondaryButton><PrimaryButton color="red" onClick={onDelete}>삭제</PrimaryButton></Group></Stack>
      </Modal>
    </Section>
  );
}
