import { useCallback, useEffect, useRef, useState } from 'react';
import { createRequestID, logClientEvent } from '../../api/client';
import {
	emptyOperationsLogSnapshot,
	emptyNetworkCaptureSnapshot,
	type NetworkCaptureSnapshot,
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

function isNetworkSnapshot(value: unknown): value is NetworkCaptureSnapshot {
	if (typeof value !== 'object' || value === null) return false;
	const candidate = value as Partial<NetworkCaptureSnapshot>;
	return Array.isArray(candidate.entries) && typeof candidate.matching === 'number'
		&& typeof candidate.statistics === 'object' && candidate.statistics !== null
		&& typeof candidate.statistics.provider_sent === 'number'
		&& typeof candidate.statistics.status_429 === 'number';
}

export function useOperationsLogs() {
	const [minimumLevel, setMinimumLevel] = useState<OperationsMinimumLevel>('warn');
	const [snapshot, setSnapshot] = useState<OperationsLogSnapshot>(emptyOperationsLogSnapshot);
	const [network, setNetwork] = useState<NetworkCaptureSnapshot>(emptyNetworkCaptureSnapshot);
	const [selectedNetwork, setSelectedNetwork] = useState<Record<string, unknown> | null>(null);
	const [selectedNetworkID, setSelectedNetworkID] = useState('');
	const [loading, setLoading] = useState(true);
	const [clearing, setClearing] = useState(false);
	const [error, setError] = useState('');
	const request = useRef(0);
	const failureLogged = useRef(false);

	const load = useCallback(async () => {
		const currentRequest = ++request.current;
		try {
			const requestID = createRequestID();
			const [response, networkResponse] = await Promise.all([
				fetch(`/api/logs?min_level=${minimumLevel}&limit=300`, { headers: { 'X-Request-Id': requestID } }),
				fetch('/api/logs/network?outcome=failed&limit=100', { headers: { 'X-Request-Id': requestID } }),
			]);
			if (!response.ok) throw new Error(`log snapshot request failed with ${response.status}`);
			if (!networkResponse.ok) throw new Error(`network snapshot request failed with ${networkResponse.status}`);
			const value: unknown = await response.json();
			const networkValue: unknown = await networkResponse.json();
			if (!isSnapshot(value)) throw new Error('log snapshot response contract changed');
			if (!isNetworkSnapshot(networkValue)) throw new Error('network snapshot response contract changed');
			if (currentRequest !== request.current) return;
			setSnapshot(value);
			setNetwork(networkValue);
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

	const inspectNetwork = useCallback(async (id: string) => {
		setSelectedNetworkID(id);
		setSelectedNetwork(null);
		try {
			const response = await fetch(`/api/logs/network/${encodeURIComponent(id)}`, {
				headers: { 'X-Request-Id': createRequestID() },
			});
			if (!response.ok) throw new Error(`network capture request failed with ${response.status}`);
			const value: unknown = await response.json();
			if (typeof value !== 'object' || value === null) throw new Error('network capture response contract changed');
			setSelectedNetwork(value as Record<string, unknown>);
		} catch (caught) {
			const message = caught instanceof Error ? caught.message : String(caught);
			setError(`네트워크 상세를 불러오지 못했습니다: ${message}`);
		} finally {
			setSelectedNetworkID('');
		}
	}, []);

	const clearLogs = useCallback(async () => {
		setClearing(true);
		try {
			const response = await fetch('/api/logs', {
				method: 'DELETE', headers: { 'X-Request-Id': createRequestID() },
			});
			if (!response.ok) throw new Error(`log clear request failed with ${response.status}`);
			setSnapshot(emptyOperationsLogSnapshot);
			setNetwork(emptyNetworkCaptureSnapshot);
			setSelectedNetwork(null);
			setError('');
			await load();
		} catch (caught) {
			const message = caught instanceof Error ? caught.message : String(caught);
			setError(`로컬 로그를 비우지 못했습니다: ${message}`);
			throw caught;
		} finally {
			setClearing(false);
		}
	}, [load]);

	return { minimumLevel, setMinimumLevel, snapshot, network, selectedNetwork, selectedNetworkID, inspectNetwork, loading, clearing, error, reload, clearLogs };
}
