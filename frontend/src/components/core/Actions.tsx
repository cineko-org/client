import type { ComponentPropsWithoutRef, ReactNode } from 'react';
import { ActionIcon, Button, Tooltip, type ActionIconProps, type ButtonProps as MantineButtonProps } from '@mantine/core';

type ButtonProps = MantineButtonProps & ComponentPropsWithoutRef<'button'>;

export function PrimaryButton(props: ButtonProps) {
  return <Button radius={0} {...props} />;
}

export function SecondaryButton(props: ButtonProps) {
  return <Button radius={0} variant="subtle" color="gray" {...props} />;
}

export function DangerButton(props: ButtonProps) {
  return <Button radius={0} variant="subtle" color="red" {...props} />;
}

export type IconActionProps = ActionIconProps & Omit<ComponentPropsWithoutRef<'button'>, keyof ActionIconProps> & {
  label: string;
  icon: ReactNode;
};

export function IconAction({ label, icon, ...props }: IconActionProps) {
  return (
    <Tooltip label={label}>
      <ActionIcon aria-label={label} radius={0} variant="subtle" color="gray" size="lg" {...props}>{icon}</ActionIcon>
    </Tooltip>
  );
}
