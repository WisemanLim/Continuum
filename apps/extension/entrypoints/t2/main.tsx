// CTM-EXT-040 T2 browser-local pool (FR-X02): folders granted through the File System Access API form an
// ordered pool; the extension's placement engine fills them in order (quota per folder, files never split)
// and verifies every copy by re-reading it (SHA-256 — WebCrypto has no BLAKE3). No Controller needed.
import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import "../../../web/src/styles.css";
import { parseSize } from "../../../web/src/logic";
import { mergeListings, planPool, type PoolPlan } from "../../../web/src/t2plan";

type Folder = { id: string; name: string; quotaBytes: number; handle: FileSystemDirectoryHandle; usedBytes?: number; granted?: boolean };
type DirPicker = (o?: { mode?: "read" | "readwrite"; id?: string }) => Promise<FileSystemDirectoryHandle>;
type PermHandle = FileSystemDirectoryHandle & {
  queryPermission?: (d: { mode: string }) => Promise<PermissionState>;
  requestPermission?: (d: { mode: string }) => Promise<PermissionState>;
};

const testMode = new URLSearchParams(location.search).has("opfs"); // E2E: OPFS folders stand in for picked ones

// ---- IndexedDB (handles are structured-cloneable; nothing leaves this browser) ----
function db(): Promise<IDBDatabase> {
  return new Promise((res, rej) => {
    const r = indexedDB.open("continuum-t2", 1);
    r.onupgradeneeded = () => r.result.createObjectStore("folders", { keyPath: "id" });
    r.onsuccess = () => res(r.result);
    r.onerror = () => rej(r.error);
  });
}
async function loadFolders(): Promise<Folder[]> {
  const d = await db();
  return new Promise((res) => {
    const q = d.transaction("folders").objectStore("folders").getAll();
    q.onsuccess = () => res((q.result as (Folder & { pos: number })[]).sort((a, b) => a.pos - b.pos));
  });
}
async function saveFolders(fs: Folder[]) {
  const d = await db();
  const tx = d.transaction("folders", "readwrite");
  const st = tx.objectStore("folders");
  st.clear();
  fs.forEach((f, pos) => st.put({ id: f.id, name: f.name, quotaBytes: f.quotaBytes, handle: f.handle, pos }));
  return new Promise((res) => (tx.oncomplete = res));
}

async function usage(dir: FileSystemDirectoryHandle): Promise<number> {
  let n = 0;
  for await (const [, h] of dir as unknown as AsyncIterable<[string, FileSystemHandle]>) {
    if (h.kind === "file") n += (await (h as FileSystemFileHandle).getFile()).size;
    else n += await usage(h as FileSystemDirectoryHandle);
  }
  return n;
}

async function subdir(root: FileSystemDirectoryHandle, path: string, create: boolean) {
  let d = root;
  for (const part of path.split("/").filter(Boolean)) d = await d.getDirectoryHandle(part, { create });
  return d;
}

async function sha256(b: Blob) {
  const h = await crypto.subtle.digest("SHA-256", await b.arrayBuffer());
  return [...new Uint8Array(h)].map((x) => x.toString(16).padStart(2, "0")).join("");
}

function fmt(n: number) {
  const u = ["B", "kB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1000 && i < u.length - 1) {
    n /= 1000;
    i++;
  }
  return `${n.toFixed(i ? 1 : 0)} ${u[i]}`;
}

