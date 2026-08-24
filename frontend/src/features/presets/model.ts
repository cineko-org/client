import { create } from '@bufbuild/protobuf';
import {
	MutationIdentitySchema, PresetSchema, SeatPreferenceSchema, WebUIResourceMutationSchema,
	type Preset, type Theater, type WebUIResourceMutation,
} from '../../api/proto';

export type SeatType = string;

export type SeatMapLoadState = 'idle' | 'loading' | 'cached' | 'pending' | 'error';

export function catalogRegions(theaters: Theater[]): string[] {
  return [...new Set(theaters.map((theater) => theater.region.trim()).filter(Boolean))]
    // The spread creates a new array, so sorting cannot mutate caller state.
    // oxlint-disable-next-line unicorn/no-array-sort
    .sort((left, right) => left.localeCompare(right, 'ko'));
}

export function catalogTheaters(theaters: Theater[], region: string): string[] {
  const selectedRegion = region.trim();
  if (!selectedRegion) return [];
  return [...new Set(theaters
    .filter((theater) => theater.region.trim() === selectedRegion)
    .map((theater) => theater.name.trim())
    .filter(Boolean))]
    // The spread creates a new array, so sorting cannot mutate caller state.
    // oxlint-disable-next-line unicorn/no-array-sort
    .sort((left, right) => left.localeCompare(right, 'ko'));
}

export interface SeatTypePresentation {
  color: string;
  label: string;
}

export const seatTypePresentation: Record<string, SeatTypePresentation> = {
  standard: { color: '#707070', label: '일반석' },
  wheelchair: { color: '#79BBF8', label: '장애인석' },
  companion: { color: '#91D7FF', label: '보호자석' },
  recliner: { color: '#FFFFBB', label: '리클라이너' },
  motion: { color: '#9B8CFF', label: '4DX 모션' },
  prime: { color: '#FFB930', label: '프라임석' },
  premium: { color: '#D5B8FF', label: '프리미엄석' },
  couple: { color: '#FF9EB5', label: '커플석' },
  bed: { color: '#D7C4FF', label: '침대형 좌석' },
  unknown: { color: '#707070', label: '기타 좌석' },
};

export function seatPresentation(type: string): SeatTypePresentation {
  return seatTypePresentation[type] ?? seatTypePresentation.unknown;
}

export interface PresetForm {
	revision: number;
	id: string;
  name: string;
}

export const initialPresetForm: PresetForm = {
	id: '', revision: 0, name: '',
};

export function formFromPreset(preset: Preset): PresetForm {
	return {
		id: preset.id,
		revision: 0,
    name: preset.name,
  };
}

export function presetSummary(preset: Preset): string {
  const candidates = preset.seatPreference?.explicitSeats.slice(0, 8).join(' · ') ?? '';
  return candidates ? `선택 좌석 · ${candidates}` : '전체 좌석에서 자동 선택';
}

export function presetSaveRequest(
  form: PresetForm,
  userId: string,
  theaterId: string,
  auditoriumId: string,
  pickedSeats: string[],
  commandId = '',
): WebUIResourceMutation {
	return create(WebUIResourceMutationSchema, {
		mutation: create(MutationIdentitySchema, { commandId, expectedRevision: BigInt(form.revision) }),
		resource: {
			case: 'preset',
			value: create(PresetSchema, {
				id: form.id, userId, name: form.name.trim(), theaterId, auditoriumId,
				seatPreference: create(SeatPreferenceSchema, {
					explicitSeats: [...pickedSeats],
				}),
			}),
		},
	});
}
