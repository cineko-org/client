import { useCallback, useState } from 'react';
import { clone, create } from '@bufbuild/protobuf';
import { api, createRequestID, errorMessage, isRevisionConflict } from '../../api/client';
import {
	MonitorResourceSchema, MonitorSchema, MutationIdentitySchema, ResourceKindSchema, ResourceSchema, WebUIActionStatusSchema,
	WebUIMonitorRetryRequestSchema, WebUIResourceDeletionSchema, WebUIResourceMutationSchema, type WebUIState,
} from '../../api/proto';
import { monitorResources, monitorStatus, resourceRevision, stateMonitors } from '../../api/resources';
import type { Notify } from '../../components/core/feedback';
import { useExclusiveOperation } from '../../shared/useExclusiveOperation';
import { refreshAfterMutation } from '../../shared/refreshAfterMutation';

export function useMonitorCommands(
	state: WebUIState,
	userId: string,
	reload: () => Promise<WebUIState>,
	notify: Notify,
) {
	const [deleteId, setDeleteId] = useState<string | null>(null);
	const [retryId, setRetryId] = useState<string | null>(null);
	const [retryAcknowledged, setRetryAcknowledged] = useState(false);
	const { active: mutationId, start, isRunning } = useExclusiveOperation();

	const executeRetry = useCallback(async (id: string) => {
		const monitor = stateMonitors(state).find((item) => item.id === id);
		if (!monitor) return;
		const release = start(id);
		if (!release) return;
		try {
			await api('/api/monitors/retry', WebUIActionStatusSchema, { method: 'POST' }, WebUIMonitorRetryRequestSchema,
				create(WebUIMonitorRetryRequestSchema, { monitor, headful: true }));
			notify('예매 찾기를 켰습니다.', { important: true });
			await refreshAfterMutation(reload, notify);
		} catch (error) {
			notify(errorMessage(error), { tone: 'error', important: true });
		} finally {
			release();
		}
	}, [notify, reload, start, state]);

	const retry = useCallback((id: string) => {
		if (isRunning()) return;
		const monitor = stateMonitors(state).find((item) => item.id === id);
		const status = monitor ? monitorStatus(monitor) : '';
		if (!['triggered', 'payment_unknown', 'failed', 'stopped'].includes(status)) return;
		if (status === 'stopped') {
			void executeRetry(id);
			return;
		}
		setRetryAcknowledged(false);
		setRetryId(id);
	}, [executeRetry, isRunning, state]);

	const stop = useCallback(async (id: string) => {
		const monitor = stateMonitors(state).find((item) => item.id === id);
		const status = monitor ? monitorStatus(monitor) : '';
		if (!monitor || !['pending', 'running'].includes(status)) return;
		const release = start(id);
		if (!release) return;
		try {
			await api('/api/monitors/stop', WebUIActionStatusSchema, { method: 'POST' }, WebUIMonitorRetryRequestSchema,
				create(WebUIMonitorRetryRequestSchema, { monitor, headful: false }));
			notify('예매 찾기를 껐습니다.');
			await refreshAfterMutation(reload, notify);
		} catch (error) {
			notify(errorMessage(error), { tone: 'error', important: true });
		} finally {
			release();
		}
	}, [notify, reload, start, state]);

	const toggleCancellationWatch = useCallback(async (id: string) => {
		const resource = monitorResources(state).find((item) => item.resource.case === 'monitor' && item.resource.value.id === id);
		if (!resource || resource.resource.case !== 'monitor') return;
		const release = start(id);
		if (!release) return;
		try {
			const requestID = createRequestID();
			const monitor = clone(MonitorSchema, resource.resource.value);
			monitor.watchCancellationSeats = !monitor.watchCancellationSeats;
			await api('/api/monitors', ResourceSchema, { method: 'PUT' }, WebUIResourceMutationSchema,
				create(WebUIResourceMutationSchema, {
					mutation: create(MutationIdentitySchema, {
						commandId: requestID, expectedRevision: BigInt(resourceRevision(resource)),
					}),
					resource: { case: 'monitor', value: monitor },
				}));
			notify(monitor.watchCancellationSeats ? '감시 조건에 취소표를 포함했습니다.' : '감시 조건에서 취소표를 제외했습니다.');
			await refreshAfterMutation(reload, notify);
		} catch (error) {
			if (isRevisionConflict(error)) {
				await reload();
				notify('다른 변경이 있어 최신 내용을 불러왔습니다.', { tone: 'warning', important: true });
			} else notify(errorMessage(error), { tone: 'error', important: true });
		} finally {
			release();
		}
	}, [notify, reload, start, state]);

	const cancelRetry = useCallback(() => {
		if (isRunning()) return;
		setRetryId(null);
		setRetryAcknowledged(false);
	}, [isRunning]);

	const confirmRetry = useCallback(async () => {
		if (!retryId || !retryAcknowledged || isRunning()) return;
		const id = retryId;
		setRetryId(null);
		setRetryAcknowledged(false);
		await executeRetry(id);
	}, [executeRetry, isRunning, retryAcknowledged, retryId]);

	const remove = useCallback(async () => {
		if (!deleteId) return;
		const id = deleteId;
		const release = start(id);
		if (!release) return;
		try {
			const resource = monitorResources(state).find((item) => item.resource.case === 'monitor' && item.resource.value.id === id);
			const requestID = createRequestID();
			await api('/api/monitors', WebUIActionStatusSchema, {
				method: 'DELETE', headers: { 'X-Request-Id': requestID },
			}, WebUIResourceDeletionSchema,
					create(WebUIResourceDeletionSchema, {
						mutation: create(MutationIdentitySchema, {
							commandId: requestID, expectedRevision: BigInt(resourceRevision(resource)),
						}),
						userId, id, kind: create(ResourceKindSchema, { kind: { case: 'monitor', value: create(MonitorResourceSchema) } }),
					}));
			notify('예매 찾기를 삭제했습니다.');
			await refreshAfterMutation(reload, notify);
		} catch (error) {
			if (isRevisionConflict(error)) {
				await reload();
				notify('다른 기기에서 이 예매 찾기를 변경했습니다. 최신 내용을 불러왔습니다.', { tone: 'warning', important: true });
			} else notify(errorMessage(error), { tone: 'error' });
		} finally {
			release();
			setDeleteId(null);
		}
	}, [deleteId, notify, reload, start, state, userId]);

	return {
		deleteId, setDeleteId: (id: string | null) => { if (!isRunning()) setDeleteId(id); }, retryMonitor: stateMonitors(state).find((monitor) => monitor.id === retryId),
		retryAcknowledged, setRetryAcknowledged, mutationId,
		retry, stop, toggleCancellationWatch, cancelRetry, confirmRetry, remove,
	};
}
