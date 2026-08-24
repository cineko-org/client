export type OperationsMinimumLevel = 'warn' | 'error';

export interface OperationsLogEntry {
	sequence: number;
	time: string;
	level: 'WARN' | 'ERROR' | string;
	message: string;
	event: string;
	scenario: string;
	operation: string;
	outcome: string;
	expected?: string;
	observed?: string;
	error?: string;
	request_id?: string;
	fields?: Record<string, unknown>;
}

export interface OperationsLogAggregate {
	level: string;
	event: string;
	scenario: string;
	operation: string;
	count: number;
	last_time: string;
	last_error?: string;
}

export interface OperationsLogSnapshot {
	entries: OperationsLogEntry[];
	aggregates: OperationsLogAggregate[];
	matching: number;
	warnings: number;
	errors: number;
	invalid_lines: number;
	scanned_bytes: number;
	truncated: boolean;
}

export const emptyOperationsLogSnapshot: OperationsLogSnapshot = {
	entries: [], aggregates: [], matching: 0, warnings: 0, errors: 0,
	invalid_lines: 0, scanned_bytes: 0, truncated: false,
};

export function scenarioLabel(value: string): string {
	const labels: Record<string, string> = {
		poster_collection: '포스터 수집',
		poster_delivery: '포스터 제공',
		catalog_collection: '영화·극장 수집',
		auditorium_collection: '상영관 수집',
		schedule_collection: '상영 일정 수집',
		seat_map_collection: '좌석 배치 수집',
		booking_monitoring: '예매 모니터링',
		seat_selection: '좌석 선택',
		booking: '예매',
		network: '네트워크',
		operations: '관제',
		system: '시스템',
	};
	return labels[value] ?? (value || '분류 없음');
}

export function formatLogTime(value: string): string {
	const date = new Date(value);
	return Number.isNaN(date.getTime()) ? value : date.toLocaleString('ko-KR');
}