function Pool() {
  const [folders, setFolders] = useState<Folder[]>([]);
  const [files, setFiles] = useState<File[]>([]);
  const [dest, setDest] = useState("/");
  const [plan, setPlan] = useState<PoolPlan | null>(null);
  const [log, setLog] = useState<string[]>([]);
  const [listing, setListing] = useState<{ name: string; folderId: string }[]>([]);
  const [busy, setBusy] = useState(false);

  const refresh = async (fs: Folder[]) => {
    const out: Folder[] = [];
    for (const f of fs) {
      const ph = f.handle as PermHandle;
      const granted = !ph.queryPermission || (await ph.queryPermission({ mode: "readwrite" })) === "granted";
      out.push({ ...f, granted, usedBytes: granted ? await usage(f.handle).catch(() => 0) : 0 });
    }
    setFolders(out);
    setListing(await merged(out, "/"));
  };
  useEffect(() => {
    loadFolders().then(refresh);
  }, []);
  const update = async (fs: Folder[]) => {
    await saveFolders(fs);
    await refresh(fs);
    setPlan(null);
  };

  const add = async () => {
    let handle: FileSystemDirectoryHandle;
    if (testMode) {
      const root = await navigator.storage.getDirectory();
      handle = await root.getDirectoryHandle(`folder-${folders.length + 1}`, { create: true });
    } else {
      const pick = (window as unknown as { showDirectoryPicker?: DirPicker }).showDirectoryPicker;
      if (!pick) return setLog((l) => [...l, "This browser has no File System Access API."]);
      handle = await pick({ mode: "readwrite", id: "continuum-pool" });
    }
    await update([...folders, { id: crypto.randomUUID(), name: handle.name, quotaBytes: 1_000_000_000, handle }]);
  };
  const move = (i: number, d: number) => {
    const fs = [...folders];
    const [f] = fs.splice(i, 1);
    fs.splice(i + d, 0, f!);
    update(fs);
  };

  async function merged(fs: Folder[], path: string) {
    const lists = [];
    for (const f of fs) {
      if (!f.granted) continue;
      const names: string[] = [];
      try {
        const d = await subdir(f.handle, path, false);
        for await (const [n] of d as unknown as AsyncIterable<[string, FileSystemHandle]>) names.push(n);
      } catch {
        /* folder has no such path */
      }
      lists.push({ folderId: f.id, names });
    }
    return mergeListings(lists);
  }

  const preview = () => {
    const items = files.map((f) => ({ path: (dest.replace(/\/+$/, "") + "/" + (f.webkitRelativePath || f.name)).replace(/^\/+/, ""), size: f.size }));
    setPlan(planPool(folders.map((f) => ({ id: f.id, name: f.name, quotaBytes: f.quotaBytes, usedBytes: f.usedBytes ?? 0 })), items));
  };

  const copy = async () => {
    if (!plan) return;
    setBusy(true);
    const byPath = new Map(files.map((f) => [(dest.replace(/\/+$/, "") + "/" + (f.webkitRelativePath || f.name)).replace(/^\/+/, ""), f]));
    let ok = 0;
    for (const p of plan.placements) {
      const folder = folders.find((f) => f.id === p.folderId)!;
      const src = byPath.get(p.path)!;
      const parts = p.path.split("/");
      const name = parts.pop()!;
      try {
        const dir = await subdir(folder.handle, parts.join("/"), true);
        const fh = await dir.getFileHandle(name, { create: true });
        const w = await fh.createWritable(); // written to a swap file; becomes visible on close (atomic)
        await w.write(src);
        await w.close();
        const [a, b] = await Promise.all([sha256(src), sha256(await fh.getFile())]);
        if (a !== b) throw new Error("checksum mismatch");
        ok++;
        setLog((l) => [...l, `✓ ${p.path} → ${folder.name}`]);
      } catch (e) {
        setLog((l) => [...l, `✕ ${p.path}: ${(e as Error).message}`]);
      }
    }
    setLog((l) => [...l, `done: ${ok}/${plan.placements.length} verified (SHA-256), ${plan.unplaced.length} did not fit`]);
    setBusy(false);
    await refresh(folders);
    setPlan(null);
  };

  return (
    <main className="stack" style={{ padding: 24, maxWidth: 960, margin: "0 auto" }}>
      <div className="row">
        <h1 className="spacer">Browser-local pool (T2)</h1>
        <span className="badge" data-testid="t2-mode">
          T2 · no Controller
        </span>
      </div>
      <div className="sub">
        Folders you grant are used in this order. Files are never split; when a folder&apos;s quota is used up the next folder continues. Everything stays in this browser profile.
      </div>
      <section className="card stack">
        <div className="row">
          <h2 className="spacer">Folders</h2>
          <button className="btn primary" onClick={add} data-testid="t2-add">
            + Add folder
          </button>
        </div>
        <table className="t" data-testid="t2-folders">
          <tbody>
            {folders.map((f, i) => (
              <tr key={f.id}>
                <td>
                  <span className="chip">{i + 1}</span>
                </td>
                <td>
                  <b>{f.name}</b>
                  {!f.granted && (
                    <button
                      className="btn sm"
                      onClick={async () => {
                        await (f.handle as PermHandle).requestPermission?.({ mode: "readwrite" });
                        refresh(folders);
                      }}
                    >
                      Grant access
                    </button>
                  )}
                </td>
                <td className="sub">{fmt(f.usedBytes ?? 0)} used</td>
                <td>
                  <input
                    className="input"
                    aria-label={`Quota of ${f.name}`}
                    defaultValue={fmt(f.quotaBytes)}
                    style={{ width: 110 }}
                    onBlur={(e) => {
                      const q = parseSize(e.target.value);
                      if (q !== null) update(folders.map((x) => (x.id === f.id ? { ...x, quotaBytes: q } : x)));
                    }}
                  />
                </td>
                <td>
                  <button className="btn sm" disabled={i === 0} aria-label={`Move ${f.name} up`} onClick={() => move(i, -1)}>
                    ↑
                  </button>
                  <button className="btn sm" disabled={i === folders.length - 1} aria-label={`Move ${f.name} down`} onClick={() => move(i, 1)}>
                    ↓
                  </button>
                  <button className="btn sm danger" onClick={() => update(folders.filter((x) => x.id !== f.id))}>
                    Remove
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {folders.length === 0 && <div className="empty">Add folders (e.g. a USB stick and a local folder) to build the pool.</div>}
      </section>
      <section className="card stack">
        <h2>Copy into the pool</h2>
        <div className="row wrap">
          <input type="file" multiple data-testid="t2-files" onChange={(e) => (setFiles([...(e.target.files ?? [])]), setPlan(null))} />
          <label className="field">
            Destination
            <input className="input mono" value={dest} onChange={(e) => setDest(e.target.value)} style={{ width: 200 }} />
          </label>
          <button className="btn" disabled={!files.length || !folders.length} onClick={preview}>
            Preview placement
          </button>
          <button className="btn primary" disabled={!plan || busy || plan.placements.length === 0} onClick={copy}>
            Copy
          </button>
        </div>
        {plan && (
          <div className="stack" data-testid="t2-plan">
            {folders.map((f) => (
              <div key={f.id} className="row sub">
                <span className="spacer">{f.name}</span>
                <span>
                  {plan.placements.filter((p) => p.folderId === f.id).length} files · {fmt(plan.perFolder[f.id] ?? 0)}
                </span>
              </div>
            ))}
            {plan.unplaced.length > 0 && <div className="banner">{plan.unplaced.length} file(s) do not fit any folder&apos;s quota</div>}
          </div>
        )}
        {log.length > 0 && (
          <pre className="codeblock" data-testid="t2-log">
            {log.join("\n")}
          </pre>
        )}
      </section>
      <section className="card stack">
        <h2>Pool contents (/)</h2>
        <table className="t" data-testid="t2-listing">
          <tbody>
            {listing.map((e) => (
              <tr key={e.name}>
                <td>{e.name}</td>
                <td className="sub">{folders.find((f) => f.id === e.folderId)?.name}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
    </main>
  );
}

createRoot(document.getElementById("root")!).render(<Pool />);
