import { useCallback, useState } from 'react';
import { homeRoute, routeSection, sectionRoute, type MainSection, type Route } from './router';

export function useRouter() {
  const [route, setRoute] = useState<Route>(homeRoute);

  return {
    route,
    activeSection: routeSection(route),
    navigate: setRoute,
    navigateSection: useCallback((section: MainSection) => setRoute(sectionRoute(section)), []),
    openSettings: useCallback(() => setRoute({ name: 'settings' }), []),
  };
}
