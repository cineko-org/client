import type { AppState, ApplicationConnection, TransferReport } from '../../api/types';

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

export function transferSummary(report: TransferReport): string {
  return `${report.presets || 0}개 프리셋 · ${report.monitors || 0}개 예매 모니터`;
}
