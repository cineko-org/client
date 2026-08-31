import { MantineProvider } from '@mantine/core';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cinekoTheme } from '../src/app/theme';
import { formatLogTime, scenarioLabel } from '../src/features/operations/model';
import { OperationsLogView } from '../src/features/operations/ui/OperationsLogView';

beforeEach(() => {
	vi.stubGlobal('matchMedia', vi.fn<(query: string) => MediaQueryList>().mockImplementation((query) => ({
		matches: false, media: query, onchange: null,
		addEventListener: () => undefined, removeEventListener: () => undefined,
		addListener: () => undefined, removeListener: () => undefined,
		dispatchEvent: () => true,
	})));
	vi.stubGlobal('ResizeObserver', class {
		observe() { /* test stub */ }
		unobserve() { /* test stub */ }
		disconnect() { /* test stub */ }
	});
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
});

describe('operations log view', () => {
	it('labels unknown scenarios and preserves invalid timestamps', () => {
		expect(scenarioLabel('provider_dialog')).toBe('provider_dialog');
		expect(scenarioLabel('')).toBe('분류 없음');
		expect(formatLogTime('not-a-time')).toBe('not-a-time');
		expect(formatLogTime('2026-08-24T01:02:03Z')).not.toBe('2026-08-24T01:02:03Z');
	});

	it('shows warning and error aggregates and can request the error-only filter', async () => {
		const onMinimumLevelChange = vi.fn<(value: 'warn' | 'error') => void>();
		const onClear = vi.fn<() => Promise<void>>().mockResolvedValue(undefined);
		render(
			<MantineProvider theme={cinekoTheme}>
				<OperationsLogView
					minimumLevel="warn"
					snapshot={{
						matching: 3, warnings: 2, errors: 1, invalid_lines: 0,
						scanned_bytes: 4096, truncated: false,
						aggregates: [{
							level: 'WARN', event: 'scanner.schedule.partial',
							scenario: 'schedule_collection', operation: 'capture_theater_schedule',
							count: 2, last_time: '2026-08-24T01:02:03Z',
						}],
						entries: [{
							sequence: 7, time: '2026-08-24T01:02:03Z', level: 'ERROR',
							message: 'seat selection failed', event: 'cgv.seat_selection.open.failed',
							scenario: 'seat_selection', operation: 'open_seat_selection',
							outcome: 'failed', expected: 'seat page', observed: 'dialog blocked',
							error: 'unexpected dialog',
						}],
					}}
					loading={false}
					error=""
					network={{
						entries: [], matching: 0,
						statistics: { captured: 81, provider_sent: 19, blocked: 59, failed: 0, status_429: 0, truncated: false },
					}}
					onMinimumLevelChange={onMinimumLevelChange}
					onReload={vi.fn<() => void>()}
					onClear={onClear}
				/>
			</MantineProvider>,
		);

		expect(screen.getByText('상영 일정 수집')).not.toBeNull();
		expect(screen.getByText('좌석 선택')).not.toBeNull();
		expect(screen.getByText('scanner.schedule.partial')).not.toBeNull();
		expect(screen.getByText('unexpected dialog')).not.toBeNull();
		expect(screen.getByText('CGV 요청 전송')).not.toBeNull();
		expect(screen.getByText('19')).not.toBeNull();
		expect(screen.getByText('HTTP 429')).not.toBeNull();
		expect(screen.getByText(/브라우저 리소스 59건 · HTTP 응답이나 오류가 아닙니다/)).not.toBeNull();

		fireEvent.click(screen.getByText('오류만'));
		expect(onMinimumLevelChange).toHaveBeenCalledWith('error');
		fireEvent.click(screen.getByText('로그 비우기'));
		expect(await screen.findByText(/구조화 로그와 저장된 네트워크 요청/)).not.toBeNull();
		expect(onClear).not.toHaveBeenCalled();
	});
});
