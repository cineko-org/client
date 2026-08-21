import { useCallback, useRef, useState } from 'react';
import { api, errorMessage, isRevisionConflict } from '../../api/client';
import { ResourceSchema, WebUIResourceMutationSchema, type WebUIState } from '../../api/proto';
import { monitorResources, resourceRevision } from '../../api/resources';
import type { Notify } from '../../components/core/feedback';
import { formFromMonitor, initialMonitorForm, monitorFormError, monitorSaveRequest, type MonitorForm } from './model';

export function useMonitorEditor(
	state: WebUIState,
	userId: string,
	reload: () => Promise<WebUIState>,
  notify: Notify,
  onSaved: () => void,
) {
  const [form, setForm] = useState<MonitorForm>(initialMonitorForm);
  const [submitting, setSubmitting] = useState(false);
	const createCommandId = useRef(crypto.randomUUID());

  const save = useCallback(async () => {
    setSubmitting(true);
	let saved = false;
	try {
		const mutation = monitorSaveRequest(form, userId, form.id ? '' : createCommandId.current);
		await api('/api/monitors', ResourceSchema, {
			method: form.id ? 'PUT' : 'POST',
		}, WebUIResourceMutationSchema, mutation);
		saved = true;
      await reload();
		notify(form.id ? '모니터를 수정했습니다.' : '모니터를 등록했습니다.', { important: true });
		onSaved();
		if (!form.id) createCommandId.current = crypto.randomUUID();
	} catch (error) {
		if (saved) await reload().catch(() => undefined);
		if (isRevisionConflict(error)) {
			await reload();
			notify('다른 기기에서 이 모니터를 변경했습니다. 최신 내용을 불러왔습니다.', { tone: 'warning', important: true });
		} else notify(errorMessage(error), { tone: 'error', important: true });
    } finally {
      setSubmitting(false);
    }
  }, [form, notify, onSaved, reload, userId]);

  const requestCreate = useCallback(async () => {
    const validation = monitorFormError(form);
    if (validation) {
      notify(validation, { tone: 'error' });
      return;
    }
    await save();
  }, [form, notify, save]);

  const edit = useCallback((id: string) => {
		const resource = monitorResources(state).find((item) => item.resource.case === 'monitor' && item.resource.value.id === id);
		const monitor = resource?.resource.case === 'monitor' ? resource.resource.value : undefined;
		if (!monitor) return false;
		setForm(formFromMonitor(monitor, resourceRevision(resource)));
    return true;
	}, [state]);

	const newMonitor = useCallback(() => {
		createCommandId.current = crypto.randomUUID();
		setForm(initialMonitorForm);
	}, []);

	return {
    form, setForm, submitting,
		requestCreate, newMonitor, edit,
  };
}
