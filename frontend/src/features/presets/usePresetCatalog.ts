import { useCallback, useEffect, useRef, useState } from 'react';
import { create } from '@bufbuild/protobuf';
import { api, errorMessage } from '../../api/client';
import {
	AuditoriumResponseSchema, ResolutionSchema, SeatMapRequestSchema, type Auditorium, type Preset,
	type Snapshot, type WebUIState,
} from '../../api/proto';
import type { Notify } from '../../components/core/feedback';

interface ActiveCatalogLoad {
  current: AbortController | null;
}

const seatMapResolutionRetryDelaysMs = [2_000, 3_000, 5_000, 8_000, 10_000, 10_000, 10_000, 10_000];

/** Waits for Central without leaving a timer behind when the selection changes. */
function waitForSeatMapRetry(signal: AbortSignal, delayMs: number): Promise<void> {
  return new Promise((resolve) => {
    const timer = window.setTimeout(resolve, delayMs);
    signal.addEventListener('abort', () => {
      window.clearTimeout(timer);
      resolve();
    }, { once: true });
  });
}

/** Completes only the catalog request that still owns the loading state. */
function finishCatalogRequest(
  activeRequest: ActiveCatalogLoad,
  request: AbortController,
  setLoading: (loading: boolean) => void,
) {
  if (activeRequest.current !== request) return;
  activeRequest.current = null;
  setLoading(false);
}

