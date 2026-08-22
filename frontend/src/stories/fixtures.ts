import { create } from '@bufbuild/protobuf';
import {
	AuditoriumSchema, CatalogIndexSchema, LocalDateSchema, LocalTimeSchema, MonitorFailedSchema,
	MonitorPendingSchema, MonitorRunningSchema, MonitorSchema, MonitorStateSchema,
	MonitorTriggeredSchema, MovieSchema, PresetSchema, ProviderSchema,
	ReservationSchema, SeatPreferenceSchema, TheaterSchema, WebUIAccountAuthenticatedSchema,
	WebUIAccountStateSchema, DirectNetworkSchema, NetworkSettingsSchema, ProxyNetworkSchema,
	type Auditorium, type CatalogIndex, type Monitor, type Preset, type Reservation,
	type WebUIAccountState, type NetworkSettings,
} from '../api/proto';
import type { Notice } from '../features/notifications/model';
import { fourDxSeatMapFixture, imaxSeatMapFixture, liveSeatMapFixtures } from './liveSeatMaps';

export const noop = () => undefined;

export const authenticatedAccount: WebUIAccountState = create(WebUIAccountStateSchema, {
	state: { case: 'authenticated', value: create(WebUIAccountAuthenticatedSchema) },
	accountId: 'cineko-user', credentialsSaved: true,
});
export const unauthenticatedAccount: WebUIAccountState = create(WebUIAccountStateSchema, {
	state: { case: 'unauthenticated', value: {} },
});
export const checkingAccount: WebUIAccountState = create(WebUIAccountStateSchema, {
	state: { case: 'checking', value: {} },
});
export const directNetwork: NetworkSettings = create(NetworkSettingsSchema, {
	mode: { case: 'direct', value: create(DirectNetworkSchema) },
});
export const proxyNetwork: NetworkSettings = create(NetworkSettingsSchema, {
	mode: { case: 'proxy', value: create(ProxyNetworkSchema, { urls: ['socks5://127.0.0.1:1080'] }) },
});

function localDate(value: string) {
	const [year, month, day] = value.split('-').map(Number);
	return create(LocalDateSchema, { year, month, day });
}

function localTime(value: string | undefined) {
	if (!value) return undefined;
	const [hour, minute] = value.split(':').map(Number);
	return create(LocalTimeSchema, { hour, minute });
}

export const presets: Preset[] = [
	create(PresetSchema, {
		id: 'preset-imax', userId: 'user', name: '용산 IMAX 중앙 2석', theaterId: 'cgv-yongsan',
		auditoriumId: imaxSeatMapFixture.seatMap.auditoriumId, seatCount: 2,
		seatPreference: create(SeatPreferenceSchema, {
			explicitSeats: imaxSeatMapFixture.pickedSeats, preferredRows: ['H', 'I'], preferredZones: [],
			preferredTypes: ['standard'], together: true, avoidEdges: true,
		}),
	}),
	create(PresetSchema, {
		id: 'preset-fourdx', userId: 'user', name: '여의도 4DX', theaterId: 'cgv-yeouido',
		auditoriumId: fourDxSeatMapFixture.seatMap.auditoriumId, seatCount: 1,
		seatPreference: create(SeatPreferenceSchema, {
			explicitSeats: fourDxSeatMapFixture.pickedSeats, preferredRows: ['F'], preferredZones: [],
			preferredTypes: ['motion'], together: true, avoidEdges: true,
		}),
	}),
];

function monitorState(status: 'pending' | 'running' | 'triggered' | 'failed', reason = '') {
	const value = status === 'failed'
		? { case: 'failed' as const, value: create(MonitorFailedSchema, { reason }) }
		: status === 'running'
			? { case: 'running' as const, value: create(MonitorRunningSchema) }
			: status === 'triggered'
				? { case: 'triggered' as const, value: create(MonitorTriggeredSchema) }
				: { case: 'pending' as const, value: create(MonitorPendingSchema) };
	return create(MonitorStateSchema, { state: value });
}

function fixtureMonitor(input: {
	id: string;
	movieId: string;
	movieTitle: string;
	status: 'pending' | 'running' | 'triggered' | 'failed';
	date: string;
	earliestTime?: string;
	latestTime?: string;
	reason?: string;
}) {
	return create(MonitorSchema, {
		id: input.id, userId: 'user', presetId: 'preset-imax', movieId: input.movieId, movieTitle: input.movieTitle,
		targetDates: [localDate(input.date)], targetWeekdays: [], searchHorizonDays: 14,
		earliestTime: localTime(input.earliestTime), latestTime: localTime(input.latestTime),
		state: monitorState(input.status, input.reason),
	});
}

