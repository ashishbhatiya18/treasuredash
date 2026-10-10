"use client";
import { useCallback, useEffect, useState } from "react";

/** Loads data on mount and whenever deps change; `reload` refetches. */
export function useLoad<T>(fn: () => Promise<T>, deps: unknown[] = []) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const load = useCallback(fn, deps);
  const reload = useCallback(async () => {
    setLoading(true);
    try {
      setData(await load());
      setError(null);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, [load]);
  useEffect(() => {
    reload();
  }, [reload]);
  return { data, error, loading, reload, setData };
}

export function useQueryParam(name: string): string | null {
  const [v, setV] = useState<string | null>(null);
  useEffect(() => {
    setV(new URLSearchParams(window.location.search).get(name));
  }, [name]);
  return v;
}
