import { create } from '@bufbuild/protobuf';
import {
	LocalTimeSchema, MonitorSchema, MonitorStateSchema,
	MutationIdentitySchema, WebUIResourceMutationSchema,
	type Monitor, type Movie, type WebUIResourceMutation,
} from '../../api/proto';
import { localTimeText } from '../../api/resources';

export type MonitorStatus = 'pending' | 'running' | 'triggered' | 'booked' | 'failed' | 'stopped';

export function orderedCatalogMovies(movies: Movie[]): Movie[] {
  return [...movies];
}

export interface MonitorForm {
	revision: number;
	id: string;
  movieId: string;
  movie: string;
  presetId: string;
  seatCount: number;
  seatType: string;
  watchCancellationSeats: boolean;
  weekdays: string[];
  earliestTime: string;
  latestTime: string;
}

export const weekdayOptions = [
  { value: '0', label: '일' },
  { value: '1', label: '월' },
  { value: '2', label: '화' },
  { value: '3', label: '수' },
  { value: '4', label: '목' },
  { value: '5', label: '금' },
  { value: '6', label: '토' },
];

export const seatTypeOptions = [
  { value: 'standard', label: '일반석' },
  { value: 'wheelchair', label: '장애인/이동식' },
  { value: 'companion', label: '보호자석' },
  { value: 'recliner', label: '리클라이너' },
  { value: 'motion', label: '4DX/모션' },
  { value: 'prime', label: '프라임석' },
  { value: 'premium', label: '프리미엄석' },
  { value: 'couple', label: '커플석' },
  { value: 'bed', label: '침대형 좌석' },
];

export const initialMonitorForm: MonitorForm = {
	id: '', revision: 0, movieId: '', movie: '', presetId: '', weekdays: [],
  seatCount: 1, seatType: 'standard', watchCancellationSeats: true,
  earliestTime: '', latestTime: '',
};

export function formFromMonitor(monitor: Monitor, revision = 0): MonitorForm {
	return {
		id: monitor.id, revision, movieId: monitor.movieId, movie: monitor.movieTitle, presetId: monitor.presetId,
		seatCount: monitor.seatCount || 1, seatType: monitor.seatType || 'standard',
		watchCancellationSeats: monitor.watchCancellationSeats,
	    weekdays: monitor.targetWeekdays.map(String),
	    earliestTime: localTimeText(monitor.earliestTime), latestTime: localTimeText(monitor.latestTime),
  };
}

export function scheduleDescription(form: Pick<MonitorForm, 'weekdays'>): string {
  if (form.weekdays.length > 0) return '선택한 요일에 예매가 열릴 때까지 계속 확인합니다.';
  return '반복 요일을 하나 이상 선택하세요.';
}

export function monitorFormError(form: MonitorForm): string {
	if (!form.movieId || !form.presetId) return '영화와 좌석 프리셋을 선택하세요.';
  if (!Number.isInteger(form.seatCount) || form.seatCount < 1 || form.seatCount > 8) return '예매 인원은 1명부터 8명까지 입력하세요.';
  if (!form.seatType.trim()) return '좌석 타입을 선택하세요.';
  if (form.weekdays.length === 0) return '반복 요일을 하나 이상 선택하세요.';
  return '';
}

export function monitorSaveRequest(form: MonitorForm, userId: string, commandId = ''): WebUIResourceMutation {
	const earliest = form.earliestTime ? form.earliestTime.split(':').map(Number) : [];
	const latest = form.latestTime ? form.latestTime.split(':').map(Number) : [];
	const mutation = create(MutationIdentitySchema, { commandId, expectedRevision: BigInt(form.revision) });
	const monitor = create(MonitorSchema, {
		id: form.id, userId, presetId: form.presetId, movieId: form.movieId, movieTitle: form.movie,
		seatCount: form.seatCount, seatType: form.seatType,
		watchCancellationSeats: form.watchCancellationSeats,
		targetWeekdays: form.weekdays.map(Number),
		earliestTime: earliest.length === 2 ? create(LocalTimeSchema, { hour: earliest[0], minute: earliest[1] }) : undefined,
		latestTime: latest.length === 2 ? create(LocalTimeSchema, { hour: latest[0], minute: latest[1] }) : undefined,
		state: create(MonitorStateSchema, { state: { case: 'pending', value: {} } }),
	});
	return create(WebUIResourceMutationSchema, {
		mutation, resource: { case: 'monitor', value: monitor },
	});
}

export function monitorScheduleLabel(monitor: Partial<Monitor>): string {
  if (monitor.targetWeekdays?.length) {
    const names = monitor.targetWeekdays.map((day) => weekdayOptions[day]?.label).filter(Boolean).join(' · ');
    return `매주 ${names}요일 · 찾을 때까지 계속`;
  }
  return '대상 요일 없음';
}

export function monitorTimeLabel(monitor: Partial<Monitor>): string {
	const earliestTime = localTimeText(monitor.earliestTime);
	const latestTime = localTimeText(monitor.latestTime);
	if (earliestTime && latestTime) return `${earliestTime}–${latestTime}`;
	if (earliestTime) return `${earliestTime} 이후`;
	if (latestTime) return `${latestTime} 이전`;
  return '모든 시간대';
}

export function monitorBookingLabel(monitor: Partial<Monitor>): string {
  const count = monitor.seatCount && monitor.seatCount > 0 ? monitor.seatCount : 1;
  const type = monitor.seatType || 'standard';
  const typeLabel = seatTypeOptions.find((option) => option.value === type)?.label ?? type;
  return `${count}명 · ${typeLabel}`;
}

export function monitorWatchLabel(monitor: Partial<Monitor>): string {
  return monitor.watchCancellationSeats ? '신규 오픈 + 취소표 감시' : '신규 오픈만 감시';
}

export function monitorStatusLabel(status: string): string {
  return ({
    pending: '대기',
    running: '실행 중',
    triggered: '결제 확인 필요',
    payment_unknown: '결제 결과 확인 필요',
    booked: '예매 완료',
    failed: '실패',
    stopped: '중지',
  })[status] ?? status;
}
