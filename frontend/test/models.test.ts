import { describe, expect, it } from 'vitest';
import { emptyAppState } from '../src/features/application/model';
import {
  formFromMonitor, initialMonitorForm, localDateString, monitorFormError, monitorIntervalLabel, monitorScheduleLabel, monitorTimeLabel,
  monitorSaveRequest, monitorStatusLabel, normalizeHorizon, scheduleBounds, scheduleDescription, weekdayOptions,
  orderedCatalogMovies,
} from '../src/features/monitors/model';
import {
  markNoticesRead, monitorTransitionMessage, prependNotice, reservationTransitionMessage,
  unreadNoticeCount, type Notice,
} from '../src/features/notifications/model';
import {
  candidateSelectionError, catalogRegions, catalogTheaters, csv, formFromPreset, initialPresetForm, presetSaveRequest,
  presetSummary,
} from '../src/features/presets/model';
import { networkForm, networkSettingsInput, networkUsageDescription } from '../src/features/settings/model';
import {
  hookForms, hookSettingsInput, selectAllHookEvents, toggleHookEvent,
} from '../src/features/settings/hookModel';
import { reservationReference, reservationStatusLabel } from '../src/features/reservations/model';
import type { CatalogMovie, Monitor, Preset, Reservation, Seat, Theater } from '../src/api/types';

const monitorWithStatus = (status: Monitor['status'], movie = '오디세이') => ({ status, movie } as Monitor);
const reservationWithStatus = (status: string, movie?: string) => ({ status, draft: { showtime: { movie } } } as Reservation);

describe('application view model', () => {
  it('provides empty state', () => {
    expect(emptyAppState).toMatchObject({ userId: 'local-user', presets: [], monitors: [] });
  });
});

