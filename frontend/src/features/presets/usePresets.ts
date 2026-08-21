import { useCallback, useState } from 'react';
import { create } from '@bufbuild/protobuf';
import { api, errorMessage, isRevisionConflict } from '../../api/client';
import {
	MutationIdentitySchema, PresetResourceSchema, ResourceKindSchema, ResourceSchema, WebUIActionStatusSchema,
	WebUIResourceDeletionSchema, WebUIResourceMutationSchema, type WebUIState,
} from '../../api/proto';
import { presetResources, resourceRevision, statePresets } from '../../api/resources';
import type { Notify } from '../../components/core/feedback';
import { candidateSelectionError, formFromPreset, initialPresetForm, presetSaveRequest, type PresetForm } from './model';
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
    if (!activeTheaterId || !auditoriumId) {
      notify('지점과 상영관을 먼저 선택하세요.', { tone: 'error' });
      return;
    }
    if (!form.name.trim()) {
      notify('프리셋 이름을 입력하세요.', { tone: 'error' });
      return;
    }
	const seats = seatMap?.layout?.seats ?? [];
	if (!seatMap || seats.length === 0) {
      notify('좌석 배치 분석이 끝난 뒤 프리셋을 저장할 수 있습니다.', { tone: 'error' });
      return;
    }
	const selectionError = candidateSelectionError(
			seats, pickedSeats, form.seatCount,
    );
    if (selectionError) {
      notify(selectionError, { tone: 'error' });
      return;
		}
		setSaving(true);
		try {
			const mutation = presetSaveRequest(form, userId, activeTheaterId, auditoriumId, pickedSeats);
			await api('/api/presets', ResourceSchema, {
				method: form.id ? 'PUT' : 'POST',
			}, WebUIResourceMutationSchema, mutation);
      notify(form.id ? '프리셋을 수정했습니다.' : '프리셋을 저장했습니다.', { important: true });
      reset();
      await reload();
      onSaved();
	} catch (error) {
		if (isRevisionConflict(error)) {
			await reload();
			notify('다른 기기에서 이 프리셋을 변경했습니다. 최신 내용을 불러왔습니다.', { tone: 'warning', important: true });
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
		await api('/api/presets', WebUIActionStatusSchema, { method: 'DELETE' }, WebUIResourceDeletionSchema,
				create(WebUIResourceDeletionSchema, {
					mutation: create(MutationIdentitySchema, { expectedRevision: BigInt(resourceRevision(resource)) }),
					userId, id: deleteId, kind: create(ResourceKindSchema, { kind: { case: 'preset', value: create(PresetResourceSchema) } }),
				}));
      await reload();
      notify('프리셋을 삭제했습니다.');
	} catch (error) {
		if (isRevisionConflict(error)) {
			await reload();
			notify('다른 기기에서 이 프리셋을 변경했습니다. 최신 내용을 불러왔습니다.', { tone: 'warning', important: true });
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
