import { describe, expect, it } from "vitest";
import type { Disk } from "./api";
import { barLevel, fmtBytes, fmtETA, insertAt, parseSize, portableInOrder, slugify, summarize } from "./logic";

const disk = (over: Partial<Disk>): Disk =>
  ({
    disk_id: "d",
    node_id: "n",
    name: "d",
    provider_type: "local-fs",
    filesystem_uuid: "u",
    mount_path: "/",
    data_root: "/.continuum",
    role: "always-on",
    state: "ONLINE",
    writable: true,
    total_bytes: 0,
    free_bytes: 0,
    available_bytes: 0,
    reserved_bytes: 0,
    removable: false,
    network_backed: false,
    effective_free_bytes: 0,
    effective_min_free_bytes: 0,
    used_pct: 0,
    ...over,
  }) as Disk;

describe("FR-V10 summary", () => {
  it("uses Σ(total − min_free) and Σ effective_free, not the mock image numbers", () => {
    const TB = 1e12,
      GB = 1e9;
    const ds = [
      disk({ total_bytes: 50 * TB, effective_free_bytes: 18 * TB }),
      disk({ total_bytes: 200 * GB, effective_free_bytes: 42 * GB }),
      disk({ total_bytes: 1 * TB, effective_free_bytes: 512 * GB, role: "portable" }),
    ];
    const s = summarize(ds, 100 * GB);
    expect(s.count).toBe(3);
    expect(s.usable).toBe(50 * TB - 100 * GB + (200 * GB - 100 * GB) + (1 * TB - 100 * GB));
    expect(s.free).toBe(18 * TB + 42 * GB + 512 * GB);
    expect(fmtBytes(s.usable)).toBe("50.9 TB"); // image showed "30 TB usable" — generative error (PRD §17 #24)
    expect(portableInOrder(ds).length).toBe(1);
  });
  it("per-disk min_free override wins", () => {
    expect(summarize([disk({ total_bytes: 1000, min_free_bytes: 100 })], 500).usable).toBe(900);
  });
});

describe("ordinal re-numbering", () => {
  it("inserts at slot and removes previous position", () => {
    expect(insertAt(["a", "b", "c"], "d", 1)).toEqual(["a", "d", "b", "c"]);
    expect(insertAt(["a", "b", "c"], "c", 0)).toEqual(["c", "a", "b"]);
    expect(insertAt(["a", "b"], "x", 99)).toEqual(["a", "b", "x"]);
  });
});

describe("format", () => {
  it("sizes, ETA, parse", () => {
    expect(fmtBytes(18e12)).toBe("18.0 TB");
    expect(fmtBytes(1073741824, "iec")).toBe("1.0 GiB");
    expect(fmtETA(522)).toBe("00:08:42");
    expect(parseSize("100GB")).toBe(100e9);
    expect(parseSize("1GiB")).toBe(1073741824);
    expect(parseSize("nope")).toBeNull();
    expect(barLevel(84)).toBe("ok");
    expect(barLevel(85)).toBe("warn");
    expect(barLevel(95)).toBe("danger");
    expect(slugify("Research Pool")).toBe("research-pool");
  });
});
