import { Children, type ReactNode } from 'react';
import { SimpleGrid, type MantineSpacing } from '@mantine/core';

export interface ColumnsProps {
  children: ReactNode;
  gap?: MantineSpacing;
}

export function Columns({ children, gap = 'md' }: ColumnsProps) {
  return (
    <SimpleGrid cols={{ base: 1, sm: Math.min(2, Children.count(children)), lg: Children.count(children) }} spacing={gap}>
      {children}
    </SimpleGrid>
  );
}
