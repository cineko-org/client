import { useCallback, useEffect, useRef, useState } from 'react';
import { createRequestID, logClientEvent } from '../../api/client';
import {
	emptyOperationsLogSnapshot,
	type OperationsLogSnapshot,
	type OperationsMinimumLevel,
} from './model';

const refreshIntervalMs = 5000;

function isSnapshot(value: unknown): value is OperationsLogSnapshot {
	if (typeof value !== 'object' || value === null) return false;
	const candidate = value as Partial<OperationsLogSnapshot>;
	return Array.isArray(candidate.entries) && Array.isArray(candidate.aggregates)
		&& typeof candidate.matching === 'number' && typeof candidate.warnings === 'number'
		&& typeof candidate.errors === 'number';
}

export function useOperationsLogs() {
	const [minimumLevel, setMinimumLevel] = useState<OperationsMinimumLevel>('warn');
	const [snapshot, setSnapshot] = useState<OperationsLogSnapshot>(emptyOperationsLogSnapshot);
	const [loading, setLoading] = useState(true);
	const [error, setError] = useState('');
	const request = useRef(0);
	const failureLogged = useRef(false);

	const load = useCallback(async () => {
		const currentRequest = ++request.current;
		try {
			const requestID = createRequestID();
			const response = await fetch(`/api/logs?min_level=${minimumLevel}&limit=300`, {
				headers: { 'X-Request-Id': requestID },
			});
			if (!response.ok) throw new Error(`log snapshot request failed with ${response.status}`);
			const value: unknown = await response.json();
			if (!isSnapshot(value)) throw new Error('log snapshot response contract changed');
			if (currentRequest !== request.current) return;
			setSnapshot(value);
			setError('');
			failureLogged.current = false;
		} catch (caught) {
			if (currentRequest !== request.current) return;
			const message = caught instanceof Error ? caught.message : String(caught);
			setError('로컬 로그를 불러오지 못했습니다.');
			if (!failureLogged.current) {
				failureLogged.current = true;
				logClientEvent('error', 'operations.logs.load.failed', {
					scenario: 'operations', operation: 'load_log_snapshot',
					expected: 'valid local log snapshot', observed: message, error: message,
				});
			}
		} finally {
			if (currentRequest === request.current) setLoading(false);
		}
	}, [minimumLevel]);

	useEffect(() => {
		const initialTimer = window.setTimeout(() => void load(), 0);
		const timer = window.setInterval(() => void load(), refreshIntervalMs);
		const requestSequence = request;
		return () => {
			requestSequence.current++;
			window.clearTimeout(initialTimer);
			window.clearInterval(timer);
		};
	}, [load]);

	const reload = useCallback(() => {
		setLoading(true);
		return load();
	}, [load]);

	return { minimumLevel, setMinimumLevel, snapshot, loading, error, reload };
}
