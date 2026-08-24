import { create } from '@bufbuild/protobuf';
import { describe, expect, it } from 'vitest';
import {
	LocalTimeSchema, MonitorSchema, MonitorStateSchema,
	MovieSchema, PresetSchema, ProxyNetworkSchema, NetworkSettingsSchema,
	ReservationBookedSchema, ReservationCancelledSchema, ReservationSchema, SeatPreferenceSchema, TheaterSchema, WebhookTargetSchema,
	DirectNetworkSchema, type Theater,
} from '../src/api/proto';
import { emptyAppState } from '../src/features/application/model';
import {
	formFromMonitor, initialMonitorForm, monitorFormError,
	monitorBookingLabel, monitorScheduleLabel, monitorTimeLabel, monitorSaveRequest, monitorStatusLabel,
	monitorWatchLabel,
	scheduleDescription, weekdayOptions, orderedCatalogMovies,
} from '../src/features/monitors/model';
import { moviePosterSource } from '../src/features/monitors/ui/MoviePicker';
import {
	markNoticesRead, monitorTransitionMessage, prependNotice, reservationTransitionMessage,
	unreadNoticeCount, type Notice,
} from '../src/features/notifications/model';
import {
		catalogRegions, catalogTheaters, formFromPreset, initialPresetForm,
	presetSaveRequest, presetSummary, seatPresentation,
} from '../src/features/presets/model';
import { networkForm, networkSettingsInput, networkUsageDescription } from '../src/features/settings/model';
import { hookForms, hookSettingsInput, selectAllHookEvents, toggleHookEvent } from '../src/features/settings/hookModel';
import { reservationReference, reservationStatusLabel } from '../src/features/reservations/model';

const time = (value: string) => {
	const [hour, minute] = value.split(':').map(Number);
	return create(LocalTimeSchema, { hour, minute });
};
const monitorWithStatus = (status: 'pending' | 'running' | 'triggered' | 'booked' | 'failed' | 'stopped', movieTitle = '오디세이') => create(MonitorSchema, {
	id: 'monitor', movieId: 'movie', movieTitle, userId: 'user', presetId: 'preset', seatCount: 1, seatType: 'standard', targetWeekdays: [],
	state: create(MonitorStateSchema, { state: { case: status, value: status === 'failed' ? { reason: '' } : {} } }),
});
const reservationWithStatus = (status: 'booked' | 'cancelled') => create(ReservationSchema, {
	id: 'reservation', userId: 'user', monitorId: 'monitor', bookingNumber: status === 'booked' ? '1234' : '',
	state: status === 'booked'
		? { case: 'booked', value: create(ReservationBookedSchema) }
		: { case: 'cancelled', value: create(ReservationCancelledSchema) },
});

describe('application view model', () => {
	it('provides an empty protobuf state', () => {
		expect(emptyAppState).toMatchObject({ userId: 'local-user', resources: [] });
		expect(emptyAppState.catalog?.movies).toEqual([]);
	});
});

