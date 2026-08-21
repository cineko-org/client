import { act, renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useEditorRouteInitialization } from '../src/app/useEditorRouteInitialization';

describe('editor route initialization', () => {
  it('does not reset a new editor when unrelated controller callbacks change', () => {
    const startNew = vi.fn<() => void>();
    const firstLoader = vi.fn<(id: string) => boolean>(() => false);
    const { rerender } = renderHook(
      ({ loadItem }) => useEditorRouteInitialization(null, loadItem, startNew),
      { initialProps: { loadItem: firstLoader } },
    );

    expect(startNew).toHaveBeenCalledTimes(1);
    act(() => rerender({ loadItem: vi.fn<(id: string) => boolean>(() => false) }));
    expect(startNew).toHaveBeenCalledTimes(1);
  });

  it('retries an edit only until its state becomes available', () => {
    const startNew = vi.fn<() => void>();
    const unavailable = vi.fn<(id: string) => boolean>(() => false);
    const { rerender } = renderHook(
      ({ loadItem }) => useEditorRouteInitialization('preset-1', loadItem, startNew),
      { initialProps: { loadItem: unavailable } },
    );

    expect(unavailable).toHaveBeenCalledWith('preset-1');
    const available = vi.fn<(id: string) => boolean>(() => true);
    act(() => rerender({ loadItem: available }));
    expect(available).toHaveBeenCalledWith('preset-1');

    const laterLoader = vi.fn<(id: string) => boolean>(() => true);
    act(() => rerender({ loadItem: laterLoader }));
    expect(laterLoader).not.toHaveBeenCalled();
    expect(startNew).not.toHaveBeenCalled();
  });
});
