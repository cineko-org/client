import { Box, Group, Indicator, Text } from '@mantine/core';

export interface StatusIndicatorProps {
  label: string;
  color?: string;
  processing?: boolean;
  muted?: boolean;
}

export function StatusIndicator({ label, color = 'gray', processing = false, muted = false }: StatusIndicatorProps) {
  return (
    <Group gap={8} wrap="nowrap">
      <Indicator inline size={8} color={color} processing={processing} position="middle-center">
        <Box w={8} h={8} />
      </Indicator>
      <Text component="span" size="xs" fw={500} c={muted ? 'gray.6' : 'gray.3'}>{label}</Text>
    </Group>
  );
}
