import type { AppState } from '../../api/types';
import type { Notify } from '../../components/core/feedback';
import { useMonitorCommands } from './useMonitorCommands';
import { useMonitorEditor } from './useMonitorEditor';

export function useMonitors(
  state: AppState,
  userId: string,
  reload: () => Promise<AppState>,
  notify: Notify,
  onSaved: () => void,
) {
  const editor = useMonitorEditor(state, userId, reload, notify, onSaved);
  const commands = useMonitorCommands(state.monitors, userId, reload, notify);
  return { monitors: state.monitors, ...editor, ...commands };
}