describe('monitor model', () => {
	it('uses only the local poster cache route and rejects provider URLs', () => {
		const movie = create(MovieSchema, {
			id: `movie_${'a'.repeat(32)}`,
			title: '긴 영화 제목',
			posterUrl: `/v1/catalog/posters/movie_${'a'.repeat(32)}?v=${'b'.repeat(64)}`,
		});
		expect(moviePosterSource(movie)).toBe(`/api/catalog/posters/movie_${'a'.repeat(32)}?v=${'b'.repeat(64)}`);
		movie.posterUrl = 'https://cdn.cgv.co.kr/poster.jpg';
		expect(moviePosterSource(movie)).toBe('');
	});

	it('exposes every weekday', () => {
		expect(weekdayOptions).toHaveLength(7);
	});

	it.each([
		[{ ...initialMonitorForm }, '반복 요일을 하나 이상 선택하세요.'],
		[{ ...initialMonitorForm, weekdays: ['1'] }, '선택한 요일에 예매가 열릴 때까지 계속 확인합니다.'],
	])('describes schedule %#', (form, expected) => expect(scheduleDescription(form)).toBe(expected));

	it('validates required monitor selections', () => {
		expect(monitorFormError(initialMonitorForm)).toBe('영화와 좌석 프리셋을 선택하세요.');
		const selected = { ...initialMonitorForm, movieId: 'movie', movie: '영화', presetId: 'preset' };
		expect(monitorFormError({ ...selected, seatCount: 0 })).toBe('예매 인원은 1명부터 8명까지 입력하세요.');
		expect(monitorFormError({ ...selected, seatType: '' })).toBe('좌석 타입을 선택하세요.');
		expect(monitorFormError(selected)).toBe('반복 요일을 하나 이상 선택하세요.');
		expect(monitorFormError({ ...selected, weekdays: ['1'] })).toBe('');
	});

	it('maps the editor form to the protobuf resource mutation', () => {
		const form = {
			...initialMonitorForm, id: 'monitor', movieId: 'movie', movie: '영화', presetId: 'preset',
			weekdays: ['1', '6'],
		};
		const mutation = monitorSaveRequest(form, 'user');
		expect(mutation.resource.case).toBe('monitor');
		if (mutation.resource.case !== 'monitor') return;
		expect(mutation.resource.value).toMatchObject({
			id: 'monitor', userId: 'user', movieId: 'movie', seatCount: 1, seatType: 'standard',
			watchCancellationSeats: true, targetWeekdays: [1, 6],
		});
	});

	it('keeps explicit monitor time windows in the protobuf mutation', () => {
		const form = {
			...initialMonitorForm, movieId: 'movie', movie: '영화', presetId: 'preset', weekdays: ['1'],
			earliestTime: '18:00', latestTime: '22:30',
		};
		const mutation = monitorSaveRequest(form, 'user');
		if (mutation.resource.case !== 'monitor') throw new Error('expected monitor resource');
		expect(mutation.resource.value.earliestTime).toMatchObject({ hour: 18, minute: 0 });
		expect(mutation.resource.value.latestTime).toMatchObject({ hour: 22, minute: 30 });
	});

	it('labels monitor schedules and time windows', () => {
		expect(monitorScheduleLabel({})).toBe('대상 요일 없음');
		expect(monitorScheduleLabel({ targetWeekdays: [1, 6] })).toBe('매주 월 · 토요일 · 찾을 때까지 계속');
		expect(monitorScheduleLabel({ targetWeekdays: [8] })).toBe('매주 요일 · 찾을 때까지 계속');
		expect(monitorTimeLabel({ earliestTime: time('18:00'), latestTime: time('22:00') })).toBe('18:00–22:00');
		expect(monitorTimeLabel({ earliestTime: time('18:00') })).toBe('18:00 이후');
		expect(monitorTimeLabel({ latestTime: time('22:00') })).toBe('22:00 이전');
		expect(monitorTimeLabel({})).toBe('모든 시간대');
		expect(monitorBookingLabel({ seatCount: 2, seatType: 'recliner' })).toBe('2명 · 리클라이너');
		expect(monitorBookingLabel({})).toBe('1명 · 일반석');
		expect(monitorBookingLabel({ seatCount: 0, seatType: 'balcony' })).toBe('1명 · balcony');
		expect(monitorWatchLabel({ watchCancellationSeats: true })).toBe('신규 오픈 + 취소표 감시');
		expect(monitorWatchLabel({ watchCancellationSeats: false })).toBe('신규 오픈만 감시');
	expect(['pending', 'running', 'triggered', 'payment_unknown', 'booked', 'failed', 'stopped'].map((status) => monitorStatusLabel(status))).toEqual([
			'대기', '실행 중', '결제 확인 필요', '결제 결과 확인 필요', '예매 완료', '실패', '중지',
		]);
		const stored = create(MonitorSchema, {
			id: 'monitor', movieId: 'movie', movieTitle: '영화', presetId: 'preset', userId: 'user', seatCount: 2, seatType: 'recliner',
			watchCancellationSeats: true,
			targetWeekdays: [1],
			earliestTime: time('18:00'), latestTime: time('22:00'),
		});
		expect(formFromMonitor(stored)).toMatchObject({ id: 'monitor', movieId: 'movie', seatCount: 2, seatType: 'recliner', watchCancellationSeats: true, weekdays: ['1'] });
		expect(formFromMonitor(create(MonitorSchema))).toMatchObject({ seatCount: 1, seatType: 'standard', watchCancellationSeats: false });
		expect(monitorStatusLabel('unknown')).toBe('unknown');
	});

	it('keeps the provider movie order', () => {
		const movies = [create(MovieSchema, { id: 'first', title: '가' }), create(MovieSchema, { id: 'second', title: '나' })];
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
		expect(monitorTransitionMessage('running', monitorWithStatus('stopped'))).toBe('오디세이 예매 찾기가 중지되었습니다.');
		expect(monitorTransitionMessage('running', monitorWithStatus('failed'))).toBe('');
		expect(monitorTransitionMessage('booked', monitorWithStatus('booked'))).toBe('');
		expect(reservationTransitionMessage('booked', reservationWithStatus('cancelled'))).toBe('예매가 취소되었습니다.');
		expect(reservationTransitionMessage(undefined, reservationWithStatus('cancelled'))).toBe('');
		expect(reservationTransitionMessage('booked', reservationWithStatus('booked'))).toBe('');
	});
});

	describe('preset model', () => {
	const preset = create(PresetSchema, {
		id: 'preset', name: '중앙',
		seatPreference: create(SeatPreferenceSchema, { explicitSeats: ['H10', 'H11'] }),
	});
	it('maps stored preset state into a seat-selection form', () => {
		expect(formFromPreset(preset)).toEqual({ id: 'preset', revision: 0, name: '중앙' });
		expect(initialPresetForm).toEqual({ id: '', revision: 0, name: '' });
	});
	it('renders future provider seat types with the unknown fallback', () => {
		expect(seatPresentation('future-provider-seat')).toEqual(seatPresentation('unknown'));
	});
	it('builds selectable regions and theaters from normalized catalog names', () => {
		const theaters: Theater[] = [
			create(TheaterSchema, { region: ' 부산 ', name: ' 센텀시티 ' }), create(TheaterSchema, { region: '서울', name: '여의도' }),
			create(TheaterSchema, { region: '서울', name: '용산아이파크몰' }), create(TheaterSchema, { region: '서울', name: '여의도' }),
			create(TheaterSchema, { region: ' ', name: '무시' }),
		];
		expect(catalogRegions(theaters)).toEqual(['부산', '서울']);
		expect(catalogTheaters(theaters, '서울')).toEqual(['여의도', '용산아이파크몰']);
		expect(catalogTheaters(theaters, '')).toEqual([]);
	});
	it('summarizes selected or automatic candidate seats', () => {
		expect(presetSummary(preset)).toBe('선택 좌석 · H10 · H11');
		const automatic = create(PresetSchema, { ...preset, seatPreference: create(SeatPreferenceSchema, { ...preset.seatPreference, explicitSeats: [] }) });
		expect(presetSummary(automatic)).toBe('전체 좌석에서 자동 선택');
		expect(presetSummary(create(PresetSchema, { id: 'empty', name: '빈 프리셋' }))).toBe('전체 좌석에서 자동 선택');
	});
	it('maps the editor form to a protobuf mutation without sharing mutable seats', () => {
		const seats = ['H10', 'H11'];
		const request = presetSaveRequest(formFromPreset(preset), 'user', 'theater', 'auditorium', seats, 'command-preset');
		seats.push('H12');
		expect(request.mutation?.commandId).toBe('command-preset');
		expect(request.resource.case).toBe('preset');
		if (request.resource.case !== 'preset') return;
		expect(request.resource.value).toMatchObject({ id: 'preset', userId: 'user', name: '중앙', theaterId: 'theater', auditoriumId: 'auditorium' });
		expect(request.resource.value.seatPreference?.explicitSeats).toEqual(['H10', 'H11']);
		expect(request.resource.value.seatPreference?.preferredRows).toEqual([]);
		expect(request.resource.value.seatPreference?.preferredTypes).toEqual([]);
		expect(request.resource.value.seatPreference?.together).toBe(false);
	});
});

