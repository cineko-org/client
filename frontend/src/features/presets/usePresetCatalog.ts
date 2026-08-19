import { useCallback, useEffect, useRef, useState } from 'react';
import { api, errorMessage } from '../../api/client';
import type { AppState, Auditorium, Preset, SeatMap } from '../../api/types';
import type { Notify } from '../../components/core/feedback';
import { csv } from './model';

export function usePresetCatalog(state: AppState, reload: () => Promise<AppState>, notify: Notify) {
  const [region, setRegionValue] = useState('');
  const [theater, setTheaterValue] = useState('');
  const [activeTheaterId, setActiveTheaterId] = useState('');
  const [auditoriumId, setAuditoriumIdValue] = useState('');
  const [auditoriums, setAuditoriums] = useState<Auditorium[]>([]);
  const [seatMap, setSeatMap] = useState<SeatMap | null>(null);
  const [pickedSeats, setPickedSeats] = useState<string[]>([]);
  const [catalogDates, setCatalogDates] = useState('');
  const [catalogMessage, setCatalogMessage] = useState('');
  const [loadingCatalog, setLoadingCatalog] = useState(false);
  const [forceCapture, setForceCapture] = useState(false);
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

  const finishRequest = useCallback((request: AbortController) => {
    if (activeRequest.current !== request) return;
    activeRequest.current = null;
    setLoadingCatalog(false);
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
    setCatalogDates('');
    setCatalogMessage('');
    setForceCapture(false);
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
    const selectedTheater = state.catalog.theaters.find((item) => item.region === region && item.name === value);
    setActiveTheaterId(selectedTheater?.id || '');
    if (!selectedTheater) {
      setAuditoriums([]);
      setCatalogMessage(value ? '상영관 목록이 없습니다. 가져오기를 실행하세요.' : '');
      finishRequest(request);
      return;
    }
    setLoadingCatalog(true);
    try {
      const values = await api<Auditorium[]>(`/api/auditoriums?theaterId=${encodeURIComponent(selectedTheater.id)}`, {
        signal: request.signal,
      });
      if (request.signal.aborted) return;
      setAuditoriums(values);
      setCatalogMessage(values.length > 0
        ? `저장된 상영관 ${values.length}개를 불러왔습니다.`
        : '상영관 목록이 없습니다. 가져오기를 실행하세요.');
    } catch (error) {
      if (!request.signal.aborted) notify(errorMessage(error), { tone: 'error' });
    } finally {
      finishRequest(request);
    }
  }, [beginRequest, finishRequest, notify, region, state.catalog.theaters]);

  const loadSeatMap = useCallback(async (
    id: string,
    values = auditoriums,
    existingRequest?: AbortController,
  ) => {
    const request = existingRequest ?? beginRequest();
    setAuditoriumIdValue(id);
    setSeatMap(null);
    if (!id) {
      finishRequest(request);
      return;
    }
    const auditorium = values.find((item) => item.id === id);
    if (!auditorium?.seatMapVersion) {
      setPickedSeats([]);
      setCatalogMessage('좌석을 미리 고르지 않아도 저장할 수 있습니다. 예매 시 CGV의 최신 좌석에서 자동으로 찾습니다.');
      finishRequest(request);
      return;
    }
    setLoadingCatalog(true);
    try {
      const value = await api<SeatMap>(`/api/seat-map?auditoriumId=${encodeURIComponent(id)}`, {
        signal: request.signal,
      });
      if (request.signal.aborted) return;
      setSeatMap(value);
      setPickedSeats((current) => current.filter((label) => value.seats.some((seat) => seat.label === label)));
      setCatalogMessage('저장된 좌석 배치를 불러왔습니다.');
    } catch (error) {
      if (!request.signal.aborted) notify(errorMessage(error), { tone: 'error' });
    } finally {
      finishRequest(request);
    }
  }, [auditoriums, beginRequest, finishRequest, notify]);

  const discoverAuditoriums = useCallback(async () => {
    if (!region || !theater) {
      notify('지역과 지점을 먼저 선택하세요.', { tone: 'error' });
      return;
    }
    const request = beginRequest();
    setLoadingCatalog(true);
    setCatalogMessage('CGV에서 이 지점의 상영관 목록을 확인합니다.');
    try {
      const result = await api<{ theater: { id: string }; auditoriums: Auditorium[] }>('/api/catalog/auditoriums', {
        method: 'POST',
        signal: request.signal,
        body: { region, theater, dates: csv(catalogDates), headful: false, force: false },
      });
      if (request.signal.aborted) return;
      setActiveTheaterId(result.theater.id);
      setAuditoriums(result.auditoriums || []);
      await reload();
      if (request.signal.aborted) return;
      setCatalogMessage(result.auditoriums.length > 0
        ? `상영관 ${result.auditoriums.length}개를 불러왔습니다.`
        : '조회 기간에 예매 가능한 상영관을 찾지 못했습니다.');
      notify(`${result.auditoriums.length}개 상영관을 선택할 수 있습니다.`, { important: true });
    } catch (error) {
      if (!request.signal.aborted) {
        setCatalogMessage(errorMessage(error));
        notify(errorMessage(error), { tone: 'error', important: true });
      }
    } finally {
      finishRequest(request);
    }
  }, [beginRequest, catalogDates, finishRequest, notify, region, reload, theater]);

  const captureSeatMap = useCallback(async (force = false) => {
    setForceCapture(false);
    if (!region || !theater || !auditoriumId) {
      notify('상영관을 먼저 선택하세요.', { tone: 'error' });
      return;
    }
    const request = beginRequest();
    setLoadingCatalog(true);
    setCatalogMessage('CGV에서 현재 좌석 배치를 확인합니다. 로그인은 필요하지 않습니다.');
    try {
      const response = await api<SeatMap | { status: 'waiting'; auditoriumId: string }>('/api/catalog/seat-map', {
        method: 'POST',
        signal: request.signal,
        body: { region, theater, auditoriumId, dates: csv(catalogDates), headful: false, force },
      });
      if (request.signal.aborted) return;
      if (!('seats' in response)) {
        setSeatMap(null);
        setCatalogMessage('좌석 배치를 확인할 수 있는 회차를 기다리고 있습니다. 프리셋 저장과 예매 감시는 계속할 수 있습니다.');
        notify('좌석 배치 확인을 요청했습니다.', { important: true });
        return;
      }
      const value = response;
      const previousVersion = seatMap?.version;
      setSeatMap(value);
      const nextAuditoriums = await api<Auditorium[]>(`/api/auditoriums?theaterId=${encodeURIComponent(activeTheaterId)}`, {
        signal: request.signal,
      });
      if (request.signal.aborted) return;
      setAuditoriums(nextAuditoriums);
      setPickedSeats((current) => current.filter((label) => value.seats.some((seat) => seat.label === label)));
      await reload();
      if (request.signal.aborted) return;
      const changed = Boolean(previousVersion && previousVersion !== value.version);
      setCatalogMessage(changed
        ? 'CGV 좌석 배치 변경을 확인해 최신 값으로 업데이트했습니다.'
        : previousVersion ? '저장된 좌석 배치가 CGV 최신 값과 같습니다.' : 'CGV 좌석 배치를 저장했습니다.');
      notify(changed ? '좌석 배치 변경을 반영했습니다.' : `${value.seats.length}석 좌석 배치를 확인했습니다.`, { important: true });
    } catch (error) {
      if (!request.signal.aborted) {
        setCatalogMessage(errorMessage(error));
        notify(errorMessage(error), { tone: 'error', important: true });
      }
    } finally {
      finishRequest(request);
    }
  }, [activeTheaterId, auditoriumId, beginRequest, catalogDates, finishRequest, notify, region, reload, seatMap?.version, theater]);

  const loadPreset = useCallback((preset: Preset) => {
    const request = beginRequest();
    setLoadingCatalog(true);
    setPickedSeats([...preset.seatPreference.candidateSeats]);
    setActiveTheaterId(preset.theaterId);
    setAuditoriumIdValue(preset.auditoriumId);
    const selectedTheater = state.catalog.theaters.find((item) => item.id === preset.theaterId);
    setRegionValue(selectedTheater?.region || '');
    setTheaterValue(selectedTheater?.name || '');
    void (async () => {
      try {
        const values = await api<Auditorium[]>(`/api/auditoriums?theaterId=${encodeURIComponent(preset.theaterId)}`, {
          signal: request.signal,
        });
        if (request.signal.aborted) return;
        setAuditoriums(values);
        await loadSeatMap(preset.auditoriumId, values, request);
      } catch (error) {
        if (!request.signal.aborted) notify(errorMessage(error), { tone: 'error' });
      } finally {
        finishRequest(request);
      }
    })();
  }, [beginRequest, finishRequest, loadSeatMap, notify, state.catalog.theaters]);

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
    catalogDates, setCatalogDates, catalogMessage, loadingCatalog,
    discoverAuditoriums, captureSeatMap, forceCapture, setForceCapture,
    reset, loadPreset,
  };
}
