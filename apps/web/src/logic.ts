// Pure UI logic (unit-tested): formatting, FR-V10 summary, ordinal re-numbering.
import type { Disk } from "./api";

export type Units = "si" | "iec";

/** Human size — TB = 10^12 (si) or TiB = 2^40 (iec) (PRD §8.6). */
export function fmtBytes(n: number, units: Units = "si"): string {
  const base = units === "iec" ? 1024 : 1000;
  const suffix = units === "iec" ? ["B", "KiB", "MiB", "GiB", "TiB", "PiB"] : ["B", "kB", "MB", "GB", "TB", "PB"];
  if (!Number.isFinite(n) || n < base) return `${Math.max(0, Math.round(n || 0))} B`;
  let i = 0;
  let v = n;
  while (v >= base && i < suffix.length - 1) {
    v /= base;
    i++;
  }
  return `${v >= 100 ? v.toFixed(0) : v.toFixed(1)} ${suffix[i] ?? ""}`;
}

/** ETA as hh:mm:ss. */
export function fmtETA(sec: number): string {
  if (!Number.isFinite(sec) || sec <= 0) return "00:00:00";
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = Math.floor(sec % 60);
  return [h, m, s].map((x) => String(x).padStart(2, "0")).join(":");
}

export function parseSize(s: string): number | null {
  const m = /^\s*([0-9]+(?:\.[0-9]+)?)\s*([kmgtp]?)(i?)(b?)\s*$/i.exec(s);
  if (!m) return null;
  const [, num = "0", unit = "", iec = "", b = ""] = m;
  const base = iec || (unit && !b) ? 1024 : 1000;
  const exp = "kmgtp".indexOf(unit.toLowerCase()) + 1;
  return Math.round(parseFloat(num) * Math.pow(base, exp));
}

/** FR-V10: usable = Σ(total − min_free), free = Σ(effective_free). The image mock numbers are NOT used. */
export function summarize(disks: Disk[], volumeMinFree: number) {
  let total = 0,
    usable = 0,
    free = 0;
  for (const d of disks) {
    const mf = d.min_free_bytes ?? volumeMinFree;
    total += d.total_bytes;
    usable += Math.max(0, d.total_bytes - mf);
    free += d.effective_free_bytes;
  }
  return { count: disks.length, total, usable, free };
}

/** Insert `id` at position `index` (0-based) removing any previous occurrence; returns new order. */
export function insertAt(order: string[], id: string, index: number): string[] {
  const rest = order.filter((x) => x !== id);
  const i = Math.max(0, Math.min(index, rest.length));
  return [...rest.slice(0, i), id, ...rest.slice(i)];
}

/** Portable/import disks in automatic placement positions (FR-V09). */
export function portableInOrder(disks: Disk[]): Disk[] {
  return disks.filter((d) => d.role === "portable" || d.role === "import");
}

export function usedPct(d: Pick<Disk, "total_bytes" | "free_bytes">): number {
  if (!d.total_bytes) return 0;
  return Math.round(100 - (d.free_bytes * 100) / d.total_bytes);
}

/** Capacity bar colour class: 85% warn, 95% danger (PRD §8.6). */
export function barLevel(pct: number): "ok" | "warn" | "danger" {
  return pct >= 95 ? "danger" : pct >= 85 ? "warn" : "ok";
}

export function slugify(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

export function defaultMountPath(slug: string, platform = typeof navigator !== "undefined" ? navigator.platform : ""): string {
  if (/win/i.test(platform)) return `\\\\controller\\${slug || "volume"}`;
  if (/mac/i.test(platform)) return `/Volumes/${slug || "volume"}`;
  return `/mnt/${slug || "volume"}`;
}

export function osOf(d: Pick<Disk, "node_name" | "filesystem_type" | "network_backed">): "linux" | "windows" | "macos" | "nas" {
  const fs = (d.filesystem_type || "").toLowerCase();
  if (d.network_backed) return "nas";
  if (fs === "ntfs" || fs === "refs") return "windows";
  if (fs === "apfs" || fs === "hfs") return "macos";
  return "linux";
}
