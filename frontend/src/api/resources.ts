import type { AppEvent, Monitor, Preset, Reservation, Resource, WebUIAccountState, WebUIState } from './proto';

export function statePresets(state: WebUIState): Preset[] {
	return state.resources.flatMap((resource) => resource.resource.case === 'preset' ? [resource.resource.value] : []);
}

export function presetResources(state: WebUIState): Resource[] {
	return state.resources.filter((resource) => resource.resource.case === 'preset');
}

export function stateMonitors(state: WebUIState): Monitor[] {
	return state.resources.flatMap((resource) => resource.resource.case === 'monitor' ? [resource.resource.value] : []);
}

export function monitorResources(state: WebUIState): Resource[] {
	return state.resources.filter((resource) => resource.resource.case === 'monitor');
}

export function stateReservations(state: WebUIState): Reservation[] {
	return state.resources.flatMap((resource) => resource.resource.case === 'reservation' ? [resource.resource.value] : []);
}

export function stateEvents(state: WebUIState): AppEvent[] {
	return state.resources.flatMap((resource) => resource.resource.case === 'appEvent' ? [resource.resource.value] : []);
}

export function resourceRevision(resource: Resource | undefined): number {
	return Number(resource?.identity?.revision ?? 0);
}

export function monitorStatus(monitor: Monitor): string {
	return monitor.state?.state.case ?? 'pending';
}

export function monitorMovie(monitor: Monitor): string {
	return monitor.movieTitle;
}

export function localDateText(value: { year: number; month: number; day: number }): string {
	return `${value.year.toString().padStart(4, '0')}-${value.month.toString().padStart(2, '0')}-${value.day.toString().padStart(2, '0')}`;
}

export function localTimeText(value: { hour: number; minute: number } | undefined): string {
	return value ? `${value.hour.toString().padStart(2, '0')}:${value.minute.toString().padStart(2, '0')}` : '';
}

export function eventTone(event: AppEvent): 'info' | 'success' | 'warning' | 'error' {
	return event.tone.case ?? 'info';
}

export function reservationStatus(reservation: Reservation): string {
	return reservation.state.case ?? 'prepared';
}

export function accountStatus(account: WebUIAccountState): string {
	return account.state.case ?? 'checking';
}

export function accountAuthenticated(account: WebUIAccountState): boolean {
	return account.state.case === 'authenticated';
}
