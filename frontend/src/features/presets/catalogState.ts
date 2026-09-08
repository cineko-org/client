import type { Auditorium, Snapshot } from '../../api/proto';
import type { SeatMapLoadState } from './model';

export type CatalogPhase = 'idle' | 'discovering' | 'empty' | 'loading' | 'cached' | 'queued' | 'collecting' | 'waitingForShowtime' | 'retryScheduled' | 'error';
export interface CatalogState {
  region: string;
  theaterId: string;
  auditoriumId: string;
  auditoriums: Auditorium[];
  pickedSeats: string[];
  result: { phase: CatalogPhase; snapshot: Snapshot | null; error: string };
}
export const emptyCatalogState: CatalogState = { region: '', theaterId: '', auditoriumId: '', auditoriums: [], pickedSeats: [], result: { phase: 'idle', snapshot: null, error: '' } };
export type CatalogAction =
  | { type: 'reset'; region?: string; theaterId?: string; auditoriumId?: string; seats?: string[]; phase?: CatalogPhase }
  | { type: 'auditoriums'; values: Auditorium[] }
  | { type: 'auditorium'; id: string }
  | { type: 'resolution'; phase: CatalogPhase; snapshot?: Snapshot; error?: string }
  | { type: 'toggle'; label: string }
  | { type: 'clearSeats' };

export function catalogReducer(state: CatalogState, action: CatalogAction): CatalogState {
  switch (action.type) {
    case 'reset': return { ...emptyCatalogState, region: action.region ?? '', theaterId: action.theaterId ?? '', auditoriumId: action.auditoriumId ?? '', pickedSeats: action.seats ?? [], result: { phase: action.phase ?? 'idle', snapshot: null, error: '' } };
    case 'auditoriums': return { ...state, auditoriums: action.values, result: { phase: action.values.length ? 'idle' : 'empty', snapshot: null, error: '' } };
    case 'auditorium': return { ...state, auditoriumId: action.id, pickedSeats: state.auditoriumId === action.id ? state.pickedSeats : [], result: { phase: action.id ? 'loading' : 'idle', snapshot: null, error: '' } };
    case 'resolution': return { ...state,
      pickedSeats: action.snapshot ? state.pickedSeats.filter((label) => action.snapshot?.layout?.seats.some((seat) => seat.label === label)) : state.pickedSeats,
      result: { phase: action.phase, snapshot: action.snapshot ?? null, error: action.error ?? '' },
    };
    case 'toggle': return { ...state, pickedSeats: state.pickedSeats.includes(action.label) ? state.pickedSeats.filter((label) => label !== action.label) : [...state.pickedSeats, action.label] };
    case 'clearSeats': return { ...state, pickedSeats: [] };
  }
}

export function catalogPresentation(state: CatalogState) {
  const phase = state.result.phase;
  const messages: Record<CatalogPhase, string> = {
    idle: state.auditoriums.length ? `확인된 상영관 ${state.auditoriums.length}개를 불러왔습니다.` : '',
    discovering: '이 영화관의 상영관을 확인하고 있습니다.', empty: '아직 예매 가능한 상영관을 찾지 못했습니다. 잠시 후 다시 시도해 주세요.',
    loading: '저장된 좌석 배치를 확인합니다.', cached: '저장된 좌석 배치를 불러왔습니다.',
    queued: '좌석 배치 수집을 기다리고 있습니다.', collecting: '좌석 배치를 수집하고 있습니다.',
    waitingForShowtime: '좌석을 확인할 수 있는 상영 회차를 기다리고 있습니다.', retryScheduled: '좌석 배치 수집을 다시 시도할 예정입니다.', error: state.result.error,
  };
  const seatMapLoadState: SeatMapLoadState = phase === 'cached' || phase === 'loading' || phase === 'error' ? phase : phase === 'idle' || phase === 'discovering' || phase === 'empty' ? 'idle' : 'pending';
  return { catalogMessage: messages[phase], loadingCatalog: phase === 'discovering' || phase === 'loading', seatMapLoadState };
}
