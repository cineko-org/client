import type { Auditorium, CatalogIndex, Monitor, Preset, Reservation, SeatMap } from '../api/types';
import type { Notice } from '../features/notifications/model';
import { fourDxSeatMapFixture, imaxSeatMapFixture, liveSeatMapFixtures } from './liveSeatMaps';

export const noop = () => undefined;

export const presets: Preset[] = [
  {
    id: 'preset-imax', userId: 'user', name: '용산 IMAX 중앙 2석', theaterId: 'cgv-yongsan',
    auditoriumId: imaxSeatMapFixture.seatMap.auditoriumId, seatCount: 2,
    seatPreference: {
      candidateSeats: imaxSeatMapFixture.pickedSeats, preferredRows: ['H', 'I'], preferredZones: [],
      preferredTypes: ['standard'], adjacency: 'required', avoidEdges: true,
    },
  },
  {
    id: 'preset-fourdx', userId: 'user', name: '여의도 4DX', theaterId: 'cgv-yeouido',
    auditoriumId: fourDxSeatMapFixture.seatMap.auditoriumId, seatCount: 1,
    seatPreference: {
      candidateSeats: fourDxSeatMapFixture.pickedSeats, preferredRows: ['F'], preferredZones: [],
      preferredTypes: ['motion'], adjacency: 'required', avoidEdges: true,
    },
  },
];

const monitorBase: Omit<Monitor, 'id' | 'movie' | 'status'> = {
  userId: 'user', presetId: 'preset-imax', mode: 'opening', targetDates: ['2026-08-20'],
  targetWeekdays: [], searchHorizonDays: 28, earliestTime: '18:00', latestTime: '23:30',
  pollInterval: 180_000_000_000, pollIntervalMax: 480_000_000_000, lastError: '',
  updatedAt: '2026-08-12T08:00:00Z',
};

export const monitors: Monitor[] = [
  { ...monitorBase, id: 'monitor-running', movie: '어벤져스: 시크릿 워즈', status: 'running' },
  {
    ...monitorBase, id: 'monitor-triggered', movie: '듄: 메시아', status: 'triggered',
    targetDates: ['2026-08-20'], earliestTime: '19:00', latestTime: '',
  },
  {
    ...monitorBase, id: 'monitor-payment-unknown', movie: '프로젝트 헤일메리', status: 'payment_unknown',
    targetDates: ['2026-08-21'], earliestTime: '20:00', latestTime: '',
  },
  { ...monitorBase, id: 'monitor-failed', movie: '위키드: 포 굿', status: 'failed', lastError: 'probe unavailable' },
];

export const reservations: Reservation[] = [
  { id: 'reservation-booked', bookingNumber: '1234-5678', status: 'booked', draft: { showtime: { movie: '어벤져스: 시크릿 워즈' }, seatLabels: ['H12', 'H13'] } },
  { id: 'reservation-pending', bookingNumber: '', status: 'prepared', draft: { showtime: { movie: '듄: 메시아' }, seatLabels: ['F10'] } },
];

export const catalog: CatalogIndex = {
  generation: 42,
  providers: [{ id: 'cgv', name: 'CGV' }],
  movies: [
    { id: 'movie-avengers', providerId: 'cgv', sourceKey: '어벤져스: 시크릿 워즈', title: '어벤져스: 시크릿 워즈' },
    { id: 'movie-dune', providerId: 'cgv', sourceKey: '듄: 메시아', title: '듄: 메시아' },
    { id: 'movie-hail-mary', providerId: 'cgv', sourceKey: '프로젝트 헤일메리', title: '프로젝트 헤일메리' },
  ],
  theaters: [
    { id: 'cgv-yongsan', providerId: 'cgv', sourceKey: '서울/용산아이파크몰', region: '서울', name: '용산아이파크몰' },
    { id: 'cgv-yeouido', providerId: 'cgv', sourceKey: '서울/여의도', region: '서울', name: '여의도' },
  ],
  auditoriums: [],
};

export const auditoriums: Auditorium[] = liveSeatMapFixtures.map((fixture) => ({
    id: fixture.seatMap.auditoriumId,
    theaterId: 'cgv-yongsan',
    sourceKey: `서울/용산아이파크몰/${fixture.auditorium}`,
    name: fixture.auditorium,
    capacity: fixture.scheduleCapacity,
    seatMapVersion: fixture.seatMap.version,
  }));

export const seatMap: SeatMap = imaxSeatMapFixture.seatMap;

export const notices: Notice[] = [
  { id: 'notice-ready', tone: 'warning', message: '듄: 메시아 예매 화면이 준비되었습니다. 결제를 확인하세요.', createdAt: '2026-08-12T08:10:00Z', read: false },
  { id: 'notice-booked', tone: 'success', message: '어벤져스: 시크릿 워즈 예매가 완료되었습니다.', createdAt: '2026-08-12T07:40:00Z', read: true },
  { id: 'notice-proxy', tone: 'error', message: '프록시 연결을 확인할 수 없습니다.', createdAt: '2026-08-12T07:10:00Z', read: true },
];
