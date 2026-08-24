import { useCallback, useState } from 'react';
import { create } from '@bufbuild/protobuf';
import { api, createRequestID, errorMessage, isRevisionConflict, logClientEvent } from '../../api/client';
import {
	MutationIdentitySchema, PresetResourceSchema, ResourceKindSchema, ResourceSchema, WebUIActionStatusSchema,
	WebUIResourceDeletionSchema, WebUIResourceMutationSchema, type WebUIState,
} from '../../api/proto';
import { presetResources, resourceRevision, statePresets } from '../../api/resources';
import type { Notify } from '../../components/core/feedback';
import { formFromPreset, initialPresetForm, presetSaveRequest, type PresetForm } from './model';
import { usePresetCatalog } from './usePresetCatalog';

export function usePresets(
  state: WebUIState,
  userId: string,
  reload: () => Promise<WebUIState>,
  notify: Notify,
  onSaved: () => void,
) {
  const [form, setForm] = useState<PresetForm>(initialPresetForm);
  const [saving, setSaving] = useState(false);
  const [deleteId, setDeleteId] = useState<string | null>(null);
  const {
    reset: resetCatalog,
    loadPreset,
    activeTheaterId,
    auditoriumId,
    pickedSeats,
    seatMap,
    ...catalog
  } = usePresetCatalog(state, notify);

  const reset = useCallback(() => {
    setForm(initialPresetForm);
    resetCatalog();
  }, [resetCatalog]);

  const save = useCallback(async () => {
    const requestID = createRequestID();
	const started = typeof performance !== 'undefined' && typeof performance.now === 'function' ? performance.now() : Date.now();
	const seats = seatMap?.layout?.seats ?? [];
	const title = form.name.trim();
	const method = form.id ? 'PUT' : 'POST';
	const path = '/api/presets';
	const fields = {
		request_id: requestID,
		method,
		route: path,
		path,
		preset_id: form.id,
		title,
		theater_id: activeTheaterId,
		auditorium_id: auditoriumId,
		candidate_count: seats.length,
		selected_count: pickedSeats.length,
	};
	logClientEvent('info', 'preset.save.attempt', fields);
    if (!activeTheaterId || !auditoriumId) {
		logClientEvent('error', 'preset.save.validation.failed', { ...fields, reason: 'theater_or_auditorium_missing' });
      notify('지점과 상영관을 먼저 선택하세요.', { tone: 'error' });
      return;
    }
	if (!title) {
		logClientEvent('error', 'preset.save.validation.failed', { ...fields, reason: 'title_missing' });
      notify('좌석 프리셋 제목을 입력하세요.', { tone: 'error' });
      return;
    }
	if (!seatMap || seats.length === 0) {
		logClientEvent('error', 'preset.save.validation.failed', { ...fields, reason: 'seat_map_candidates_missing' });
      notify('좌석 배치 분석이 끝난 뒤 좌석 프리셋을 저장할 수 있습니다.', { tone: 'error' });
      return;
    }
		setSaving(true);
		let requestStarted = false;
		let requestCompleted = false;
		try {
			const mutation = presetSaveRequest(form, userId, activeTheaterId, auditoriumId, pickedSeats, requestID);
			logClientEvent('info', 'preset.save.request', fields);
			requestStarted = true;
			await api(path, ResourceSchema, {
				method,
				headers: { 'X-Request-Id': requestID },
			}, WebUIResourceMutationSchema, mutation);
			requestCompleted = true;
			logClientEvent('info', 'preset.save.success', {
				...fields, method, duration_ms: (typeof performance !== 'undefined' && typeof performance.now === 'function' ? performance.now() : Date.now()) - started,
			});
      notify(form.id ? '좌석 프리셋을 수정했습니다.' : '좌석 프리셋을 저장했습니다.', { important: true });
      reset();
      await reload();
      onSaved();
	} catch (error) {
		const rawError = error instanceof Error ? error.stack || error.message : String(error);
		const status = typeof error === 'object' && error !== null && 'status' in error && typeof error.status === 'number'
			? error.status : undefined;
		logClientEvent('error', 'preset.save.failure', {
			...fields,
			status,
			duration_ms: (typeof performance !== 'undefined' && typeof performance.now === 'function' ? performance.now() : Date.now()) - started,
			phase: requestStarted ? 'request' : 'prepare',
			saved: requestCompleted,
			error: rawError,
		});
		if (isRevisionConflict(error)) {
			await reload();
			notify('다른 기기에서 이 좌석 프리셋을 변경했습니다. 최신 내용을 불러왔습니다.', { tone: 'warning', important: true });
		} else {
			notify(errorMessage(error), { tone: 'error' });
		}
    } finally {
      setSaving(false);
    }
  }, [activeTheaterId, auditoriumId, form, notify, onSaved, pickedSeats, reload, reset, seatMap, userId]);

  const edit = useCallback((id: string) => {
		const resource = presetResources(state).find((item) => item.resource.case === 'preset' && item.resource.value.id === id);
		const preset = resource?.resource.case === 'preset' ? resource.resource.value : undefined;
		if (!preset) return false;
		setForm({ ...formFromPreset(preset), revision: resourceRevision(resource) });
		loadPreset(preset);
    return true;
	}, [loadPreset, state]);

  const remove = useCallback(async () => {
	if (!deleteId) return;
	try {
		const resource = presetResources(state).find((item) => item.resource.case === 'preset' && item.resource.value.id === deleteId);
		const requestID = createRequestID();
		await api('/api/presets', WebUIActionStatusSchema, {
			method: 'DELETE', headers: { 'X-Request-Id': requestID },
		}, WebUIResourceDeletionSchema,
				create(WebUIResourceDeletionSchema, {
					mutation: create(MutationIdentitySchema, {
						commandId: requestID, expectedRevision: BigInt(resourceRevision(resource)),
					}),
					userId, id: deleteId, kind: create(ResourceKindSchema, { kind: { case: 'preset', value: create(PresetResourceSchema) } }),
				}));
      await reload();
      notify('좌석 프리셋을 삭제했습니다.');
	} catch (error) {
		if (isRevisionConflict(error)) {
			await reload();
			notify('다른 기기에서 이 좌석 프리셋을 변경했습니다. 최신 내용을 불러왔습니다.', { tone: 'warning', important: true });
		} else notify(errorMessage(error), { tone: 'error' });
    } finally {
      setDeleteId(null);
    }
	}, [deleteId, notify, reload, state, userId]);

  return {
		presets: statePresets(state), form, setForm, saving, save, reset, newPreset: reset, edit,
    deleteId, setDeleteId, remove, activeTheaterId, auditoriumId, pickedSeats, seatMap, ...catalog,
  };
}