describe('monitor model', () => {
  it('formats local bounds and normalizes mode and numeric input', () => {
    const today = new Date(2026, 7, 9, 12);
    expect(localDateString(today)).toBe('2026-08-09');
    expect(scheduleBounds(today)).toEqual({ today: '2026-08-09', last: '2027-08-09' });
    expect(normalizeHorizon(14)).toBe(14);
    expect(normalizeHorizon('')).toBe(14);
    expect(normalizeHorizon(28)).toBe(14);
    expect(weekdayOptions).toHaveLength(7);
  });

  it.each([
    [{ ...initialMonitorForm }, '날짜나 반복 요일을 추가하세요.'],
    [{ ...initialMonitorForm, dates: ['2026-08-10'] }, '1개 날짜를 확인합니다.'],
    [{ ...initialMonitorForm, weekdays: ['1'], horizonDays: 14 }, '앞으로 14일간 선택한 요일을 확인합니다.'],
    [{ ...initialMonitorForm, dates: ['2026-08-10'], weekdays: ['1'], horizonDays: 14 }, '1개 날짜와 앞으로 14일간 선택한 요일을 확인합니다.'],
  ])('describes schedule %#', (form, expected) => expect(scheduleDescription(form)).toBe(expected));

  it('validates required monitor selections', () => {
    expect(monitorFormError(initialMonitorForm)).toBe('영화와 좌석 프리셋을 선택하세요.');
    const selected = { ...initialMonitorForm, movieId: 'movie', movie: '영화', presetId: 'preset' };
    expect(monitorFormError(selected)).toBe('관람 날짜나 반복 요일을 하나 이상 추가하세요.');
    expect(monitorFormError({ ...selected, dates: ['2026-08-10'] })).toBe('');
    expect(monitorFormError({ ...selected, weekdays: ['1'] })).toBe('');
    expect(monitorFormError({ ...selected, dates: ['2026-08-10'], pollMaxMinutes: 3 })).toBe('최대 확인 간격은 최소 간격보다 커야 합니다.');
  });

  it('maps the editor form to the monitor API contract', () => {
    const form = {
      ...initialMonitorForm,
      id: 'monitor', movieId: 'movie', movie: '영화', presetId: 'preset', dates: ['2026-08-10'],
      weekdays: ['1', '6'], pollMinMinutes: 4, pollMaxMinutes: 9,
    };
    expect(monitorSaveRequest(form, 'user')).toMatchObject({
      id: 'monitor', userId: 'user', movieId: 'movie', targetDates: ['2026-08-10'], targetWeekdays: [1, 6],
      pollInterval: 240_000_000_000, pollIntervalMax: 540_000_000_000,
    });
  });

  it('labels monitor schedules and time windows', () => {
    expect(monitorScheduleLabel({})).toBe('대상 일정 없음');
    expect(monitorScheduleLabel({ targetDates: ['2026-08-10'] })).toBe('2026-08-10');
    expect(monitorScheduleLabel({ targetWeekdays: [1, 6], searchHorizonDays: 14 })).toBe('매주 월 · 토요일 · 앞으로 14일');
    expect(monitorScheduleLabel({ targetDates: ['2026-08-10'], targetWeekdays: [8] })).toBe('2026-08-10 / 매주 요일 · 앞으로 14일');
    expect(monitorTimeLabel({ earliestTime: '18:00', latestTime: '22:00' })).toBe('18:00–22:00');
    expect(monitorTimeLabel({ earliestTime: '18:00' })).toBe('18:00 이후');
    expect(monitorTimeLabel({ latestTime: '22:00' })).toBe('22:00 이전');
    expect(monitorTimeLabel({})).toBe('모든 시간대');
    expect(['pending', 'running', 'triggered', 'payment_unknown', 'booked', 'failed', 'stopped'].map((status) => monitorStatusLabel(status as Monitor['status']))).toEqual([
      '대기', '실행 중', '결제 확인 필요', '결제 결과 확인 필요', '예매 완료', '실패', '중지',
    ]);
    const stored = {
      id: 'monitor', movieId: 'movie', movie: '영화', presetId: 'preset', pollInterval: 180_000_000_000,
      pollIntervalMax: 480_000_000_000, mode: 'opening',
      targetDates: ['2026-08-10'], targetWeekdays: [1], searchHorizonDays: 14,
      earliestTime: '18:00', latestTime: '22:00',
    } as Monitor;
    expect(formFromMonitor(stored)).toMatchObject({ id: 'monitor', movieId: 'movie', pollMinMinutes: 3, pollMaxMinutes: 8, weekdays: ['1'], horizonDays: 14 });
    expect(monitorIntervalLabel(stored)).toBe('3–8분');
    expect(formFromMonitor({ ...stored, pollInterval: 0, pollIntervalMax: 0, searchHorizonDays: 0 })).toMatchObject({ pollMinMinutes: 3, pollMaxMinutes: 8, horizonDays: 14 });
    expect(monitorIntervalLabel({ pollInterval: 0, pollIntervalMax: 0 })).toBe('3–8분');
  });

  it('keeps the provider movie order', () => {
    const movies = [
      { id: 'first', title: '가' },
      { id: 'second', title: '나' },
    ] as CatalogMovie[];
    const ordered = orderedCatalogMovies(movies);
    expect(ordered.map((movie) => movie.id)).toEqual(['first', 'second']);
    expect(ordered).not.toBe(movies);
  });
});