describe('network settings model', () => {
	it('maps settings and trims desktop input', () => {
		expect(networkForm()).toEqual({ mode: 'direct', proxyUrls: '', proxyUsername: '', proxyPassword: '' });
		const stored = create(NetworkSettingsSchema, { mode: { case: 'proxy', value: create(ProxyNetworkSchema, { urls: ['socks5://one:1'], username: 'user', password: 'stored' }) } });
		expect(networkForm(stored)).toEqual({ mode: 'proxy', proxyUrls: 'socks5://one:1', proxyUsername: 'user', proxyPassword: '' });
		const input = networkSettingsInput({ mode: 'proxy', proxyUrls: ' socks5://one:1\nhttps://two:2 ', proxyUsername: ' user ', proxyPassword: ' pass ' });
		expect(input.mode.case).toBe('proxy');
		if (input.mode.case !== 'proxy') throw new Error('expected proxy network mode');
		expect(input.mode.value).toMatchObject({ urls: ['socks5://one:1', 'https://two:2'], username: 'user', password: ' pass ' });
		expect(networkSettingsInput(networkForm()).mode.case).toBe('direct');
		expect(networkUsageDescription()).toBe('사용 안 함');
		expect(networkUsageDescription(create(NetworkSettingsSchema, { mode: { case: 'direct', value: create(DirectNetworkSchema) } }))).toBe('사용 안 함');
		expect(networkUsageDescription(create(NetworkSettingsSchema, { mode: { case: 'proxy', value: create(ProxyNetworkSchema) } }))).toBe('0개 표준 프록시');
		expect(networkUsageDescription(stored)).toBe('1개 표준 프록시');
	});
	it('maps hook secrets and filters without exposing stored values', () => {
		const forms = hookForms([create(WebhookTargetSchema, {
			id: 'one', name: 'Discord', url: 'https://discord.com/api/webhooks/1/token', secret: '', hasSecret: true, eventKinds: ['monitor.completed'], enabled: true,
		})]);
		expect(forms[0]).toMatchObject({ id: 'one', secret: '', eventKinds: ['monitor.completed'], hasSecret: true, kind: 'discord' });
		const targets = hookSettingsInput([{ ...forms[0], name: ' Discord ', url: ' https://discord.com/api/webhooks/1/token ', eventKinds: ['monitor.completed', 'reservation.cancelled', 'monitor.completed'] }]);
		expect(targets[0]).toMatchObject({ name: 'Discord', url: 'https://discord.com/api/webhooks/1/token', eventKinds: ['monitor.completed', 'reservation.cancelled'], secret: '' });
		expect(selectAllHookEvents(true)).toEqual([]);
		expect(selectAllHookEvents(false)).toEqual(['monitor.completed', 'monitor.failed', 'monitor.stopped', 'payment.expired', 'reservation.cancelled']);
		expect(toggleHookEvent(['monitor.completed'], 'monitor.failed', true)).toEqual(['monitor.completed', 'monitor.failed']);
		expect(toggleHookEvent(['monitor.completed', 'monitor.failed'], 'monitor.completed', false)).toEqual(['monitor.failed']);
	});
});

describe('reservation model', () => {
	it('labels known states and preserves unknown states', () => {
		expect(['booked', 'cancelled', 'cancellationCommitting', 'cancellationUnknown', 'prepared', 'custom'].map(reservationStatusLabel)).toEqual(['예약 완료', '취소 완료', '취소 처리 중', '취소 결과 확인 필요', '결제 준비', 'custom']);
		expect(reservationReference('booked', '1234-5678')).toBe('1234-5678');
		expect(reservationReference('prepared', '')).toBe('결제를 기다리는 중');
		expect(reservationReference('cancellationCommitting', '')).toBe('취소 처리를 확인하는 중');
		expect(reservationReference('cancellationUnknown', '')).toBe('CGV 취소 내역 확인 필요');
		expect(reservationReference('custom', '')).toBe('예매번호 없음');
	});
});
