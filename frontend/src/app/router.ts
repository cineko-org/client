export type Route =
  | { name: 'home' }
  | { name: 'monitors' }
  | { name: 'monitor-detail'; monitorId: string }
  | { name: 'monitor-new' }
  | { name: 'monitor-edit'; monitorId: string }
  | { name: 'presets' }
  | { name: 'preset-new' }
  | { name: 'preset-edit'; presetId: string }
  | { name: 'settings' };

export type MainSection = 'home' | 'monitors' | 'presets';

export const homeRoute: Route = { name: 'home' };

export function routeSection(route: Route): MainSection | null {
  switch (route.name) {
    case 'home':
      return 'home';
    case 'monitors':
    case 'monitor-detail':
    case 'monitor-new':
    case 'monitor-edit':
      return 'monitors';
    case 'presets':
    case 'preset-new':
    case 'preset-edit':
      return 'presets';
    case 'settings':
      return null;
  }
}

export function sectionRoute(section: MainSection): Route {
  return { name: section };
}
