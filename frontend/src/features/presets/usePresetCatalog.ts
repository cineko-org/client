import { useCallback, useEffect, useRef, useState } from 'react';
import { api, errorMessage, watchSeatMap } from '../../api/client';
import {
	AuditoriumResponseSchema, type Resolution, type Auditorium, type Preset,
	type Snapshot, type WebUIState,
} from '../../api/proto';
import type { Notify } from '../../components/core/feedback';

interface ActiveCatalogLoad {
  current: AbortController | null;
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
  const [pickedSeats, setPickedSeats] = useState<string[]>([]);
  const [catalogMessage, setCatalogMessage] = useState('');
  const [loadingCatalog, setLoadingCatalog] = useState(false);
  const activeRequest = useRef<AbortController | null>(null);
	const activeSeatMapWatch = useRef<(() => void) | null>(null);

	const stopSeatMapWatch = useCallback(() => {
		const stop = activeSeatMapWatch.current;
		activeSeatMapWatch.current = null;
		stop?.();
	}, []);

  const cancelRequest = useCallback(() => {
    const request = activeRequest.current;
    activeRequest.current = null;
    request?.abort();
		stopSeatMapWatch();
    setLoadingCatalog(false);
  }, [stopSeatMapWatch]);

  const beginRequest = useCallback(() => {
    activeRequest.current?.abort();
		stopSeatMapWatch();
    const request = new AbortController();
    activeRequest.current = request;
    setLoadingCatalog(false);
    return request;
  }, [stopSeatMapWatch]);

  useEffect(() => () => {
		activeRequest.current?.abort();
		activeSeatMapWatch.current?.();
	}, []);

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
    await new Promise<void>((resolve) => {
		let firstEvent = true;
		let stop: (() => void) | null = null;
		const ownsWatch = () => stop !== null && activeSeatMapWatch.current === stop && !request.signal.aborted;
		const finishInitialEvent = () => {
			if (!firstEvent) return;
			firstEvent = false;
			finishCatalogRequest(activeRequest, request, setLoadingCatalog);
			resolve();
		};
		const applyResolution = (response: Resolution) => {
			if (!ownsWatch()) return;
			const stateCase = response.state?.state.case;
			if (!stateCase || (stateCase === 'idle' && !response.snapshot)) {
				throw new Error('Cineko가 올바르지 않은 좌석 배치 상태를 보냈습니다.');
			}
			const snapshot = response.snapshot;
      setSeatMap(snapshot ?? null);
      setPickedSeats((current) => snapshot
        ? current.filter((label) => snapshot.layout?.seats.some((seat) => seat.label === label))
        : []);
			switch (stateCase) {
        case 'idle':
          setCatalogMessage('저장된 좌석 배치를 불러왔습니다.');
          break;
        case 'queued':
          setCatalogMessage('좌석 배치 수집을 기다리고 있습니다.');
          break;
        case 'collecting':
          setCatalogMessage('Central에서 좌석 배치를 수집하고 있습니다.');
          break;
        case 'waitingForShowtime':
          setCatalogMessage('좌석을 확인할 수 있는 상영 회차를 기다리고 있습니다.');
          break;
        case 'retryScheduled':
          setCatalogMessage('좌석 배치 수집을 다시 시도할 예정입니다.');
          break;
        case 'blocked':
          setCatalogMessage('좌석 배치를 준비하지 못했습니다. 관리자 확인이 필요합니다.');
          break;
        default:
				throw new Error('Cineko가 올바르지 않은 좌석 배치 상태를 보냈습니다.');
      }
			finishInitialEvent();
		};
		try {
			stop = watchSeatMap(
				id,
				(response) => {
					if (!response.resolution) {
						throw new Error('Cineko가 올바르지 않은 좌석 배치 상태를 보냈습니다.');
					}
					applyResolution(response.resolution);
				},
				(error) => {
					if (!ownsWatch()) return;
					activeSeatMapWatch.current = null;
					stop?.();
					setCatalogMessage(errorMessage(error));
					finishInitialEvent();
				},
			);
			activeSeatMapWatch.current = stop;
		} catch (error) {
			if (!request.signal.aborted) setCatalogMessage(errorMessage(error));
			finishInitialEvent();
		}
		request.signal.addEventListener('abort', () => {
			if (stop !== null && activeSeatMapWatch.current === stop) {
				activeSeatMapWatch.current = null;
				stop();
			}
			resolve();
		}, { once: true });
	});
  }, [beginRequest]);

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
