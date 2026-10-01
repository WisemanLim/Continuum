// CTM-EXT-020 Side panel (T1 remote console summary) + CTM-EXT-010 mode detection + FR-X04 capability badges.
// Talks to the same Controller REST API as the desktop and CLI (DP05). Minimal permissions (FR-X05).
import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import "../../../web/src/styles.css";

type Summary = {
  tiles: Record<string, number>;
  volumes: { name: string; slug: string; summary: { disk_count: number; usable_bytes: number; free_bytes: number } }[];
  warnings: string[];
};

const KEY = "controllerUrl";
const DEFAULT = "http://127.0.0.1:8080";

function fmt(n: number) {
  const u = ["B", "kB", "MB", "GB", "TB", "PB"];
  let i = 0;
  while (n >= 1000 && i < u.length - 1) {
    n /= 1000;
    i++;
  }
  return `${n.toFixed(i ? 1 : 0)} ${u[i]}`;
}

// Capability matrix (PRD §14.1). T1 = remote console via Controller; T3 = + native bridge to the local agent.
const caps = (t3: boolean): [string, boolean][] => [
  ["Volumes · ordinal DnD (via Controller)", true],
  ["Copy · move · delete (Agent executes)", true],
  ["Transfers progress", true],
  ["Local disk discovery (UUID · free space · bus)", t3],
  ["Native bridge to local Agent (T3)", t3],
  ["Browser-local folder pool (T2)", true],
];

const HOST = "com.continuum.agent";

type Native = {
  hello: { version: string; hostname: string; os: string; enrolled: boolean; node_id?: string; controller_api: string };
  disks: { mount_path: string; volume_label: string; filesystem_type: string; available_bytes: number; bus_type: string }[];
};

// tryNative connects to the native host when the optional nativeMessaging permission is granted (FR-X03).
async function tryNative(): Promise<Native | null> {
  const has = await browser.permissions.contains({ permissions: ["nativeMessaging"] }).catch(() => false);
  if (!has) return null;
  try {
    const hello = await browser.runtime.sendNativeMessage(HOST, { type: "hello" });
    const d = await browser.runtime.sendNativeMessage(HOST, { type: "discover" });
    return { hello, disks: d?.disks ?? [] };
  } catch {
    return null; // host not installed → stay T1 (automatic downgrade)
  }
}

