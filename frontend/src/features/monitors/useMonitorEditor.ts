import { useCallback, useRef, useState } from 'react';
import { api, createRequestID, errorMessage, isRevisionConflict } from '../../api/client';
import { ResourceSchema, WebUIResourceMutationSchema, type WebUIState } from '../../api/proto';
import { monitorResources, resourceRevision } from '../../api/resources';
import type { Notify } from '../../components/core/feedback';
import { useExclusiveOperation } from '../../shared/useExclusiveOperation';
import { refreshAfterMutation } from '../../shared/refreshAfterMutation';
import { formFromMonitor, initialMonitorForm, monitorFormError, monitorSaveRequest, type MonitorForm } from './model';

export function useMonitorEditor(
	state: WebUIState,
	userId: string,
	reload: () => Promise<WebUIState>,
  notify: Notify,
  onSaved: () => void,
) {
  const [form, setForm] = useState<MonitorForm>(initialMonitorForm);
  const { active, start, isRunning } = useExclusiveOperation();
	const createCommandId = useRef(createRequestID());

  const save = useCallback(async () => {
    const release = start('save');
    if (!release) return;
	try {
		const mutation = monitorSaveRequest(form, userId, form.id ? createRequestID() : createCommandId.current);
		await api('/api/monitors', ResourceSchema, {
			method: form.id ? 'PUT' : 'POST',
		}, WebUIResourceMutationSchema, mutation);
		notify(form.id ? '예매 찾기를 수정했습니다.' : '예매 찾기를 시작했습니다.', { important: true });
		await refreshAfterMutation(reload, notify);
		onSaved();
		if (!form.id) createCommandId.current = createRequestID();
	} catch (error) {
		if (isRevisionConflict(error)) {
			await reload();
			notify('다른 기기에서 이 예매 찾기를 변경했습니다. 최신 내용을 불러왔습니다.', { tone: 'warning', important: true });
		} else notify(errorMessage(error), { tone: 'error', important: true });
    } finally {
      release();
    }
  }, [form, notify, onSaved, reload, start, userId]);

  const requestCreate = useCallback(async () => {
    const validation = monitorFormError(form);
    if (validation) {
      notify(validation, { tone: 'error' });
      return;
    }
    await save();
  }, [form, notify, save]);

  const edit = useCallback((id: string) => {
    if (isRunning()) return false;
		const resource = monitorResources(state).find((item) => item.resource.case === 'monitor' && item.resource.value.id === id);
		const monitor = resource?.resource.case === 'monitor' ? resource.resource.value : undefined;
		if (!monitor) return false;
		setForm(formFromMonitor(monitor, resourceRevision(resource)));
    return true;
	}, [isRunning, state]);

	const newMonitor = useCallback(() => {
    if (isRunning()) return;
		createCommandId.current = createRequestID();
		setForm(initialMonitorForm);
	}, [isRunning]);

	return {
    form, setForm: (value: MonitorForm) => { if (!isRunning()) setForm(value); }, submitting: active !== null,
		requestCreate, newMonitor, edit,
  };
}
