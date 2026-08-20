export type MonitorMode = 'opening' | 'cancellation';
export type MonitorStatus = 'pending' | 'running' | 'triggered' | 'payment_unknown' | 'booked' | 'failed' | 'stopped';
export type SeatType =
  | 'standard' | 'wheelchair' | 'companion' | 'couple' | 'recliner'
  | 'motion' | 'prime' | 'premium' | 'bed' | 'unknown';
export type SeatAdjacency = 'required';

export interface Theater {
  id: string;
  providerId: string;
  sourceKey: string;
  region: string;
  name: string;
}

export interface Auditorium {
  id: string;
  theaterId: string;
  sourceKey: string;
  name: string;
  capacity: number;
  seatMapVersion: string;
}

export interface Seat {
  id: string;
  label: string;
  row: string;
  number: number;
  x: number;
  y: number;
  type: SeatType;
  zoneName: string;
  saleFormName: string;
  leftAisle: boolean;
  rightAisle: boolean;
}

export interface SeatMap {
  auditoriumId: string;
  version: string;
  seats: Seat[];
  zones: unknown[];
}

export interface SeatPreference {
  candidateSeats: string[];
  preferredRows: string[];
  preferredZones: unknown[];
  preferredTypes: SeatType[];
  adjacency: SeatAdjacency;
  avoidEdges: boolean;
}

export interface Preset {
	revision?: number;
	id: string;
  userId: string;
  name: string;
  theaterId: string;
  auditoriumId: string;
  seatCount: number;
  seatPreference: SeatPreference;
}

export interface Monitor {
	revision?: number;
	id: string;
  userId: string;
  presetId: string;
  mode: MonitorMode;
  movieId: string;
  movie: string;
  targetDates: string[];
  targetWeekdays: number[];
  searchHorizonDays: number;
  earliestTime: string;
  latestTime: string;
  pollInterval: number;
  pollIntervalMax: number;
  status: MonitorStatus;
  lastError: string;
  lastCheckedAt?: string;
  updatedAt: string;
}

export interface Reservation {
  id: string;
  bookingNumber: string;
  status: string;
  draft?: {
    showtime?: { movie?: string };
    seatLabels?: string[];
  };
}

export interface CatalogMovie {
  id: string;
  providerId: string;
  sourceKey: string;
  title: string;
  posterUrl?: string;
}

export interface CatalogProvider {
  id: string;
  name: string;
}

export interface CatalogIndex {
  generation: number;
  providers: CatalogProvider[];
  theaters: Theater[];
  movies: CatalogMovie[];
  auditoriums: Auditorium[];
}

export interface AppState {
  userId: string;
  catalog: CatalogIndex;
  presets: Preset[];
  monitors: Monitor[];
  reservations: Reservation[];
}

export type ApplicationConnectionStatus = 'loading' | 'ready' | 'stale' | 'unavailable';

export interface ApplicationConnection {
  status: ApplicationConnectionStatus;
  message: string;
  lastSuccessfulAt: string;
  retrying: boolean;
}

export interface TaskState {
  status: 'running' | 'completed' | 'failed' | 'stopped';
  message?: string;
  updatedAt: string;
}

export interface AppEvent {
  id: string;
  userId: string;
  kind: string;
  tone: 'info' | 'success' | 'warning' | 'error';
  message: string;
  createdAt: string;
  readAt?: string;
}

export interface AccountState {
  status: 'checking' | 'authenticated' | 'unauthenticated' | 'error';
  authenticated: boolean;
  credentialsSaved?: boolean;
  accountId?: string;
  message?: string;
}

export interface NetworkSettings {
  mode: 'direct' | 'proxy';
  proxyUrls?: string[];
  proxyUsername?: string;
  hasProxyPassword?: boolean;
  source?: string;
}

export interface NetworkSettingsInput {
  mode: 'direct' | 'proxy';
  proxyUrls: string[];
  proxyUsername: string;
  proxyPassword: string;
}

export interface HookTargetSettings {
  id: string;
  name: string;
  kind: 'discord' | 'slack' | 'webhook';
  url: string;
  eventKinds: string[];
  enabled: boolean;
  hasSecret?: boolean;
}

export interface HookTargetInput extends Omit<HookTargetSettings, 'hasSecret'> {
  secret: string;
}

export interface HookSettings {
  targets: HookTargetSettings[];
}

export interface HookSettingsInput {
  targets: HookTargetInput[];
}

export interface DesktopBridge {
  GetNetworkSettings(): Promise<NetworkSettings>;
  SaveNetworkSettings(input: NetworkSettingsInput): Promise<NetworkSettings>;
  GetHookSettings(): Promise<HookSettings>;
  SaveHookSettings(input: HookSettingsInput): Promise<HookSettings>;
  GetUserID(): Promise<string>;
  Exit(): Promise<void>;
}

declare global {
  interface Window {
    go?: { main?: { DesktopApp?: DesktopBridge } };
    runtime?: { EventsOn?: (name: string, callback: (...args: unknown[]) => void) => (() => void) };
    __cinekoAppBooted?: boolean;
  }
}
