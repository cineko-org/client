import type { Monitor, MonitorMode, MonitorStatus } from '../../api/types';

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

export interface MonitorSaveRequest {
	revision: number;
	id: string;
  userId: string;
  presetId: string;
  movieId: string;
  movie: string;
  targetDates: string[];
  targetWeekdays: number[];
  mode: MonitorMode;
  searchHorizonDays: number;
  earliestTime: string;
  latestTime: string;
  pollInterval: number;
  pollIntervalMax: number;
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

export const initialMonitorForm: MonitorForm = {
	id: '', revision: 0, movieId: '', movie: '', presetId: '', pollMinMinutes: 3, pollMaxMinutes: 8,
  monitorMode: 'opening', dates: [], weekdays: [],
  horizonDays: 28, earliestTime: '', latestTime: '',
};

const durationMinutes = (value: number | undefined, fallback: number) => value ? Math.round(value / 60_000_000_000) : fallback;

export function formFromMonitor(monitor: Monitor): MonitorForm {
	return {
		id: monitor.id, revision: monitor.revision ?? 0, movieId: monitor.movieId, movie: monitor.movie, presetId: monitor.presetId,
    pollMinMinutes: durationMinutes(monitor.pollInterval, 3),
    pollMaxMinutes: durationMinutes(monitor.pollIntervalMax, 8),
    monitorMode: monitor.mode,
    dates: [...monitor.targetDates], weekdays: monitor.targetWeekdays.map(String),
    horizonDays: monitor.searchHorizonDays || 28, earliestTime: monitor.earliestTime, latestTime: monitor.latestTime,
  };
}

export function monitorIntervalLabel(monitor: Pick<Monitor, 'pollInterval' | 'pollIntervalMax'>): string {
  return `${durationMinutes(monitor.pollInterval, 3)}–${durationMinutes(monitor.pollIntervalMax, 8)}분`;
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
  return typeof value === 'number' ? value : 28;
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

export function monitorSaveRequest(form: MonitorForm, userId: string): MonitorSaveRequest {
	return {
		id: form.id,
		revision: form.revision,
    userId,
    presetId: form.presetId,
    movieId: form.movieId,
    movie: form.movie,
    targetDates: [...form.dates],
    targetWeekdays: form.weekdays.map(Number),
    mode: form.monitorMode,
    searchHorizonDays: form.horizonDays,
    earliestTime: form.earliestTime,
    latestTime: form.latestTime,
    pollInterval: form.pollMinMinutes * 60 * 1_000_000_000,
    pollIntervalMax: form.pollMaxMinutes * 60 * 1_000_000_000,
  };
}

export function monitorScheduleLabel(monitor: Partial<Monitor>): string {
  const parts: string[] = [];
  if (monitor.targetDates?.length) parts.push(monitor.targetDates.join(' · '));
  if (monitor.targetWeekdays?.length) {
    const names = monitor.targetWeekdays.map((day) => weekdayOptions[day]?.label).filter(Boolean).join(' · ');
    parts.push(`매주 ${names}요일 · 앞으로 ${monitor.searchHorizonDays || 28}일`);
  }
  return parts.join(' / ') || '대상 일정 없음';
}

export function monitorTimeLabel(monitor: Partial<Monitor>): string {
  if (monitor.earliestTime && monitor.latestTime) return `${monitor.earliestTime}–${monitor.latestTime}`;
  if (monitor.earliestTime) return `${monitor.earliestTime} 이후`;
  if (monitor.latestTime) return `${monitor.latestTime} 이전`;
  return '모든 시간대';
}

export function monitorStatusLabel(status: MonitorStatus): string {
  return ({
    pending: '대기',
    running: '실행 중',
    triggered: '결제 확인 필요',
    payment_unknown: '결제 결과 확인 필요',
    booked: '예매 완료',
    failed: '실패',
    stopped: '중지',
  })[status];
}
