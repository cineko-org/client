import type { ReactNode } from 'react';
import { Box, Group, Stack, Text } from '@mantine/core';

export interface SectionProps {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  subtle?: boolean;
}

export function Section({ title, description, actions, children, subtle = false }: SectionProps) {
  return (
    <Box
      py="lg"
      px={subtle ? 0 : 'md'}
      style={{ borderTop: '1px solid var(--mantine-color-dark-4)' }}
    >
      <Stack gap="lg">
        <Group justify="space-between" align="flex-start" wrap="wrap" gap="md">
          <Box style={{ minWidth: 0 }}>
            <Text component="h2" size="lg" fw={700} m={0}>{title}</Text>
            {description ? <Text size="sm" c="dimmed" mt={2}>{description}</Text> : null}
          </Box>
          {actions ? <Group gap="xs">{actions}</Group> : null}
        </Group>
        {children}
      </Stack>
    </Box>
  );
}

export function EmptyState({ children }: { children: ReactNode }) {
  return <Text c="dimmed" size="sm" ta="center" py="lg">{children}</Text>;
}