export function usePresetCatalog(state: WebUIState, notify: Notify) {
  const [region, setRegionValue] = useState('');
  const [theater, setTheaterValue] = useState('');
  const [activeTheaterId, setActiveTheaterId] = useState('');
  const [auditoriumId, setAuditoriumIdValue] = useState('');
  const [auditoriums, setAuditoriums] = useState<Auditorium[]>([]);
	const [seatMap, setSeatMap] = useState<Snapshot | null>(null);
	const seatMapVersion = seatMap?.layoutHash;
  const [pickedSeats, setPickedSeats] = useState<string[]>([]);
  const [catalogMessage, setCatalogMessage] = useState('');
  const [loadingCatalog, setLoadingCatalog] = useState(false);
  const activeRequest = useRef<AbortController | null>(null);

  const cancelRequest = useCallback(() => {
    const request = activeRequest.current;
    activeRequest.current = null;
    request?.abort();
    setLoadingCatalog(false);
  }, []);

  const beginRequest = useCallback(() => {
    activeRequest.current?.abort();
    const request = new AbortController();
    activeRequest.current = request;
    setLoadingCatalog(false);
    return request;
  }, []);

  useEffect(() => () => activeRequest.current?.abort(), []);

  const reset = useCallback(() => {
    cancelRequest();
    setRegionValue('');
    setTheaterValue('');
    setActiveTheaterId('');
    setAuditoriumIdValue('');
    setAuditoriums([]);
    setSeatMap(null);
    setPickedSeats([]);
    setCatalogMessage('');
  }, [cancelRequest]);

  const setRegion = useCallback((value: string) => {
    cancelRequest();
    setRegionValue(value);
    setTheaterValue('');
    setActiveTheaterId('');
    setAuditoriumIdValue('');
    setAuditoriums([]);
    setSeatMap(null);
    setPickedSeats([]);
    setCatalogMessage('');
  }, [cancelRequest]);

  const setTheater = useCallback(async (value: string) => {
    const request = beginRequest();
    setTheaterValue(value);
    setAuditoriumIdValue('');
    setSeatMap(null);
    setPickedSeats([]);
	const selectedTheater = state.catalog?.theaters.find((item) => item.region === region && item.name === value);
    setActiveTheaterId(selectedTheater?.id || '');
    if (!selectedTheater) {
      setAuditoriums([]);
      setCatalogMessage(value ? 'Central에 저장된 상영관이 없습니다.' : '');
      finishCatalogRequest(activeRequest, request, setLoadingCatalog);
      return;
    }
    setLoadingCatalog(true);
    try {
		const response = await api(`/api/auditoriums?theaterId=${encodeURIComponent(selectedTheater.id)}`, AuditoriumResponseSchema, {
			signal: request.signal,
		});
		if (request.signal.aborted) return;
		const values = response.auditoriums;
      setAuditoriums(values);
      setCatalogMessage(values.length > 0
        ? `저장된 상영관 ${values.length}개를 불러왔습니다.`
        : 'Central에 저장된 상영관이 없습니다. 관측이 완료되면 자동으로 표시됩니다.');
    } catch (error) {
      if (!request.signal.aborted) notify(errorMessage(error), { tone: 'error' });
    } finally {
      finishCatalogRequest(activeRequest, request, setLoadingCatalog);
    }
  }, [beginRequest, notify, region, state.catalog?.theaters]);

  const loadSeatMap = useCallback(async (
    id: string,
    existingRequest?: AbortController,
  ) => {
    const request = existingRequest ?? beginRequest();
    setAuditoriumIdValue(id);
    setSeatMap(null);
    if (!id) {
      finishCatalogRequest(activeRequest, request, setLoadingCatalog);
      return;
    }
    setLoadingCatalog(true);
    setCatalogMessage('Central에 저장된 좌석 배치를 확인합니다.');
    try {
      const resolve = async (attempt: number): Promise<void> => {
		const response = await api('/api/catalog/seat-map', ResolutionSchema, {
			method: 'POST',
			signal: request.signal,
		}, SeatMapRequestSchema, create(SeatMapRequestSchema, { auditoriumId: id }));
        if (request.signal.aborted) return;
		if (response.result.case === 'ready' && response.result.value.snapshot) {
			const snapshot = response.result.value.snapshot;
			setSeatMap(snapshot);
			setPickedSeats((current) => current.filter((label) => snapshot.layout?.seats.some((seat) => seat.label === label)));
          setCatalogMessage('저장된 좌석 배치를 불러왔습니다.');
          return;
        }
        setPickedSeats([]);
        if (attempt >= seatMapResolutionRetryDelaysMs.length) {
          setCatalogMessage('좌석 배치를 아직 준비하지 못했습니다. 잠시 후 다시 선택해 주세요.');
          return;
        }
        setCatalogMessage('Central에서 좌석 배치를 준비 중입니다.');
        await waitForSeatMapRetry(request.signal, seatMapResolutionRetryDelaysMs[attempt]);
        if (request.signal.aborted) return;
        await resolve(attempt + 1);
      };
      await resolve(0);
    } catch (error) {
      if (!request.signal.aborted) notify(errorMessage(error), { tone: 'error' });
    } finally {
      finishCatalogRequest(activeRequest, request, setLoadingCatalog);
    }
  }, [beginRequest, notify]);

	const catalogSeatMapVersion = state.catalog?.auditoriums.find((item) => item.id === auditoriumId)?.currentLayoutHash;
  useEffect(() => {
    if (!auditoriumId || !catalogSeatMapVersion || seatMapVersion === catalogSeatMapVersion) return;
    queueMicrotask(() => void loadSeatMap(auditoriumId));
  }, [auditoriumId, catalogSeatMapVersion, loadSeatMap, seatMapVersion]);

  const loadPreset = useCallback((preset: Preset) => {
    const request = beginRequest();
    setLoadingCatalog(true);
	setPickedSeats([...(preset.seatPreference?.explicitSeats ?? [])]);
    setActiveTheaterId(preset.theaterId);
    setAuditoriumIdValue(preset.auditoriumId);
	const selectedTheater = state.catalog?.theaters.find((item) => item.id === preset.theaterId);
    setRegionValue(selectedTheater?.region || '');
    setTheaterValue(selectedTheater?.name || '');
    void (async () => {
      try {
		const response = await api(`/api/auditoriums?theaterId=${encodeURIComponent(preset.theaterId)}`, AuditoriumResponseSchema, {
			signal: request.signal,
		});
		if (request.signal.aborted) return;
		const values = response.auditoriums;
        setAuditoriums(values);
        await loadSeatMap(preset.auditoriumId, request);
      } catch (error) {
        if (!request.signal.aborted) notify(errorMessage(error), { tone: 'error' });
      } finally {
        finishCatalogRequest(activeRequest, request, setLoadingCatalog);
      }
    })();
  }, [beginRequest, loadSeatMap, notify, state.catalog?.theaters]);

  const toggleSeat = useCallback((label: string) => {
    setPickedSeats((current) => current.includes(label)
      ? current.filter((value) => value !== label)
      : [...current, label]);
  }, []);
  const clearSeats = useCallback(() => setPickedSeats([]), []);

  return {
    region, setRegion, theater, setTheater, activeTheaterId,
    auditoriumId, setAuditorium: loadSeatMap, auditoriums, seatMap,
    pickedSeats, toggleSeat, clearSeats,
    catalogMessage, loadingCatalog,
    reset, loadPreset,
  };
}
