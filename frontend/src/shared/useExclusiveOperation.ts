import { useCallback, useRef, useState } from 'react';

// active is the UI projection of a synchronous lock: render-state booleans
// cannot reject two calls made before React renders again.
export function useExclusiveOperation() {
  const owner = useRef<symbol | null>(null);
  const [active, setActive] = useState<string | null>(null);
  const isRunning = useCallback(() => owner.current !== null, []);
  const start = useCallback((name: string) => {
    if (owner.current) return null;
    const token = Symbol(name);
    owner.current = token;
    setActive(name);
    return () => {
      if (owner.current !== token) return;
      owner.current = null;
      setActive(null);
    };
  }, []);
  return { active, start, isRunning };
}
