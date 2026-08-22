import { Group, Modal, Stack, Text } from '@mantine/core';
import { CheckField } from '../../../components/core/Fields';
import { PrimaryButton, SecondaryButton } from '../../../components/core/Actions';
import type { Monitor } from '../../../api/proto';
import { monitorStatus } from '../../../api/resources';

export interface MonitorRetryDialogProps {
  monitor?: Monitor;
  acknowledged: boolean;
  submitting: boolean;
  onAcknowledgedChange: (acknowledged: boolean) => void;
  onClose: () => void;
  onConfirm: () => void;
}

export function MonitorRetryDialog({
  monitor, acknowledged, submitting, onAcknowledgedChange, onClose, onConfirm,
}: MonitorRetryDialogProps) {
	const paymentUnknown = monitor ? monitorStatus(monitor) === 'payment_unknown' : false;
  return (
    <Modal
      opened={Boolean(monitor)}
      onClose={onClose}
      closeOnClickOutside={!submitting}
      closeOnEscape={!submitting}
      withCloseButton={!submitting}
      title={paymentUnknown ? '예매 내역 확인 후 다시 찾기' : '현재 결제 시도를 종료하고 다시 찾기'}
    >
      <Stack gap="lg">
        <Text size="sm">{paymentUnknown
          ? '결제 결과를 확인하지 못했습니다. 중복 예매를 막기 위해 CGV 예매 내역에 완료된 예매가 없는지 먼저 확인해야 합니다.'
          : '열려 있는 결제 화면과 현재 좌석을 포기하고 새로운 좌석 탐색을 시작합니다.'}</Text>
        <CheckField
          checked={acknowledged}
          disabled={submitting}
          label={paymentUnknown
            ? 'CGV 예매 내역에 완료된 예매가 없음을 확인했습니다.'
            : '현재 결제를 중단하고 새 좌석 탐색을 시작합니다.'}
          onChange={(event) => onAcknowledgedChange(event.currentTarget.checked)}
        />
        <Group justify="flex-end">
          <SecondaryButton disabled={submitting} onClick={onClose}>돌아가기</SecondaryButton>
          <PrimaryButton loading={submitting} disabled={!acknowledged} onClick={onConfirm}>다시 찾기</PrimaryButton>
        </Group>
      </Stack>
    </Modal>
  );
}
