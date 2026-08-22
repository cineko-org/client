import { useCallback, useEffect, useRef, useState } from 'react';
import { create } from '@bufbuild/protobuf';
import { api } from '../../api/client';
import {
	AppEventSchema, ResourceSchema, WebUIActionStatusSchema, WebUIAppEventUserRequestSchema, WebUIResourceListSchema,
	type AppEvent,
} from '../../api/proto';
import { eventTone } from '../../api/resources';
import type { NotifyOptions } from '../../components/core/feedback';
import { markNoticesRead, prependNotice, type Feedback, type Notice } from './model';

export function useNotifications() {
  const [notices, setNotices] = useState<Notice[]>([]);
  const [feedback, setFeedback] = useState<Feedback | null>(null);
  const timer = useRef<number | undefined>(undefined);
	const userId = useRef('local-user');
	const loadRequest = useRef(0);

  useEffect(() => () => window.clearTimeout(timer.current), []);

  const notify = useCallback((message: string, options: NotifyOptions = {}) => {
    const tone = options.tone ?? 'info';
    const id = crypto.randomUUID();
    setFeedback({ id, message, tone });
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setFeedback(null), 3600);
    if (options.important) {
		const event = create(AppEventSchema, {
			userId: userId.current, kind: 'ui.feedback', message,
			tone: { case: tone, value: {} },
		});
		void api('/api/events', ResourceSchema, { method: 'POST' }, AppEventSchema, event)
			.then((resource) => {
				const created = resource.resource;
				if (created?.case === 'appEvent') setNotices((current) => prependNotice(current, eventNotice(created.value)));
			})
			.catch(() => undefined);
    }
  }, []);

	const load = useCallback(async (activeUserId: string) => {
		const request = ++loadRequest.current;
		userId.current = activeUserId;
		const response = await api(`/api/events?user=${encodeURIComponent(activeUserId)}`, WebUIResourceListSchema);
		if (request !== loadRequest.current) return;
		setNotices(response.resources.flatMap((resource) => resource.resource.case === 'appEvent' ? [eventNotice(resource.resource.value)] : []));
	}, []);
	const markRead = useCallback(() => {
		setNotices(markNoticesRead);
		void api('/api/events/read', WebUIActionStatusSchema, { method: 'POST' }, WebUIAppEventUserRequestSchema,
			create(WebUIAppEventUserRequestSchema, { userId: userId.current })).catch(() => undefined);
	}, []);
	const clear = useCallback(() => {
		loadRequest.current++;
		setNotices([]);
		void api('/api/events', WebUIActionStatusSchema, { method: 'DELETE' }, WebUIAppEventUserRequestSchema,
			create(WebUIAppEventUserRequestSchema, { userId: userId.current })).catch(() => undefined);
	}, []);
  const dismissFeedback = useCallback(() => setFeedback(null), []);

	return { notices, feedback, notify, load, markRead, clear, dismissFeedback };
}

function eventNotice(event: AppEvent): Notice {
	return {
		id: event.id, message: event.message, tone: eventTone(event),
		createdAt: event.createdAt ? new Date(Number(event.createdAt.seconds) * 1000).toISOString() : '', read: Boolean(event.readAt),
	};
}
