import { create } from '@bufbuild/protobuf';
import {
	LocalDateSchema, LocalTimeSchema, MonitorModeSchema, MonitorSchema, MonitorStateSchema,
	MutationIdentitySchema, WebUIResourceMutationSchema,
	type Monitor, type Movie, type WebUIResourceMutation,
} from '../../api/proto';
import { localDateText, localTimeText, monitorMode, seconds } from '../../api/resources';

export type MonitorMode = 'opening' | 'cancellation';
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
  pollMinMinutes: number;
  pollMaxMinutes: number;
  monitorMode: MonitorMode;
  dates: string[];
  weekdays: string[];
  horizonDays: number;
  earliestTime: string;
  latestTime: string;
}

export const defaultSearchHorizonDays = 14;
export const maximumSearchHorizonDays = 14;

export const weekdayOptions = [
  { value: '0', label: '일' },
  { value: '1', label: '월' },
  { value: '2', label: '화' },
  { value: '3', label: '수' },
  { value: '4', label: '목' },
  { value: '5', label: '금' },
  { value: '6', label: '토' },
];

export const initialMonitorForm: MonitorForm = {
	id: '', revision: 0, movieId: '', movie: '', presetId: '', pollMinMinutes: 3, pollMaxMinutes: 8,
  monitorMode: 'opening', dates: [], weekdays: [],
  horizonDays: defaultSearchHorizonDays, earliestTime: '', latestTime: '',
};

const durationMinutes = (value: { seconds: bigint; nanos: number } | undefined, fallback: number) => {
	const totalSeconds = seconds(value);
	return totalSeconds ? Math.round(totalSeconds / 60) : fallback;
};

export function formFromMonitor(monitor: Monitor, revision = 0): MonitorForm {
	return {
		id: monitor.id, revision, movieId: monitor.movieId, movie: monitor.movieTitle, presetId: monitor.presetId,
    pollMinMinutes: durationMinutes(monitor.pollInterval, 3),
	    pollMaxMinutes: durationMinutes(monitor.maximumPollInterval, 8),
	    monitorMode: monitorMode(monitor),
	    dates: monitor.targetDates.map(localDateText), weekdays: monitor.targetWeekdays.map(String),
	    horizonDays: normalizeHorizon(monitor.searchHorizonDays), earliestTime: localTimeText(monitor.earliestTime), latestTime: localTimeText(monitor.latestTime),
  };
}

export function monitorIntervalLabel(monitor: Monitor): string {
	return `${durationMinutes(monitor.pollInterval, 3)}–${durationMinutes(monitor.maximumPollInterval, 8)}분`;
}

export function localDateString(date: Date): string {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  return `${year}-${month}-${day}`;
}

export function scheduleBounds(today: Date): { today: string; last: string } {
  const last = new Date(today);
  last.setFullYear(last.getFullYear() + 1);
  return { today: localDateString(today), last: localDateString(last) };
}

export function normalizeHorizon(value: number | string): number {
  if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) return defaultSearchHorizonDays;
  return Math.max(1, Math.min(maximumSearchHorizonDays, Math.round(value)));
}

export function scheduleDescription(form: Pick<MonitorForm, 'dates' | 'weekdays' | 'horizonDays'>): string {
  if (form.dates.length > 0 && form.weekdays.length > 0) {
    return `${form.dates.length}개 날짜와 앞으로 ${form.horizonDays}일간 선택한 요일을 확인합니다.`;
  }
  if (form.dates.length > 0) return `${form.dates.length}개 날짜를 확인합니다.`;
  if (form.weekdays.length > 0) return `앞으로 ${form.horizonDays}일간 선택한 요일을 확인합니다.`;
  return '날짜나 반복 요일을 추가하세요.';
}

export function monitorFormError(form: MonitorForm): string {
	if (!form.movieId || !form.presetId) return '영화와 좌석 프리셋을 선택하세요.';
  if (form.dates.length + form.weekdays.length === 0) return '관람 날짜나 반복 요일을 하나 이상 추가하세요.';
  if (form.pollMinMinutes >= form.pollMaxMinutes) return '최대 확인 간격은 최소 간격보다 커야 합니다.';
  return '';
}

export function monitorSaveRequest(form: MonitorForm, userId: string, commandId = ''): WebUIResourceMutation {
	const targetDates = form.dates.map((value) => {
		const [year, month, day] = value.split('-').map(Number);
		return create(LocalDateSchema, { year, month, day });
	});
	const earliest = form.earliestTime ? form.earliestTime.split(':').map(Number) : [];
	const latest = form.latestTime ? form.latestTime.split(':').map(Number) : [];
	const mutation = create(MutationIdentitySchema, { commandId, expectedRevision: BigInt(form.revision) });
	const monitor = create(MonitorSchema, {
		id: form.id, userId, presetId: form.presetId, movieId: form.movieId, movieTitle: form.movie,
		mode: create(MonitorModeSchema, { mode: { case: form.monitorMode, value: {} } }),
		targetDates, targetWeekdays: form.weekdays.map(Number), searchHorizonDays: form.horizonDays,
		earliestTime: earliest.length === 2 ? create(LocalTimeSchema, { hour: earliest[0], minute: earliest[1] }) : undefined,
		latestTime: latest.length === 2 ? create(LocalTimeSchema, { hour: latest[0], minute: latest[1] }) : undefined,
		pollInterval: { seconds: BigInt(form.pollMinMinutes * 60), nanos: 0 },
		maximumPollInterval: { seconds: BigInt(form.pollMaxMinutes * 60), nanos: 0 },
		state: create(MonitorStateSchema, { state: { case: 'pending', value: {} } }),
	});
	return create(WebUIResourceMutationSchema, {
		mutation, resource: { case: 'monitor', value: monitor },
	});
}

export function monitorScheduleLabel(monitor: Partial<Monitor>): string {
  const parts: string[] = [];
  if (monitor.targetDates?.length) parts.push(monitor.targetDates.map(localDateText).join(' · '));
  if (monitor.targetWeekdays?.length) {
    const names = monitor.targetWeekdays.map((day) => weekdayOptions[day]?.label).filter(Boolean).join(' · ');
    parts.push(`매주 ${names}요일 · 앞으로 ${monitor.searchHorizonDays || defaultSearchHorizonDays}일`);
  }
  return parts.join(' / ') || '대상 일정 없음';
}

export function monitorTimeLabel(monitor: Partial<Monitor>): string {
	const earliestTime = localTimeText(monitor.earliestTime);
	const latestTime = localTimeText(monitor.latestTime);
	if (earliestTime && latestTime) return `${earliestTime}–${latestTime}`;
	if (earliestTime) return `${earliestTime} 이후`;
	if (latestTime) return `${latestTime} 이전`;
  return '모든 시간대';
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
