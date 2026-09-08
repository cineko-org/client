import { create } from '@bufbuild/protobuf';
import { useCallback, useEffect, useReducer, useRef } from 'react';
import { api, errorMessage, watchSeatMap } from '../../api/client';
import { AuditoriumResponseSchema, ResolutionSchema, SeatMapRequestSchema, type Preset, type Resolution, type WebUIState } from '../../api/proto';
import type { Notify } from '../../components/core/feedback';
import { catalogPresentation, catalogReducer, emptyCatalogState } from './catalogState';

export const seatMapInitialEventTimeoutMs = 10_000;
export const auditoriumDiscoveryPollMs = 1_000;
export const auditoriumDiscoveryTimeoutMs = 90_000;

function waitForDiscovery(signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const aborted = () => { window.clearTimeout(timer); reject(new DOMException('aborted', 'AbortError')); };
    const timer = window.setTimeout(() => { signal.removeEventListener('abort', aborted); resolve(); }, auditoriumDiscoveryPollMs);
    signal.addEventListener('abort', aborted, { once: true });
    if (signal.aborted) aborted();
  });
}

export function usePresetCatalog(state: WebUIState, notify: Notify) {
  const [catalog, dispatch] = useReducer(catalogReducer, emptyCatalogState);
  const activeRequest = useRef<AbortController | null>(null);
  const activeWatch = useRef<(() => void) | null>(null);
  const cancel = useCallback(() => {
    activeRequest.current?.abort(); activeRequest.current = null;
    activeWatch.current?.(); activeWatch.current = null;
  }, []);
  const begin = useCallback(() => {
    cancel();
    const request = new AbortController();
    activeRequest.current = request;
    return request;
  }, [cancel]);
  useEffect(() => cancel, [cancel]);

  const reset = useCallback(() => { cancel(); dispatch({ type: 'reset' }); }, [cancel]);
  const setRegion = useCallback((region: string) => { cancel(); dispatch({ type: 'reset', region }); }, [cancel]);
  const setTheater = useCallback(async (name: string) => {
    const request = begin();
    const theater = state.catalog?.theaters.find((item) => item.region === catalog.region && item.name === name);
    dispatch({ type: 'reset', region: catalog.region, theaterId: theater?.id, phase: theater ? 'discovering' : 'idle' });
    if (!theater) return;
    try {
      const deadline = Date.now() + auditoriumDiscoveryTimeoutMs;
      for (;;) {
        // oxlint-disable-next-line no-await-in-loop -- Sequential local discovery, not concurrent provider requests.
        const response = await api(`/api/auditoriums?theaterId=${encodeURIComponent(theater.id)}`, AuditoriumResponseSchema, { signal: request.signal });
        if (request.signal.aborted) return;
        if (response.auditoriums.length || Date.now() >= deadline) {
          dispatch({ type: 'auditoriums', values: response.auditoriums });
          return;
        }
        // oxlint-disable-next-line no-await-in-loop -- Wait before the next local discovery read.
        await waitForDiscovery(request.signal);
      }
    } catch (error) {
      if (!request.signal.aborted) {
        dispatch({ type: 'resolution', phase: 'error', error: errorMessage(error) });
        notify(errorMessage(error), { tone: 'error' });
      }
    }
  }, [begin, catalog.region, notify, state.catalog?.theaters]);

  const loadSeatMap = useCallback(async (id: string, existingRequest?: AbortController) => {
    const request = existingRequest ?? begin();
    dispatch({ type: 'auditorium', id });
    if (!id) return;
    const apply = (response: Resolution) => {
      const phase = response.state?.state.case;
      const snapshot = response.snapshot;
      if (snapshot && snapshot.auditoriumId !== id) throw new Error('다른 상영관의 좌석 배치를 받았습니다.');
      if (snapshot) { dispatch({ type: 'resolution', phase: 'cached', snapshot }); return; }
      switch (phase) {
        case 'queued': case 'collecting': case 'waitingForShowtime': case 'retryScheduled':
          dispatch({ type: 'resolution', phase }); return;
        case 'blocked': dispatch({ type: 'resolution', phase: 'error', error: '좌석 배치를 준비하지 못했습니다. 관제 로그를 확인하세요.' }); return;
        default: throw new Error('Cineko가 올바르지 않은 좌석 배치 상태를 보냈습니다.');
      }
    };
    try {
      const initial = await api('/api/catalog/seat-map', ResolutionSchema, { method: 'POST', signal: request.signal }, SeatMapRequestSchema, create(SeatMapRequestSchema, { auditoriumId: id }));
      if (request.signal.aborted) return;
      if (initial.snapshot) { apply(initial); return; }
    } catch (error) {
      if (!request.signal.aborted) dispatch({ type: 'resolution', phase: 'error', error: errorMessage(error) });
      return;
    }
    await new Promise<void>((resolve) => {
      let first = true;
      let stop: (() => void) | null = null;
      let timer: number | undefined;
      const owns = () => stop !== null && activeWatch.current === stop && !request.signal.aborted;
      const finish = () => { first = false; window.clearTimeout(timer); resolve(); };
      const fail = (error: unknown) => {
        if (!owns()) return;
        stop?.(); activeWatch.current = null;
        dispatch({ type: 'resolution', phase: 'error', error: errorMessage(error) });
        finish();
      };
      try {
        stop = watchSeatMap(id, (response) => {
          if (!owns()) return;
          try {
            if (!response.resolution) throw new Error('Cineko가 올바르지 않은 좌석 배치 상태를 보냈습니다.');
            apply(response.resolution);
            finish();
          } catch (error) { fail(error); }
        }, fail);
        activeWatch.current = stop;
      } catch (error) {
        dispatch({ type: 'resolution', phase: 'error', error: errorMessage(error) });
        finish();
      }
      if (first) timer = window.setTimeout(() => fail(new Error('좌석 배치 상태를 받지 못했습니다. 다시 시도하세요.')), seatMapInitialEventTimeoutMs);
      request.signal.addEventListener('abort', () => { if (activeWatch.current === stop) { stop?.(); activeWatch.current = null; } finish(); }, { once: true });
      if (request.signal.aborted) finish();
    });
  }, [begin]);

  const loadPreset = useCallback((preset: Preset) => {
    const request = begin();
    const theater = state.catalog?.theaters.find((item) => item.id === preset.theaterId);
    dispatch({ type: 'reset', region: theater?.region, theaterId: preset.theaterId, auditoriumId: preset.auditoriumId, seats: [...(preset.seatPreference?.explicitSeats ?? [])], phase: 'discovering' });
    void (async () => {
      try {
        const response = await api(`/api/auditoriums?theaterId=${encodeURIComponent(preset.theaterId)}`, AuditoriumResponseSchema, { signal: request.signal });
        if (request.signal.aborted) return;
        dispatch({ type: 'auditoriums', values: response.auditoriums });
        await loadSeatMap(preset.auditoriumId, request);
      } catch (error) {
        if (!request.signal.aborted) {
          dispatch({ type: 'resolution', phase: 'error', error: errorMessage(error) });
          notify(errorMessage(error), { tone: 'error' });
        }
      }
    })();
  }, [begin, loadSeatMap, notify, state.catalog?.theaters]);
  const toggleSeat = useCallback((label: string) => dispatch({ type: 'toggle', label }), []);
  const clearSeats = useCallback(() => dispatch({ type: 'clearSeats' }), []);
  return { region: catalog.region, setRegion,
    theater: state.catalog?.theaters.find((item) => item.id === catalog.theaterId)?.name ?? '', setTheater,
    activeTheaterId: catalog.theaterId, auditoriumId: catalog.auditoriumId, setAuditorium: loadSeatMap,
    auditoriums: catalog.auditoriums, seatMap: catalog.result.snapshot, pickedSeats: catalog.pickedSeats,
    toggleSeat, clearSeats, ...catalogPresentation(catalog), reset, loadPreset };
}
