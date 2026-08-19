import { Box, Stack, Text } from '@mantine/core';
import { StatusIndicator } from './StatusIndicator';

export interface MetricProps {
  label: string;
  value: number | string;
  detail: string;
  color?: string;
  processing?: boolean;
}

export function Metric({ label, value, detail, color = 'gray', processing = false }: MetricProps) {
  return (
    <Box py="lg" h="100%" mih={124} style={{ borderTop: '1px solid var(--mantine-color-dark-4)' }}>
      <Stack gap="sm" justify="space-between" h="100%">
        <StatusIndicator label={label} color={color} processing={processing} />
        <Text fz="1.75rem" fw={700} lh={1.05}>{value}</Text>
        <Text size="xs" c="gray.5">{detail}</Text>
      </Stack>
    </Box>
  );
}