function Panel() {
  const [url, setUrl] = useState(DEFAULT);
  const [draft, setDraft] = useState(DEFAULT);
  const [data, setData] = useState<Summary | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [native, setNative] = useState<Native | null>(null);
  const [nativeMsg, setNativeMsg] = useState("");

  useEffect(() => {
    Promise.all([browser.storage.local.get(KEY), tryNative()]).then(([r, n]) => {
      setNative(n);
      // the local agent knows the Controller this machine uses
      const u = (r[KEY] as string) || n?.hello.controller_api || DEFAULT;
      setUrl(u);
      setDraft(u);
    });
  }, []);
  const connectNative = async () => {
    const ok = await browser.permissions.request({ permissions: ["nativeMessaging"] }).catch(() => false);
    if (!ok) return setNativeMsg("Permission not granted — staying in T1 mode.");
    const n = await tryNative();
    setNative(n);
    if (!n) setNativeMsg(`Local agent not found. Install it, then run: continuum-agent native-host install --extension-id ${browser.runtime.id}`);
  };

  useEffect(() => {
    let alive = true;
    const load = () =>
      fetch(`${url}/v1/overview`)
        .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`HTTP ${r.status}`))))
        .then((d) => alive && (setData(d), setErr(null)))
        .catch((e) => alive && (setData(null), setErr(String(e.message || e))));
    load();
    const t = setInterval(load, 5000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [url]);

  const save = async () => {
    await browser.storage.local.set({ [KEY]: draft });
    setUrl(draft);
  };

  return (
    <main className="stack" style={{ padding: 16, minWidth: 360, gap: 12 }}>
      <div className="row">
        <span className="brand-mark" style={{ fontSize: 22 }}>
          ∞
        </span>
        <b className="spacer" style={{ fontSize: 16 }}>
          Continuum
        </b>
        <span className="badge always-on" data-testid="mode">
          {native ? "T3 native bridge" : "T1 remote console"}
        </span>
      </div>
      <div className="card stack" style={{ gap: 6 }}>
        <label className="field">
          Controller URL
          <input className="input mono" value={draft} onChange={(e) => setDraft(e.target.value)} />
        </label>
        <div className="row">
          <button className="btn sm primary" onClick={save}>
            Connect
          </button>
          <span className="status" data-testid="conn">
            <span className={`dot ${data ? "ok" : "danger"}`} /> {data ? "Connected" : err ? `Controller disconnected (${err})` : "…"}
          </span>
        </div>
      </div>
      {data && (
        <>
          <div className="tiles" style={{ gridTemplateColumns: "repeat(2, 1fr)", margin: 0 }}>
            <div className="card tile">
              <div className="sub">Free</div>
              <div className="v">{fmt((data.tiles.free_bytes ?? 0))}</div>
            </div>
            <div className="card tile">
              <div className="sub">Volumes</div>
              <div className="v" data-testid="volumes">
                {(data.tiles.volumes ?? 0)}
              </div>
            </div>
            <div className="card tile">
              <div className="sub">Running jobs</div>
              <div className="v">{(data.tiles.running_jobs ?? 0)}</div>
            </div>
            <div className="card tile">
              <div className="sub">Warnings</div>
              <div className="v">{(data.tiles.warnings ?? 0)}</div>
            </div>
          </div>
          {data.volumes.map((v) => (
            <div key={v.slug} className="card">
              <b>{v.name}</b>
              <div className="sub">
                {v.summary.disk_count} disks · {fmt(v.summary.usable_bytes)} usable · {fmt(v.summary.free_bytes)} free
              </div>
            </div>
          ))}
          {data.warnings.map((w, i) => (
            <div key={i} className="sub" style={{ color: "var(--warn)" }}>
              ▲ {w}
            </div>
          ))}
        </>
      )}
      {native ? (
        <div className="card stack" style={{ gap: 4 }} data-testid="native">
          <b>Local agent · {native.hello.hostname}</b>
          <div className="sub">
            {native.hello.os} · agent {native.hello.version} · {native.hello.enrolled ? `node ${native.hello.node_id}` : "not enrolled (single machine)"}
          </div>
          {native.disks.slice(0, 8).map((d) => (
            <div key={d.mount_path} className="row sub">
              <span className="mono spacer truncate">{d.mount_path}</span>
              <span>
                {d.filesystem_type} · {fmt(d.available_bytes)} free
              </span>
            </div>
          ))}
        </div>
      ) : (
        <div className="card stack" style={{ gap: 4 }}>
          <button className="btn sm" onClick={connectNative} data-testid="connect-native">
            Connect local agent (T3)
          </button>
          {nativeMsg && <div className="sub">{nativeMsg}</div>}
        </div>
      )}
      <button className="btn" onClick={() => browser.tabs.create({ url: browser.runtime.getURL("/t2.html") })}>
        Browser-local pool (T2) ↗
      </button>
      <button className="btn" onClick={() => browser.tabs.create({ url: browser.runtime.getURL("/console.html") })}>
        Open full console ↗
      </button>
      <div className="card stack" style={{ gap: 4 }}>
        <b>Capabilities in browser mode</b>
        {caps(!!native).map(([label, ok]) => (
          <div key={label} className="row sub">
            <span aria-hidden="true">{ok ? "✓" : "○"}</span>
            <span className="spacer">{label}</span>
            {!ok && <span className="badge">Not available in browser mode</span>}
          </div>
        ))}
      </div>
    </main>
  );
}

createRoot(document.getElementById("root")!).render(<Panel />);
