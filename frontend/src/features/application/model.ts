import type { AppState, ApplicationConnection } from '../../api/types';

export const initialApplicationConnection: ApplicationConnection = {
  status: 'loading',
  message: '',
  lastSuccessfulAt: '',
  retrying: false,
};

export const emptyAppState: AppState = {
  userId: 'local-user',
  catalog: { generation: 0, providers: [], theaters: [], movies: [], auditoriums: [] },
  presets: [],
  monitors: [],
  reservations: [],
};
