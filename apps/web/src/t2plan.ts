// T2 browser-local pool placement (FR-X02): fill-in-order over user-chosen folders with a quota each.
// Pure — used by the extension's T2 page and unit-tested here.

export interface PoolFolder {
  id: string;
  name: string;
  quotaBytes: number; // what the user allows Continuum to use in this folder (browsers cannot statfs)
  usedBytes: number; // measured by walking the folder
}

export interface PoolItem {
  path: string; // relative, "/"-separated
  size: number;
}

export interface PoolPlacement {
  path: string;
  size: number;
  folderId: string;
}

export interface PoolPlan {
  placements: PoolPlacement[];
  unplaced: PoolItem[];
  perFolder: Record<string, number>; // bytes planned per folder
}

/** Files are never split; each goes to the first folder (pool order) whose remaining quota − reserve fits. */
export function planPool(folders: PoolFolder[], items: PoolItem[], reserveBytes = 0): PoolPlan {
  const left = new Map(folders.map((f) => [f.id, f.quotaBytes - f.usedBytes - reserveBytes]));
  const plan: PoolPlan = { placements: [], unplaced: [], perFolder: {} };
  for (const it of items) {
    const f = folders.find((x) => (left.get(x.id) ?? 0) >= it.size);
    if (!f) {
      plan.unplaced.push(it);
      continue;
    }
    left.set(f.id, (left.get(f.id) ?? 0) - it.size);
    plan.placements.push({ path: it.path, size: it.size, folderId: f.id });
    plan.perFolder[f.id] = (plan.perFolder[f.id] ?? 0) + it.size;
  }
  return plan;
}

/** Merge per-folder listings: one entry per name, the lowest pool position wins (FR-A07). */
export function mergeListings(lists: { folderId: string; names: string[] }[]): { name: string; folderId: string }[] {
  const seen = new Map<string, string>();
  for (const l of lists) for (const n of l.names) if (!seen.has(n)) seen.set(n, l.folderId);
  return [...seen.entries()].map(([name, folderId]) => ({ name, folderId })).sort((a, b) => a.name.localeCompare(b.name));
}
