import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect } from 'react';
import type { Command, Concept, Lab } from '@cipherkeep/shared';
import { api, listAll, type Meta, type SearchResponse, type Settings } from './api';

export const keys = {
  all: (c: string) => ['coll', c] as const,
  search: (q: string, type: string) => ['search', type, q] as const,
  meta: ['meta'] as const,
  settings: ['settings'] as const,
  backups: ['backups'] as const,
  imports: ['imports'] as const,
};

export const useCommands = () =>
  useQuery({
    queryKey: keys.all('commands'),
    queryFn: () => listAll<Command>('commands'),
    staleTime: 30_000,
  });
export const useConcepts = () =>
  useQuery({
    queryKey: keys.all('concepts'),
    queryFn: () => listAll<Concept>('concepts'),
    staleTime: 30_000,
  });
export const useLabs = () =>
  useQuery({ queryKey: keys.all('labs'), queryFn: () => listAll<Lab>('labs'), staleTime: 30_000 });
export const useMeta = () => useQuery({ queryKey: keys.meta, queryFn: () => api<Meta>('/meta') });
export const useSettings = () =>
  useQuery({
    queryKey: keys.settings,
    queryFn: () => api<Settings>('/settings'),
    staleTime: Infinity,
  });

export function useSearch(q: string, type = '') {
  const term = q.trim();
  return useQuery({
    queryKey: keys.search(term, type),
    queryFn: () =>
      api<SearchResponse>(
        `/search?q=${encodeURIComponent(term)}&limit=${type ? 500 : 14}${type ? `&type=${type}` : ''}`,
      ),
    enabled: term.length > 0,
    placeholderData: keepPreviousData,
    staleTime: 5_000,
  });
}

export function useSaveSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (s: Settings) => api<Settings>('/settings', { method: 'PUT', json: s }),
    // apply instantly; roll back if the server rejects it
    onMutate: async (s) => {
      const prev = qc.getQueryData<Settings>(keys.settings);
      qc.setQueryData(keys.settings, s);
      return { prev };
    },
    onError: (_e, _s, ctx) => ctx?.prev && qc.setQueryData(keys.settings, ctx.prev),
    onSuccess: (s) => qc.setQueryData(keys.settings, s),
  });
}

/** PATCH a command with an optimistic cache update (used for mastery / favourite / archive). */
export function usePatchCommand() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      patch,
    }: {
      id: string;
      patch: Partial<Command> | Record<string, unknown>;
    }) => api<Command>(`/commands/${id}`, { method: 'PATCH', json: patch }),
    onMutate: async ({ id, patch }) => {
      await qc.cancelQueries({ queryKey: keys.all('commands') });
      const prev = qc.getQueryData<Command[]>(keys.all('commands'));
      qc.setQueryData<Command[]>(keys.all('commands'), (old) =>
        old?.map((c) => (c.id === id ? ({ ...c, ...patch } as Command) : c)),
      );
      return { prev };
    },
    onError: (_e, _v, ctx) => ctx?.prev && qc.setQueryData(keys.all('commands'), ctx.prev),
    onSettled: () => void qc.invalidateQueries({ queryKey: keys.all('commands') }),
  });
}

export function useSaveCommand() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, payload }: { id?: string; payload: Record<string, unknown> }) =>
      id
        ? api<Command>(`/commands/${id}`, { method: 'PATCH', json: payload })
        : api<Command>('/commands', { method: 'POST', json: payload }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.all('commands') });
      void qc.invalidateQueries({ queryKey: keys.meta });
      void qc.invalidateQueries({ queryKey: ['search'] });
    },
  });
}

/** Live refresh: the API streams change events (hand edits, imports) over SSE. */
export function useLiveEvents(): void {
  const qc = useQueryClient();
  useEffect(() => {
    const ctl = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const refresh = () => {
      clearTimeout(timer);
      timer = setTimeout(() => {
        void qc.invalidateQueries({ queryKey: ['coll'] });
        void qc.invalidateQueries({ queryKey: ['search'] });
        void qc.invalidateQueries({ queryKey: keys.meta });
        void qc.invalidateQueries({ queryKey: ['overview'] });
      }, 150);
    };
    const b = window.cipherkeep;
    if (b?.onEvent) {
      // Wails: the shell forwards the library's events directly.
      const off = b.onEvent((kind) => {
        if (kind === 'change' || kind === 'reload') refresh();
      });
      return () => {
        off();
        clearTimeout(timer);
      };
    }
    (async () => {
      const res = await fetch(b ? `${b.apiBase}/api/v1/events` : '/api/v1/events', {
        headers: b?.token ? { authorization: `Bearer ${b.token}` } : {},
        signal: ctl.signal,
      }).catch(() => null);
      if (!res?.body) return;
      const reader = res.body.getReader();
      const dec = new TextDecoder();
      let buf = '';
      for (;;) {
        const { value, done } = await reader.read().catch(() => ({ value: undefined, done: true }));
        if (done || !value) return;
        buf += dec.decode(value, { stream: true });
        if (/event: (change|reload)/.test(buf)) refresh();
        buf = buf.slice(-200);
      }
    })();
    return () => {
      ctl.abort();
      clearTimeout(timer);
    };
  }, [qc]);
}
