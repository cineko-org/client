import { useCallback, useEffect, useRef, useState } from 'react';
import { create } from '@bufbuild/protobuf';
import { api, errorMessage, logClientEvent } from '../../api/client';
import {
	AppEventSchema, ResourceSchema, WebUIActionStatusSchema, WebUIAppEventUserRequestSchema, WebUIResourceListSchema,
	type AppEvent,
} from '../../api/proto';
import { eventTone } from '../../api/resources';
import type { NotifyOptions } from '../../components/core/feedback';
import { prependNotice, type Feedback, type Notice } from './model';
import { refreshAfterMutation } from '../../shared/refreshAfterMutation';

export function useNotifications() {
  const [notices, setNotices] = useState<Notice[]>([]);
  const [feedback, setFeedback] = useState<Feedback | null>(null);
  const timer = useRef<number | undefined>(undefined);
	const userId = useRef('local-user');
	const loadRequest = useRef(0);
  const writes = useRef(Promise.resolve());

  const invalidate = useCallback(() => { loadRequest.current++; window.clearTimeout(timer.current); }, []);
  useEffect(() => invalidate, [invalidate]);

  const showFeedback = useCallback((message: string, options: NotifyOptions = {}) => {
    const tone = options.tone ?? 'info';
    const id = crypto.randomUUID();
    setFeedback({ id, message, tone });
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setFeedback(null), 3600);
  }, []);

  // All local notification writes keep their user-requested order. Reads never
  // overwrite a later confirmed mutation, and failed writes do not fake success.
  const enqueue = useCallback((action: () => Promise<void>) => {
    const next = writes.current.then(action);
    writes.current = next.catch((error) => {
      showFeedback(`알림 변경을 저장하지 못했습니다: ${errorMessage(error)}`, { tone: 'error' });
      logClientEvent('warn', 'notifications.write.failed', { scenario: 'notifications', operation: 'persist_notification', error: String(error) });
    });
    return writes.current;
  }, [showFeedback]);

  const notify = useCallback((message: string, options: NotifyOptions = {}) => {
    showFeedback(message, options);
    if (options.important) {
		const event = create(AppEventSchema, {
			userId: userId.current, kind: 'ui.feedback', message,
			tone: { case: options.tone ?? 'info', value: {} },
		});
      void enqueue(async () => {
        const resource = await api('/api/events', ResourceSchema, { method: 'POST' }, AppEventSchema, event);
				loadRequest.current++;
				const created = resource.resource;
				if (created?.case === 'appEvent') setNotices((current) => prependNotice(current, eventNotice(created.value)));
      });
    }
  }, [enqueue, showFeedback]);

	const load = useCallback(async (activeUserId: string) => {
		const request = ++loadRequest.current;
		userId.current = activeUserId;
		const response = await api(`/api/events?user=${encodeURIComponent(activeUserId)}`, WebUIResourceListSchema);
		if (request !== loadRequest.current) return;
		setNotices(response.resources.flatMap((resource) => resource.resource.case === 'appEvent' ? [eventNotice(resource.resource.value)] : []));
	}, []);
  const mutate = useCallback((clear: boolean) => {
    const activeUser = userId.current;
    return enqueue(async () => {
      loadRequest.current++;
      await api(clear ? '/api/events' : '/api/events/read', WebUIActionStatusSchema,
        { method: clear ? 'DELETE' : 'POST' }, WebUIAppEventUserRequestSchema,
        create(WebUIAppEventUserRequestSchema, { userId: activeUser }));
      loadRequest.current++;
      await refreshAfterMutation(() => load(activeUser), showFeedback);
    });
  }, [enqueue, load, showFeedback]);
  const markRead = useCallback(() => mutate(false), [mutate]);
  const clear = useCallback(() => mutate(true), [mutate]);
  const dismissFeedback = useCallback(() => setFeedback(null), []);

	return { notices, feedback, notify, load, markRead, clear, dismissFeedback };
}

function eventNotice(event: AppEvent): Notice {
	return {
		id: event.id, message: event.message, tone: eventTone(event),
		createdAt: event.createdAt ? new Date(Number(event.createdAt.seconds) * 1000).toISOString() : '', read: Boolean(event.readAt),
	};
}
