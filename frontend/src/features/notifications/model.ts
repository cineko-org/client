import type { Monitor, Reservation } from '../../api/proto';
import { monitorMovie, monitorStatus, reservationStatus } from '../../api/resources';

export type NoticeTone = 'info' | 'success' | 'warning' | 'error';

export interface Notice {
  id: string;
  message: string;
  tone: NoticeTone;
  createdAt: string;
  read: boolean;
}

export interface Feedback {
  id: string;
  message: string;
  tone: NoticeTone;
}

export function prependNotice(notices: Notice[], notice: Omit<Notice, 'read'>, limit = 30): Notice[] {
  return [{ ...notice, read: false }, ...notices].slice(0, Math.max(0, limit));
}

export function unreadNoticeCount(notices: Notice[]): number {
  return notices.filter((notice) => !notice.read).length;
}

export function markNoticesRead(notices: Notice[]): Notice[] {
  return notices.map((notice) => ({ ...notice, read: true }));
}

export function monitorTransitionMessage(previousStatus: string | undefined, monitor: Monitor): string {
	const status = monitorStatus(monitor);
	if (!previousStatus || previousStatus === status) return '';
	const movie = monitorMovie(monitor) || '영화';
	if (status === 'triggered') return `${movie} 예매 화면이 준비되었습니다. 결제를 확인하세요.`;
	if (status === 'booked') return `${movie} 예매가 완료되었습니다.`;
	if (status === 'stopped') return `${movie} 예매 찾기가 중지되었습니다.`;
  return '';
}

export function reservationTransitionMessage(previousStatus: string | undefined, reservation: Reservation): string {
	const status = reservationStatus(reservation);
	if (!previousStatus || previousStatus === status || status !== 'cancelled') return '';
	return '예매가 취소되었습니다.';
}
