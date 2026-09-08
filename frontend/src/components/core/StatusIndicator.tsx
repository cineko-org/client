import { Box, Group, Indicator, Text, Tooltip } from '@mantine/core';

export type IndicatorState = 'checking' | 'off' | 'failed' | 'ready';

const states = {
  checking: { color: 'orange', label: '확인 중' },
  off: { color: '#000000', label: '꺼짐' },
  failed: { color: 'red', label: '실패' },
  ready: { color: 'green', label: '정상' },
} as const;

export interface StatusIndicatorProps {
  label: string;
  color?: string;
  processing?: boolean;
  muted?: boolean;
  state?: IndicatorState;
}

export function StatusIndicator({ label, color = 'gray', processing = false, muted = false, state }: StatusIndicatorProps) {
  const status = state ? states[state] : undefined;
  const content = (
    <Group gap={8} wrap="nowrap">
      <Indicator inline size={8} color={status?.color ?? color} processing={state ? state === 'checking' : processing} position="middle-center">
        <Box w={8} h={8} role={status ? 'img' : undefined} aria-label={status ? `${label}: ${status.label}` : undefined} />
      </Indicator>
      <Text component="span" size="xs" fw={500} c={(state ? state === 'off' : muted) ? 'gray.6' : 'gray.3'}>{label}</Text>
    </Group>
  );
  return status ? <Tooltip label={status.label}>{content}</Tooltip> : content;
}
