import { useEffect, useRef } from 'react';

/** Initializes an editor once per route while allowing unavailable edit data to retry. */
export function useEditorRouteInitialization(
  itemId: string | null,
  loadItem: (id: string) => boolean,
  startNew: () => void,
) {
  const initializedRoute = useRef<string | null>(null);
  const routeKey = itemId ? `edit:${itemId}` : 'new';

  useEffect(() => {
    if (initializedRoute.current === routeKey) return;
    if (itemId) {
      if (!loadItem(itemId)) return;
    } else {
      startNew();
    }
    initializedRoute.current = routeKey;
  }, [itemId, loadItem, routeKey, startNew]);
}
