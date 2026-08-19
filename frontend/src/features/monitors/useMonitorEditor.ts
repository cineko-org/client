import { useCallback, useRef, useState } from 'react';
import { api, errorMessage, isRevisionConflict } from '../../api/client';
import type { AppState, Monitor } from '../../api/types';
import type { Notify } from '../../components/core/feedback';
import { formFromMonitor, initialMonitorForm, monitorFormError, monitorSaveRequest, type MonitorForm } from './model';

export function useMonitorEditor(
  state: AppState,
  userId: string,
  reload: () => Promise<AppState>,
  notify: Notify,
  onSaved: () => void,
) {
  const [form, setForm] = useState<MonitorForm>(initialMonitorForm);
  const [submitting, setSubmitting] = useState(false);
	const createCommandId = useRef(crypto.randomUUID());

  const save = useCallback(async () => {
    setSubmitting(true);
    let monitor: Monitor | undefined;
    try {
		monitor = await api<Monitor>('/api/monitors', {
			method: form.id ? 'PUT' : 'POST',
			body: form.id
				? monitorSaveRequest(form, userId)
				: { ...monitorSaveRequest(form, userId), idempotencyKey: createCommandId.current },
		});
      await reload();
		notify(form.id ? '모니터를 수정했습니다.' : '모니터를 등록했습니다.', { important: true });
		onSaved();
		if (!form.id) createCommandId.current = crypto.randomUUID();
	} catch (error) {
		if (monitor) await reload().catch(() => undefined);
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
    const monitor = state.monitors.find((item) => item.id === id);
    if (!monitor) return false;
    setForm(formFromMonitor(monitor));
    return true;
  }, [state.monitors]);

	const newMonitor = useCallback(() => {
		createCommandId.current = crypto.randomUUID();
		setForm(initialMonitorForm);
	}, []);

	return {
    form, setForm, submitting,
		requestCreate, newMonitor, edit,
  };
}
