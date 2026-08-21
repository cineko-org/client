import { create } from '@bufbuild/protobuf';
import {
	MutationIdentitySchema, PresetSchema, SeatPreferenceSchema, WebUIResourceMutationSchema,
	type Preset, type Seat, type Theater, type WebUIResourceMutation,
} from '../../api/proto';

export type SeatType = string;

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
  seatCount: number;
  seatType: SeatType;
  preferredRows: string;
}

export const initialPresetForm: PresetForm = {
	id: '', revision: 0, name: '', seatCount: 1, seatType: 'standard', preferredRows: '',
};

export function csv(value: string): string[] {
  return value.split(',').map((item) => item.trim()).filter(Boolean);
}

export function formFromPreset(preset: Preset): PresetForm {
	return {
		id: preset.id,
		revision: 0,
    name: preset.name,
    seatCount: preset.seatCount,
    seatType: preset.seatPreference?.preferredTypes[0] ?? 'standard',
    preferredRows: preset.seatPreference?.preferredRows.join(', ') ?? '',
  };
}

export function presetSummary(preset: Preset): string {
  const candidates = preset.seatPreference?.explicitSeats.slice(0, 8).join(' · ') ?? '';
  if (!candidates) {
    return preset.seatCount === 1 ? '실시간 좌석에서 1석 자동 선택' : `실시간 좌석에서 ${preset.seatCount}석 연석 자동 선택`;
  }
  return `${preset.seatCount === 1 ? '선택 후보 중 1석' : `${preset.seatCount}석 연석 필수`} · ${candidates}`;
}

export function candidateSelectionError(
  seats: Seat[], candidateLabels: string[], count: number,
): string {
  if (candidateLabels.length === 0) return '';
  if (candidateLabels.length < count) return `후보 좌석을 ${count}석 이상 선택하세요.`;
  if (count === 1) return '';
  const candidates = new Set(candidateLabels);
  const rows = new Map<string, Seat[]>();
  for (const seat of seats) {
    if (!candidates.has(seat.label)) continue;
    rows.set(seat.row, [...(rows.get(seat.row) ?? []), seat]);
  }
  for (const row of rows.values()) {
    row.sort((left, right) => left.number - right.number);
    for (let start = 0; start + count <= row.length; start += 1) {
      const group = row.slice(start, start + count);
      const consecutive = group.slice(1).every((seat, index) => (
        seat.number === group[index].number + 1 && !group[index].rightAisle && !seat.leftAisle
      ));
      if (consecutive) return '';
    }
  }
  return `선택한 후보에 ${count}석 연석이 없습니다.`;
}

export function presetSaveRequest(
  form: PresetForm,
  userId: string,
  theaterId: string,
  auditoriumId: string,
  pickedSeats: string[],
): WebUIResourceMutation {
	return create(WebUIResourceMutationSchema, {
		mutation: create(MutationIdentitySchema, { expectedRevision: BigInt(form.revision) }),
		resource: {
			case: 'preset',
			value: create(PresetSchema, {
				id: form.id, userId, name: form.name.trim(), theaterId, auditoriumId, seatCount: form.seatCount,
				seatPreference: create(SeatPreferenceSchema, {
					explicitSeats: [...pickedSeats], preferredRows: csv(form.preferredRows), preferredTypes: [form.seatType],
					together: true, avoidEdges: true,
				}),
			}),
		},
	});
}