export const monitors: Monitor[] = [
	fixtureMonitor({ id: 'monitor-running', movieId: 'movie-avengers', movieTitle: '어벤져스: 시크릿 워즈', status: 'running', date: '2026-08-20', earliestTime: '18:00', latestTime: '23:30' }),
	fixtureMonitor({ id: 'monitor-triggered', movieId: 'movie-dune', movieTitle: '듄: 메시아', status: 'triggered', date: '2026-08-20', earliestTime: '19:00' }),
	fixtureMonitor({ id: 'monitor-payment-unknown', movieId: 'movie-hail-mary', movieTitle: '프로젝트 헤일메리', status: 'triggered', date: '2026-08-21', earliestTime: '20:00' }),
	fixtureMonitor({ id: 'monitor-failed', movieId: 'movie-avengers', movieTitle: '위키드: 포 굿', status: 'failed', date: '2026-08-20', earliestTime: '18:00', latestTime: '23:30', reason: 'probe unavailable' }),
];

export const reservations: Reservation[] = [
	create(ReservationSchema, { id: 'reservation-booked', userId: 'user', monitorId: monitors[0].id, bookingNumber: '1234-5678', seatLabels: ['H12', 'H13'], totalPrice: '30000' }),
	create(ReservationSchema, { id: 'reservation-pending', userId: 'user', monitorId: monitors[1].id, bookingNumber: '', seatLabels: ['F10'], totalPrice: '15000' }),
];

export const catalog: CatalogIndex = create(CatalogIndexSchema, {
	generation: 42n,
	providers: [create(ProviderSchema, { id: 'cgv', name: 'CGV' })],
	movies: [
		create(MovieSchema, { id: 'movie-dune', providerId: 'cgv', sourceKey: '듄: 메시아', title: '듄: 메시아', posterUrl: '/storybook/poster-dune.svg' }),
		create(MovieSchema, { id: 'movie-avengers', providerId: 'cgv', sourceKey: '어벤져스: 시크릿 워즈', title: '어벤져스: 시크릿 워즈', posterUrl: '/storybook/poster-avengers.svg' }),
		create(MovieSchema, { id: 'movie-hail-mary', providerId: 'cgv', sourceKey: '프로젝트 헤일메리', title: '프로젝트 헤일메리', posterUrl: '/storybook/poster-hail-mary.svg' }),
	],
	theaters: [
		create(TheaterSchema, { id: 'cgv-yongsan', providerId: 'cgv', sourceKey: '서울/용산아이파크몰', region: '서울', name: '용산아이파크몰' }),
		create(TheaterSchema, { id: 'cgv-yeouido', providerId: 'cgv', sourceKey: '서울/여의도', region: '서울', name: '여의도' }),
		create(TheaterSchema, { id: 'cgv-centum', providerId: 'cgv', sourceKey: '부산/센텀시티', region: '부산', name: '센텀시티' }),
		create(TheaterSchema, { id: 'cgv-pangyo', providerId: 'cgv', sourceKey: '경기/판교', region: '경기', name: '판교' }),
	],
});

export const auditoriums: Auditorium[] = liveSeatMapFixtures.map((fixture) => create(AuditoriumSchema, {
	id: fixture.seatMap.auditoriumId,
	theaterId: 'cgv-yongsan',
	sourceKey: `서울/용산아이파크몰/${fixture.auditorium}`,
	name: fixture.auditorium,
	screenTypes: fixture.screenTypes,
	capacity: fixture.scheduleCapacity,
	currentLayoutHash: fixture.seatMap.layoutHash,
}));

export const seatMap = imaxSeatMapFixture.seatMap;

export const notices: Notice[] = [
	{ id: 'notice-ready', tone: 'warning', message: '듄: 메시아 예매 화면이 준비되었습니다. 결제를 확인하세요.', createdAt: '2026-08-12T08:10:00Z', read: false },
	{ id: 'notice-booked', tone: 'success', message: '어벤져스: 시크릿 워즈 예매가 완료되었습니다.', createdAt: '2026-08-12T07:40:00Z', read: true },
	{ id: 'notice-proxy', tone: 'error', message: '프록시 연결을 확인할 수 없습니다.', createdAt: '2026-08-12T07:10:00Z', read: true },
];
