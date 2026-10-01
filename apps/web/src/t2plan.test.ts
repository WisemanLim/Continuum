import { describe, expect, it } from "vitest";
import { mergeListings, planPool } from "./t2plan";

const MB = 1_000_000;

describe("T2 pool placement (FR-X02)", () => {
  it("fills folders in order, spills over, never splits files", () => {
    const folders = [
      { id: "a", name: "USB", quotaBytes: 10 * MB, usedBytes: 2 * MB },
      { id: "b", name: "Downloads", quotaBytes: 5 * MB, usedBytes: 0 },
    ];
    const items = [
      { path: "x/1.bin", size: 5 * MB },
      { path: "x/2.bin", size: 4 * MB }, // a has 3 MB left → b
      { path: "x/3.bin", size: 2 * MB }, // a still fits
      { path: "x/4.bin", size: 9 * MB }, // fits nowhere
    ];
    const p = planPool(folders, items);
    expect(p.placements.map((x) => x.folderId)).toEqual(["a", "b", "a"]);
    expect(p.unplaced.map((x) => x.path)).toEqual(["x/4.bin"]);
    expect(p.perFolder).toEqual({ a: 7 * MB, b: 4 * MB });
  });
  it("honours the reserve", () => {
    const p = planPool([{ id: "a", name: "a", quotaBytes: 10 * MB, usedBytes: 0 }], [{ path: "f", size: 9 * MB }], 2 * MB);
    expect(p.unplaced).toHaveLength(1);
  });
  it("merges listings with the lowest position winning", () => {
    expect(
      mergeListings([
        { folderId: "a", names: ["b.txt", "a.txt"] },
        { folderId: "b", names: ["a.txt", "c.txt"] },
      ]),
    ).toEqual([
      { name: "a.txt", folderId: "a" },
      { name: "b.txt", folderId: "a" },
      { name: "c.txt", folderId: "b" },
    ]);
  });
});
