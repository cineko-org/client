import { create } from '@bufbuild/protobuf';
import {
	DirectNetworkSchema, NetworkSettingsSchema, ProxyNetworkSchema, type NetworkSettings,
} from '../../api/proto';

export type SettingsLoadState = 'unavailable' | 'idle' | 'loading' | 'ready' | 'error';

export interface NetworkForm {
	mode: 'direct' | 'proxy';
  proxyUrls: string;
  proxyUsername: string;
  proxyPassword: string;
}

export function networkForm(settings?: NetworkSettings): NetworkForm {
  return {
		mode: settings?.mode.case === 'proxy' ? 'proxy' : 'direct',
	    proxyUrls: settings?.mode.case === 'proxy' ? settings.mode.value.urls.join('\n') : '',
	    proxyUsername: settings?.mode.case === 'proxy' ? settings.mode.value.username : '',
    proxyPassword: '',
  };
}

export function networkSettingsInput(form: NetworkForm): NetworkSettings {
	return create(NetworkSettingsSchema, {
		mode: form.mode === 'proxy'
			? { case: 'proxy', value: create(ProxyNetworkSchema, {
				urls: form.proxyUrls.split(/[,\n]/).map((value) => value.trim()).filter(Boolean),
				username: form.proxyUsername.trim(), password: form.proxyPassword,
			}) }
			: { case: 'direct', value: create(DirectNetworkSchema) },
	});
}

export function networkUsageDescription(settings?: NetworkSettings): string {
	if (settings?.mode.case === 'proxy') return `${settings.mode.value.urls.length}개 표준 프록시`;
	return '사용 안 함';
}
