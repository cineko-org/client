import { useCallback, useRef, useState } from 'react';
import { create } from '@bufbuild/protobuf';
import { api, errorMessage, isRevisionConflict } from '../../api/client';
import {
	MonitorResourceSchema, MutationIdentitySchema, ResourceKindSchema, WebUIActionStatusSchema,
	WebUIMonitorRetryRequestSchema, WebUIResourceDeletionSchema, type WebUIState,
} from '../../api/proto';
import { monitorResources, monitorStatus, resourceRevision, stateMonitors } from '../../api/resources';
import type { Notify } from '../../components/core/feedback';

export function useMonitorCommands(
	state: WebUIState,
	userId: string,
	reload: () => Promise<WebUIState>,
	notify: Notify,
) {
	const [deleteId, setDeleteId] = useState<string | null>(null);
	const [retryId, setRetryId] = useState<string | null>(null);
	const [retryAcknowledged, setRetryAcknowledged] = useState(false);
	const [mutationId, setMutationId] = useState<string | null>(null);
	const mutationLock = useRef<string | null>(null);

	const executeRetry = useCallback(async (id: string) => {
		if (mutationLock.current) return;
		const monitor = stateMonitors(state).find((item) => item.id === id);
		if (!monitor) return;
		mutationLock.current = id;
		setMutationId(id);
		try {
			await api('/api/monitors/retry', WebUIActionStatusSchema, { method: 'POST' }, WebUIMonitorRetryRequestSchema,
				create(WebUIMonitorRetryRequestSchema, { monitor, headful: true }));
			notify('좌석을 다시 찾습니다.', { important: true });
		} catch (error) {
			notify(errorMessage(error), { tone: 'error', important: true });
		} finally {
			mutationLock.current = null;
			setMutationId(null);
		}
	}, [notify, state]);

	const retry = useCallback((id: string) => {
		if (mutationLock.current) return;
		const monitor = stateMonitors(state).find((item) => item.id === id);
		const status = monitor ? monitorStatus(monitor) : '';
		if (!['triggered', 'payment_unknown', 'failed', 'stopped'].includes(status)) return;
		setRetryAcknowledged(false);
		setRetryId(id);
	}, [state]);

	const cancelRetry = useCallback(() => {
		if (mutationLock.current) return;
		setRetryId(null);
		setRetryAcknowledged(false);
	}, []);

	const confirmRetry = useCallback(async () => {
		if (!retryId || !retryAcknowledged || mutationLock.current) return;
		const id = retryId;
		setRetryId(null);
		setRetryAcknowledged(false);
		await executeRetry(id);
	}, [executeRetry, retryAcknowledged, retryId]);

	const remove = useCallback(async () => {
		if (!deleteId || mutationLock.current) return;
		const id = deleteId;
		mutationLock.current = id;
		setMutationId(id);
		try {
			const resource = monitorResources(state).find((item) => item.resource.case === 'monitor' && item.resource.value.id === id);
			await api('/api/monitors', WebUIActionStatusSchema, { method: 'DELETE' }, WebUIResourceDeletionSchema,
					create(WebUIResourceDeletionSchema, {
						mutation: create(MutationIdentitySchema, { expectedRevision: BigInt(resourceRevision(resource)) }),
						userId, id, kind: create(ResourceKindSchema, { kind: { case: 'monitor', value: create(MonitorResourceSchema) } }),
					}));
			await reload();
			notify('모니터를 삭제했습니다.');
		} catch (error) {
			if (isRevisionConflict(error)) {
				await reload();
				notify('다른 기기에서 이 모니터를 변경했습니다. 최신 내용을 불러왔습니다.', { tone: 'warning', important: true });
			} else notify(errorMessage(error), { tone: 'error' });
		} finally {
			mutationLock.current = null;
			setMutationId(null);
			setDeleteId(null);
		}
	}, [deleteId, notify, reload, state, userId]);

	return {
		deleteId, setDeleteId, retryMonitor: stateMonitors(state).find((monitor) => monitor.id === retryId),
		retryAcknowledged, setRetryAcknowledged, mutationId,
		retry, cancelRetry, confirmRetry, remove,
	};
}