describe('notification model', () => {
  const oldNotice: Notice = { id: 'old', message: 'old', tone: 'info', createdAt: '2026-08-10', read: true };

  it('bounds, counts, reads, and clears notification state', () => {
    const values = prependNotice([oldNotice], { id: 'new', message: 'new', tone: 'success', createdAt: '2026-08-10' }, 2);
    expect(values.map((item) => item.id)).toEqual(['new', 'old']);
    expect(unreadNoticeCount(values)).toBe(1);
    expect(unreadNoticeCount(markNoticesRead(values))).toBe(0);
    expect(prependNotice([], { id: 'new', message: 'new', tone: 'info', createdAt: '' }, 0)).toEqual([]);
  });

  it('reports only important monitor and reservation transitions', () => {
    expect(monitorTransitionMessage(undefined, monitorWithStatus('booked'))).toBe('');
    expect(monitorTransitionMessage('running', monitorWithStatus('triggered'))).toBe('오디세이 예매 화면이 준비되었습니다. 결제를 확인하세요.');
    expect(monitorTransitionMessage('running', monitorWithStatus('booked', ''))).toBe('영화 예매가 완료되었습니다.');
    expect(monitorTransitionMessage('running', monitorWithStatus('stopped'))).toBe('오디세이 모니터가 중지되었습니다.');
    expect(monitorTransitionMessage('running', monitorWithStatus('failed'))).toBe('');
    expect(monitorTransitionMessage('booked', monitorWithStatus('booked'))).toBe('');
    expect(reservationTransitionMessage('booked', reservationWithStatus('cancelled', '오디세이'))).toBe('오디세이 예매가 취소되었습니다.');
    expect(reservationTransitionMessage('booked', reservationWithStatus('cancelled'))).toBe('영화 예매가 취소되었습니다.');
    expect(reservationTransitionMessage(undefined, reservationWithStatus('cancelled'))).toBe('');
    expect(reservationTransitionMessage('booked', reservationWithStatus('booked'))).toBe('');
  });
});

describe('preset model', () => {
  const preset = {
    id: 'preset', name: '중앙', seatCount: 2,
    seatPreference: { candidateSeats: ['H10', 'H11'], preferredRows: ['H'], preferredTypes: ['recliner'], adjacency: 'required' },
  } as Preset;

  it('parses CSV and maps stored preset state into a form', () => {
    expect(csv(' H, , I ')).toEqual(['H', 'I']);
    expect(formFromPreset(preset)).toEqual({ id: 'preset', revision: 0, name: '중앙', seatCount: 2, seatType: 'recliner', preferredRows: 'H' });
    expect(formFromPreset({ ...preset, seatPreference: { ...preset.seatPreference, preferredTypes: [] } })).toMatchObject({ seatType: 'standard' });
    expect(initialPresetForm.seatCount).toBe(1);
  });

  it('builds selectable regions and theaters from normalized catalog names', () => {
    const theaters = [
      { region: ' 부산 ', name: ' 센텀시티 ' },
      { region: '서울', name: '여의도' },
      { region: '서울', name: '용산아이파크몰' },
      { region: '서울', name: '여의도' },
      { region: ' ', name: '무시' },
    ] as Theater[];
    expect(catalogRegions(theaters)).toEqual(['부산', '서울']);
    expect(catalogTheaters(theaters, '서울')).toEqual(['여의도', '용산아이파크몰']);
    expect(catalogTheaters(theaters, '')).toEqual([]);
  });

  it('summarizes candidate and scored seat preferences', () => {
    expect(presetSummary(preset)).toBe('2석 연석 필수 · H10 · H11');
    expect(presetSummary({ ...preset, seatCount: 1 })).toBe('선택 후보 중 1석 · H10 · H11');
    expect(presetSummary({ ...preset, seatPreference: { ...preset.seatPreference, candidateSeats: [] } })).toBe('실시간 좌석에서 2석 연석 자동 선택');
    expect(presetSummary({ ...preset, seatCount: 1, seatPreference: { ...preset.seatPreference, candidateSeats: [] } })).toBe('실시간 좌석에서 1석 자동 선택');
  });

  it('maps the editor form to the preset API contract without sharing mutable seats', () => {
    const seats = ['H10', 'H11'];
    const request = presetSaveRequest(formFromPreset(preset), 'user', 'theater', 'auditorium', seats);
    seats.push('H12');
    expect(request).toMatchObject({
      id: 'preset', userId: 'user', name: '중앙', theaterId: 'theater', auditoriumId: 'auditorium',
      seatPreference: { candidateSeats: ['H10', 'H11'], preferredRows: ['H'], adjacency: 'required', avoidEdges: true },
    });
  });

  it('validates candidate count and required adjacency before saving', () => {
    const seats = [
      { label: 'H10', row: 'H', number: 10 },
      { label: 'H11', row: 'H', number: 11 },
      { label: 'H13', row: 'H', number: 13 },
    ] as Seat[];
    expect(candidateSelectionError(seats, [], 2)).toBe('');
    expect(candidateSelectionError(seats, ['H10'], 2)).toBe('후보 좌석을 2석 이상 선택하세요.');
    expect(candidateSelectionError(seats, ['H10', 'H13'], 2)).toBe('선택한 후보에 2석 연석이 없습니다.');
    expect(candidateSelectionError(seats, ['H10', 'H11'], 2)).toBe('');
    expect(candidateSelectionError(seats, ['H10', 'H13'], 1)).toBe('');
  });
});

