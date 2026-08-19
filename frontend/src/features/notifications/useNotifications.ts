import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from '../../api/client';
import type { AppEvent } from '../../api/types';
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
		void api<AppEvent>('/api/events', { method: 'POST', body: {
			userId: userId.current, kind: 'ui.feedback', tone, message,
		} }).then((event) => setNotices((current) => prependNotice(current, eventNotice(event)))).catch(() => undefined);
    }
  }, []);

	const load = useCallback(async (activeUserId: string) => {
		const request = ++loadRequest.current;
		userId.current = activeUserId;
		const events = await api<AppEvent[]>(`/api/events?user=${encodeURIComponent(activeUserId)}`);
		if (request !== loadRequest.current) return;
		setNotices(events.map(eventNotice));
	}, []);
	const markRead = useCallback(() => {
		setNotices(markNoticesRead);
		void api('/api/events/read', { method: 'POST', body: { userId: userId.current } }).catch(() => undefined);
	}, []);
	const clear = useCallback(() => {
		loadRequest.current++;
		setNotices([]);
		void api('/api/events', { method: 'DELETE', body: { userId: userId.current } }).catch(() => undefined);
	}, []);
  const dismissFeedback = useCallback(() => setFeedback(null), []);

	return { notices, feedback, notify, load, markRead, clear, dismissFeedback };
}

function eventNotice(event: AppEvent): Notice {
	return {
		id: event.id, message: event.message, tone: event.tone,
		createdAt: event.createdAt, read: Boolean(event.readAt),
	};
}
