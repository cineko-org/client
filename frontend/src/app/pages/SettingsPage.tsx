import { Divider, Stack } from '@mantine/core';
import { PageHeader } from '../../components/core/PageHeader';
import { AccountSettingsView } from '../../features/settings/ui/AccountSettingsView';
import { ProxySettingsView, type ProxySettingsViewProps } from '../../features/settings/ui/ProxySettingsView';
import { HookSettingsView } from '../../features/settings/ui/HookSettingsView';
import type { HookTargetForm } from '../../features/settings/hookModel';
import type { WebUIAccountState } from '../../api/proto';

export interface SettingsPageProps extends ProxySettingsViewProps {
  account: WebUIAccountState;
  onAuthenticate: () => void;
  hookAvailable: boolean;
  hookForms: HookTargetForm[];
  hookLoadState: ProxySettingsViewProps['loadState'];
  hookSaving: boolean;
  onHookAdd: () => void;
  onHookReload: () => void;
  onHookChange: (index: number, value: HookTargetForm) => void;
  onHookRemove: (index: number) => void;
  onHookSave: () => void;
}

export function SettingsPage({
  account, onAuthenticate, hookAvailable, hookForms, hookLoadState, hookSaving,
  onHookAdd, onHookReload, onHookChange, onHookRemove, onHookSave, ...proxy
}: SettingsPageProps) {
  return (
    <Stack gap="xl">
      <PageHeader title="설정" description="CGV 로그인, 네트워크, 외부 알림을 관리합니다." />
      <AccountSettingsView account={account} onAuthenticate={onAuthenticate} />
      <Divider />
      <ProxySettingsView {...proxy} />
      <Divider />
      <HookSettingsView available={hookAvailable} forms={hookForms} loadState={hookLoadState} saving={hookSaving}
        onAdd={onHookAdd} onReload={onHookReload} onChange={onHookChange} onRemove={onHookRemove} onSave={onHookSave} />
    </Stack>
  );
}
