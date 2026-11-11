"use client";

import { useSyncExternalStore } from "react";

// What the task panel over the board needs from the board (task-board-ux 2.1, 2.4): whether the board is on screen
// under it, and the board's columns as the owner sees them (filters and order applied) for previous/next.
export type BoardColumn = { name: string; ids: string[] };
let state: { mounted: number; columns: BoardColumn[] } = { mounted: 0, columns: [] };
const listeners = new Set<() => void>();
const emit = () => { for (const f of listeners) f(); };
const subscribe = (f: () => void) => { listeners.add(f); return () => { listeners.delete(f); }; };
const empty = { mounted: 0, columns: [] as BoardColumn[] };
const useBoard = () => useSyncExternalStore(subscribe, () => state, () => empty);

// mountBoard marks the board as on screen until the returned function runs.
export function mountBoard() {
  state = { ...state, mounted: state.mounted + 1 };
  emit();
  return () => { state = { ...state, mounted: state.mounted - 1 }; emit(); };
}

const same = (a: BoardColumn[], b: BoardColumn[]) =>
  a.length === b.length && a.every((c, i) => c.name === b[i].name && c.ids.length === b[i].ids.length && c.ids.every((id, j) => id === b[i].ids[j]));

export function publishBoard(columns: BoardColumn[]) {
  if (same(columns, state.columns)) return;
  state = { ...state, columns };
  emit();
}

export const useBoardMounted = () => useBoard().mounted > 0;

// useNeighbours is the task before and after id in its board column, its column and its place there.
export function useNeighbours(id: string): { prev?: string; next?: string; column?: string; index: number; total: number } {
  const columns = useBoard().columns;
  for (const c of columns) {
    const i = c.ids.indexOf(id);
    if (i >= 0) return { prev: c.ids[i - 1], next: c.ids[i + 1], column: c.name, index: i, total: c.ids.length };
  }
  return { index: -1, total: 0 };
}

// The board's open (unfolded) columns per company, kept for the browser tab so they survive leaving the board for
// a task's full page and coming back (task-board-ux 2.3).
const openKey = (company: string) => `factory.tasks.open.${company}`;
export function savedOpenColumns(company: string): string[] {
  try { const v = JSON.parse(sessionStorage.getItem(openKey(company)) ?? "[]"); return Array.isArray(v) ? v : []; } catch { return []; }
}
export function saveOpenColumns(company: string, cols: string[]) {
  try { sessionStorage.setItem(openKey(company), JSON.stringify(cols)); } catch { /* private mode */ }
}
