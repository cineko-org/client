import type { ReactNode } from 'react';
import { Group, Stack, Text } from '@mantine/core';

export interface PageHeaderProps {
  eyebrow?: string;
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  level?: 1 | 2;
}

export function PageHeader({ eyebrow, title, description, actions, level = 1 }: PageHeaderProps) {
  return (
    <Group justify="space-between" align="flex-start" wrap="wrap" gap="lg">
      <Stack gap={4} style={{ minWidth: 0, flex: 1 }}>
        {eyebrow ? <Text size="xs" fw={700} c="cineko.4" tt="uppercase" lts="0.12em">{eyebrow}</Text> : null}
        <Text component={`h${level}`} fz={level === 1 ? '2rem' : '1.25rem'} fw={700} lh={1.2} m={0}>{title}</Text>
        {description ? <Text component="p" size="sm" c="dimmed" m={0} maw={720}>{description}</Text> : null}
      </Stack>
      {actions ? <Group gap="xs">{actions}</Group> : null}
    </Group>
  );
}
