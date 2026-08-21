import { stateMonitors } from '../../api/resources';
import type { WebUIState } from '../../api/proto';
import type { Notify } from '../../components/core/feedback';
import { useMonitorCommands } from './useMonitorCommands';
import { useMonitorEditor } from './useMonitorEditor';

export function useMonitors(
	state: WebUIState,
  userId: string,
	reload: () => Promise<WebUIState>,
  notify: Notify,
  onSaved: () => void,
) {
  const editor = useMonitorEditor(state, userId, reload, notify, onSaved);
	const commands = useMonitorCommands(state, userId, reload, notify);
	return { monitors: stateMonitors(state), ...editor, ...commands };
}
