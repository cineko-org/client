import { describe, expect, it } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { routeSection, sectionRoute, type Route } from '../src/app/router';
import { useRouter } from '../src/app/useRouter';

describe('application routes', () => {
  it.each<[Route, string | null]>([
    [{ name: 'home' }, 'home'],
    [{ name: 'monitors' }, 'monitors'],
    [{ name: 'monitor-detail', monitorId: 'monitor' }, 'monitors'],
    [{ name: 'monitor-new' }, 'monitors'],
    [{ name: 'monitor-edit', monitorId: 'monitor' }, 'monitors'],
    [{ name: 'presets' }, 'presets'],
    [{ name: 'preset-new' }, 'presets'],
    [{ name: 'preset-edit', presetId: 'preset' }, 'presets'],
    [{ name: 'settings' }, null],
  ])('maps $name to its main section', (route, section) => {
    expect(routeSection(route)).toBe(section);
  });

  it('creates root routes from main navigation sections', () => {
    expect(sectionRoute('home')).toEqual({ name: 'home' });
    expect(sectionRoute('monitors')).toEqual({ name: 'monitors' });
    expect(sectionRoute('presets')).toEqual({ name: 'presets' });
  });

  it('owns typed route navigation and dedicated settings navigation', () => {
    const { result } = renderHook(() => useRouter());
    expect(result.current.route).toEqual({ name: 'home' });
    act(() => result.current.navigate({ name: 'monitor-detail', monitorId: 'monitor' }));
    expect(result.current.activeSection).toBe('monitors');
    act(() => result.current.navigateSection('presets'));
    expect(result.current.route).toEqual({ name: 'presets' });
    act(() => result.current.openSettings());
    expect(result.current.route).toEqual({ name: 'settings' });
  });
});
