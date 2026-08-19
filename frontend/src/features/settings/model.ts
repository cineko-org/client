import type { NetworkSettings, NetworkSettingsInput } from '../../api/types';

export type SettingsLoadState = 'unavailable' | 'idle' | 'loading' | 'ready' | 'error';

export interface NetworkForm {
  mode: NetworkSettings['mode'];
  proxyUrls: string;
  proxyUsername: string;
  proxyPassword: string;
  soxyUrl: string;
  soxyToken: string;
  sessionTtl: string;
}

export function networkForm(settings?: NetworkSettings): NetworkForm {
  return {
    mode: settings?.mode ?? 'direct',
    proxyUrls: settings?.proxyUrls?.join('\n') ?? '',
    proxyUsername: settings?.proxyUsername ?? '',
    proxyPassword: '',
    soxyUrl: settings?.soxyUrl ?? '',
    soxyToken: '',
    sessionTtl: settings?.soxySessionTtl || '30m',
  };
}

export function networkSettingsInput(form: NetworkForm): NetworkSettingsInput {
  return {
    mode: form.mode,
    proxyUrls: form.proxyUrls.split(/[,\n]/).map((value) => value.trim()).filter(Boolean),
    proxyUsername: form.proxyUsername.trim(),
    proxyPassword: form.proxyPassword,
    soxyUrl: form.soxyUrl.trim(),
    soxyApiToken: form.soxyToken.trim(),
    soxySessionTtl: form.sessionTtl.trim() || '30m',
  };
}

export function networkUsageDescription(settings?: NetworkSettings): string {
	if (settings?.mode === 'soxy') return 'Soxy';
	if (settings?.mode === 'proxy') return `${settings.proxyUrls?.length ?? 0}개 표준 프록시`;
	return '사용 안 함';
}