describe('network settings model', () => {
  it('maps settings and trims desktop input', () => {
    expect(networkForm()).toEqual({ mode: 'direct', proxyUrls: '', proxyUsername: '', proxyPassword: '' });
    expect(networkSettingsInput({
      mode: 'proxy', proxyUrls: ' socks5://one:1\nhttps://two:2 ', proxyUsername: ' user ', proxyPassword: ' pass ',
    })).toEqual({
      mode: 'proxy', proxyUrls: ['socks5://one:1', 'https://two:2'], proxyUsername: 'user', proxyPassword: ' pass ',
    });
	expect(networkUsageDescription()).toBe('사용 안 함');
	expect(networkUsageDescription({ mode: 'direct' })).toBe('사용 안 함');
	expect(networkUsageDescription({ mode: 'proxy' })).toBe('0개 표준 프록시');
	expect(networkUsageDescription({ mode: 'proxy', proxyUrls: ['http://one:1'] })).toBe('1개 표준 프록시');
  });

  it('maps hook secrets and filters without exposing stored values', () => {
    const forms = hookForms({ targets: [{
      id: 'one', name: 'Discord', kind: 'discord', url: 'https://discord.com/api/webhooks/1/token',
      eventKinds: ['monitor.completed'], enabled: true, hasSecret: true,
    }] });
    expect(forms[0]).toMatchObject({ id: 'one', secret: '', eventKinds: ['monitor.completed'], hasSecret: true });
    expect(hookSettingsInput([{ ...forms[0], name: ' Discord ', url: ' https://discord.com/api/webhooks/1/token ', eventKinds: ['monitor.completed', 'reservation.cancelled', 'monitor.completed'] }])).toEqual({
      targets: [expect.objectContaining({
        name: 'Discord', url: 'https://discord.com/api/webhooks/1/token',
        eventKinds: ['monitor.completed', 'reservation.cancelled'], secret: '',
      })],
    });
    expect(selectAllHookEvents(true)).toEqual([]);
    expect(selectAllHookEvents(false)).toEqual([
      'monitor.completed', 'monitor.failed', 'monitor.stopped', 'payment.expired', 'reservation.cancelled',
    ]);
    expect(toggleHookEvent(['monitor.completed'], 'monitor.failed', true)).toEqual(['monitor.completed', 'monitor.failed']);
    expect(toggleHookEvent(['monitor.completed', 'monitor.failed'], 'monitor.completed', false)).toEqual(['monitor.failed']);
  });
});

describe('reservation model', () => {
  it('labels known states and preserves unknown states', () => {
    expect(['booked', 'cancelled', 'cancellation_pending', 'prepared', 'abandoned', 'expired', 'unknown', 'custom'].map(reservationStatusLabel)).toEqual([
      '예약 완료', '취소 완료', '취소 처리 중', '결제 준비', '다시 찾는 중', '결제 대기 종료', '결제 결과 확인 필요', 'custom',
    ]);
    expect(reservationReference('booked', '1234-5678')).toBe('1234-5678');
    expect(reservationReference('prepared', '')).toBe('결제를 기다리는 중');
    expect(reservationReference('abandoned', '')).toBe('새 좌석을 찾기 위해 종료됨');
    expect(reservationReference('expired', '')).toBe('결제 대기 시간이 지나 종료됨');
    expect(reservationReference('unknown', '')).toBe('CGV 예매 내역 확인 필요');
    expect(reservationReference('custom', '')).toBe('예매번호 없음');
  });
});
