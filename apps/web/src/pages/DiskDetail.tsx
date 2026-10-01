// CTM-DSK-020~023 Disk detail — overview·SMART, benchmark, usage, duplicates (FR-D27·D13·D14·D15·D16).
import { useState } from "react";
import { ApiError, api, type Disk, type UsageNode } from "../api";
import { useT } from "../i18n";
import { navigate } from "../nav";
import { RelocateDialog } from "./Relocate";
import { ConfirmDestructive, DiskFree, DiskUsage, ErrorState, RoleBadge, Status, useBytes, useLoad, useToast } from "../ui";

const TABS = ["overview", "benchmark", "usage", "duplicates"] as const;
type TabKey = "dd.tab.overview" | "dd.tab.benchmark" | "dd.tab.usage" | "dd.tab.duplicates";
const TAB_KEYS: TabKey[] = ["dd.tab.overview", "dd.tab.benchmark", "dd.tab.usage", "dd.tab.duplicates"];

export function DiskDetail({ id, tab }: { id: string; tab: string }) {
  const t = useT();
  const toast = useToast();
  const { data: d, error, reload } = useLoad(() => api.disk(id), [id], 5000);
  const [detach, setDetach] = useState(false);
  const [wipe, setWipe] = useState(false);
  const [drain, setDrain] = useState(false);
  if (error) return <ErrorState error={error} onRetry={reload} />;
  if (!d) return <div className="sub">{t("common.loading")}</div>;
  const act = (p: Promise<unknown>, msg: string) =>
    p.then(() => (toast(msg), reload())).catch((e: ApiError) => toast(e.message, "error"));
  return (
    <>
      <div className="row">
        <h1 className="spacer">{d.name}</h1>
        <RoleBadge role={d.role} />
        <Status state={d.state} />
      </div>
      <div className="row" style={{ marginBottom: 12, flexWrap: "wrap" }}>
        <button className="btn" onClick={() => act(api.reveal(d.disk_id), t("dd.openedInFM"))}>
          {t("dd.openFinder")}
        </button>
        {d.volume_id && (
          <button className="btn" onClick={() => act(api.scan(d.volume_id!, { disk: d.disk_id }), t("dd.scanFinished"))}>
            {t("dd.scan")}
          </button>
        )}
        <button className="btn" onClick={() => navigate(`/disks/${d.disk_id}/benchmark`)}>
          {t("dd.benchBtn")}
        </button>
        <span className="spacer" />
        {d.state === "ONLINE" && d.volume_id && (
          <button className="btn" onClick={() => setDrain(true)}>
            {t("dd.drainBtn")}
          </button>
        )}
        {d.state === "ONLINE" && (
          <button className="btn danger" onClick={() => setDetach(true)}>
            {t("dd.detachBtn")}
          </button>
        )}
        {!d.volume_id && d.state !== "OFFLINE" && (
          <button className="btn danger" onClick={() => setWipe(true)} title={t("dd.wipeHelp")}>
            {t("dd.wipe")}…
          </button>
        )}
      </div>
      <div className="tabs" role="tablist">
        {TABS.map((tabId, i) => (
          <button key={tabId} role="tab" aria-selected={tab === tabId} onClick={() => navigate(`/disks/${d.disk_id}/${tabId}`)}>
            {t(TAB_KEYS[i]!)}
          </button>
        ))}
      </div>
      {tab === "overview" && <OverviewTab d={d} />}
      {tab === "benchmark" && <BenchTab d={d} />}
      {tab === "usage" && <UsageTab d={d} />}
      {tab === "duplicates" && <DupesTab d={d} />}
      {drain && d.volume_id && (
        <RelocateDialog
          title={t("vd.drainTitle")}
          req={{ type: "DRAIN", volume: d.volume_id, source: d.disk_id, destination: "/" }}
          note={t("vd.drainNote")}
          onClose={() => setDrain(false)}
          onStarted={() => navigate("/transfers")}
        />
      )}
      {wipe && (
        <ConfirmDestructive
          action={t("dd.wipe")}
          name={d.name}
          onClose={() => setWipe(false)}
          onConfirm={() => {
            setWipe(false);
            api
              .wipe(d.disk_id, d.name)
              .then((r) => (toast(t("dd.wiped", { n: r.files })), navigate("/nodes")))
              .catch((e: ApiError) => toast(e.message, "error"));
          }}
        />
      )}
      {detach && (
        <ConfirmDestructive action={t("dd.detachDisk")} name={d.name} onClose={() => setDetach(false)} onConfirm={() => (setDetach(false), act(api.detach(d.disk_id), t("dd.detachedToast")))} />
      )}
    </>
  );
}

function OverviewTab({ d }: { d: Disk }) {
  const t = useT();
  const b = useBytes();
  const toast = useToast();
  const smart = useLoad(() => api.smart(d.disk_id), [d.disk_id]);
  const hist = useLoad(() => api.smartHistory(d.disk_id), [d.disk_id, smart.data?.checked_at]);
  const events = useLoad(() => api.activity(300), [d.disk_id]);
  const [busy, setBusy] = useState(false);
  const s = smart.data;
  const rows: [string, string][] = [
    [t("dd.diskId"), d.disk_id],
    [t("dd.filesystem"), `${d.filesystem_type || "?"} · UUID ${d.filesystem_uuid}`],
    [t("dd.labelBus"), `${d.volume_label || "—"} · ${d.bus_type || "?"}${d.removable ? " · removable" : ""}${d.network_backed ? " · network" : ""}`],
    [t("dd.volumeOrdinal"), d.volume_id ? `${d.volume_id} · #${d.ordinal}` : t("nod.unassigned")],
    [t("dd.mount"), d.mount_path],
    [t("dd.dataRoot"), d.data_root],
    [t("dd.node"), d.node_name || d.node_id],
    [t("dd.lastSeen"), d.last_seen ? new Date(d.last_seen).toLocaleString() : "—"],
  ];
  return (
    <div className="grid2">
      <div className="card stack">
        <h2>{t("dd.idCapacity")}</h2>
        <DiskUsage d={d} />
        <div className="row">
          <DiskFree d={d} />
          <span className="spacer" />
          <span className="sub">
            {b(d.total_bytes)} total · reserve {b(d.effective_min_free_bytes)} · reserved by jobs {b(d.reserved_bytes)}
          </span>
        </div>
        <table className="t kv">
          <tbody>
            {rows.map(([k, v]) => (
              <tr key={k}>
                <th>{k}</th>
                <td className="mono">{v}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="card stack">
        <div className="row">
          <h2 className="spacer">SMART</h2>
          <button
            className="btn sm"
            disabled={busy}
            onClick={() => {
              setBusy(true);
              api
                .smart(d.disk_id, true)
                .then((x) => smart.setData(x))
                .catch((e: ApiError) => toast(e.message, "error"))
                .finally(() => setBusy(false));
            }}
          >
            {t("dd.refresh")}
          </button>
        </div>
        {!s ? (
          <div className="sub">{t("common.loading")}</div>
        ) : (
          <>
            <div className="row" data-testid="smart-status">
              <Status state={s.passed === false ? "FAILED" : s.available ? "ONLINE" : "DISABLED"} label={`${s.status} (${s.source})`} />
              {s.temperature_c !== undefined && <span className="chip">{s.temperature_c} °C</span>}
              {s.power_on_hours !== undefined && <span className="chip">{s.power_on_hours} h</span>}
            </div>
            {s.model && <div className="sub">{[s.model, s.serial, s.device].filter(Boolean).join(" · ")}</div>}
            {s.note && <div className="sub">{s.note}</div>}
            {s.attributes.length > 0 && (
              <table className="t">
                <thead>
                  <tr>
                    <th>ID</th>
                    <th>Attribute</th>
                    <th>Value</th>
                    <th>Worst</th>
                    <th>Thresh</th>
                    <th>Raw</th>
                  </tr>
                </thead>
                <tbody>
                  {s.attributes.map((a) => (
                    <tr key={a.name} className={a.failed ? "bad" : ""}>
                      <td>{a.id ?? ""}</td>
                      <td>{a.name}</td>
                      <td>{a.value}</td>
                      <td>{a.worst ?? ""}</td>
                      <td>{a.thresh ?? ""}</td>
                      <td>{a.raw ?? ""}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
            <TempTrend points={(hist.data ?? []).filter((p) => p.temperature_c != null).map((p) => ({ at: p.at, c: p.temperature_c! }))} />
            <div className="sub">Checked {new Date(s.checked_at).toLocaleString()}</div>
          </>
        )}
      </div>
      <div className="card" style={{ gridColumn: "1 / -1" }}>
        <h2>{t("dd.recentEvents")}</h2>
        <table className="t">
          <tbody>
            {(events.data ?? [])
              .filter((e) => e.disk_id === d.disk_id)
              .slice(0, 20)
              .map((e) => (
                <tr key={e.event_id}>
                  <td className="sub" style={{ width: 200 }}>
                    {new Date(e.ts).toLocaleString()}
                  </td>
                  <td>{e.kind}</td>
                  <td className="mono sub">{e.job_id}</td>
                </tr>
              ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function BenchTab({ d }: { d: Disk }) {
  const t = useT();
  const b = useBytes();
  const toast = useToast();
  const hist = useLoad(() => api.benchHistory(d.disk_id), [d.disk_id]);
  const [size, setSize] = useState(256);
  const [busy, setBusy] = useState(false);
  const run = () => {
    setBusy(true);
    api
      .bench(d.disk_id, size << 20)
      .then(() => (toast(t("dd.benchFinished")), hist.reload()))
      .catch((e: ApiError) => toast(e.message, "error"))
      .finally(() => setBusy(false));
  };
  return (
    <div className="stack">
      <div className="card row">
        <label className="field">
          {t("dd.testSize")}
          <select className="input" value={size} onChange={(e) => setSize(Number(e.target.value))}>
            {[64, 256, 1024].map((m) => (
              <option key={m} value={m}>
                {m >= 1024 ? `${m / 1024} GiB` : `${m} MiB`}
              </option>
            ))}
          </select>
        </label>
        <span className="spacer sub">{t("dd.benchNote")}</span>
        <button className="btn primary" disabled={busy || d.state !== "ONLINE"} onClick={run}>
          {busy ? t("dd.running") : t("dd.runBench")}
        </button>
      </div>
      <div className="card">
        <table className="t" data-testid="bench-table">
          <thead>
            <tr>
              <th>{t("dd.benchWhen")}</th>
              <th>{t("dd.benchFile")}</th>
              <th>{t("dd.seqWrite")}</th>
              <th>{t("dd.seqRead")}</th>
              <th>{t("dd.randRead")}</th>
            </tr>
          </thead>
          <tbody>
            {(hist.data ?? []).map((r) => (
              <tr key={r.at + r.size_bytes}>
                <td className="sub">{new Date(r.at).toLocaleString()}</td>
                <td>{b(r.size_bytes)}</td>
                <td>{r.seq_write_mb_s.toFixed(0)} MB/s</td>
                <td>
                  {r.seq_read_mb_s.toFixed(0)} MB/s{r.cache_bypass ? "" : " *"}
                </td>
                <td>{r.rand_read_4k_iops.toFixed(0)} IOPS</td>
              </tr>
            ))}
          </tbody>
        </table>
        {hist.data?.length === 0 && <div className="empty">{t("dd.benchEmpty")}</div>}
        {hist.data?.some((r) => !r.cache_bypass) && <div className="sub">{t("dd.cacheNote")}</div>}
      </div>
    </div>
  );
}

function Treemap({ node, total }: { node: UsageNode; total: number }) {
  const b = useBytes();
  const kids = node.children ?? [];
  if (kids.length === 0) return null;
  return (
    <div className="treemap" role="list" aria-label="Folder sizes">
      {kids.slice(0, 12).map((c, i) => (
        <div key={c.path} role="listitem" className={`tm tm${i % 6}`} style={{ flexGrow: c.bytes || 1 }} title={`${c.path} · ${b(c.bytes)}`}>
          <b className="truncate">{c.name}</b>
          <span>{b(c.bytes)}</span>
          <span>{total ? Math.round((c.bytes / total) * 100) : 0}%</span>
        </div>
      ))}
    </div>
  );
}

function UsageTab({ d }: { d: Disk }) {
  const t = useT();
  const b = useBytes();
  const { data: u, error, reload } = useLoad(() => api.diskUsage(d.disk_id), [d.disk_id]);
  if (error) return <ErrorState error={error} onRetry={reload} />;
  if (!u) return <div className="sub">{t("common.loading")}</div>;
  return (
    <div className="stack">
      <div className="card stack">
        <div className="row">
          <h2 className="spacer">{t("dd.usageFolders")}</h2>
          <span className="sub">
            {t("dd.usageNote", { n: b(u.bytes), m: u.files })}
          </span>
        </div>
        <Treemap node={u.tree} total={u.bytes} />
        {u.files === 0 && <div className="empty">{t("dd.noFiles")}</div>}
      </div>
      <div className="grid2">
        <div className="card">
          <h2>{t("dd.largestFiles")}</h2>
          <table className="t">
            <tbody>
              {u.top_files.map((f) => (
                <tr key={f.logical_path}>
                  <td style={{ wordBreak: "break-all" }}>{f.logical_path}</td>
                  <td style={{ textAlign: "right" }}>{b(f.size)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div className="card">
          <h2>{t("dd.fileTypes")}</h2>
          <table className="t">
            <tbody>
              {u.types.slice(0, 15).map((ft) => (
                <tr key={ft.ext}>
                  <td>{ft.ext}</td>
                  <td className="sub">{ft.files} {t("job.files")}</td>
                  <td style={{ textAlign: "right" }}>{b(ft.bytes)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}

function DupesTab({ d }: { d: Disk }) {
  const t = useT();
  const b = useBytes();
  const toast = useToast();
  const [run, setRun] = useState(0);
  const { data, error, reload } = useLoad(() => (run ? api.diskDupes(d.disk_id) : Promise.resolve(null)), [d.disk_id, run]);
  const volume = d.volume_id;
  return (
    <div className="stack">
      <div className="card row">
        <span className="spacer sub">{t("dd.dupesNote")}</span>
        <button className="btn primary" disabled={!volume} onClick={() => setRun((n) => n + 1)}>
          {t("dd.findDupes")}
        </button>
      </div>
      {error && <ErrorState error={error} onRetry={reload} />}
      {data && (
        <div className="card stack" data-testid="dupes">
          <div className="sub">
            {t("dd.dupesResult", { g: data.groups.length, r: b(data.wasted_bytes), s: data.files_scanned, h: data.files_hashed })}
          </div>
          {data.groups.map((g) => (
            <div key={g.checksum} className="dupe">
              <div className="row">
                <b className="spacer">
                  {b(g.size)} × {g.files.length}
                </b>
                <span className="mono sub">{g.checksum.slice(0, 12)}</span>
              </div>
              {g.files.map((f, i) => (
                <div key={f.logical_path + f.disk} className="row">
                  <span className="spacer truncate">
                    {f.logical_path} <span className="sub">· {f.disk} #{f.ordinal}</span>
                  </span>
                  {i > 0 && volume && (
                    <button
                      className="btn sm"
                      onClick={() =>
                        api
                          .remove(volume, f.logical_path, false, false)
                          .then(() => (toast(t("dd.movedToTrash", { n: f.logical_path })), setRun((n) => n + 1)))
                          .catch((e: ApiError) => toast(e.message, "error"))
                      }
                    >
                      {t("vd.moveTrash")}
                    </button>
                  )}
                </div>
              ))}
            </div>
          ))}
          {data.groups.length === 0 && <div className="empty">{t("dd.noDupes")}</div>}
        </div>
      )}
    </div>
  );
}

// Temperature trend (FR-D13) from hourly SMART snapshots.
function TempTrend({ points }: { points: { at: string; c: number }[] }) {
  const t = useT();
  if (points.length < 2) return <div className="sub">{t("dd.tempNone")}</div>;
  const w = 320,
    h = 60;
  const lo = Math.min(...points.map((p) => p.c)) - 2,
    hi = Math.max(...points.map((p) => p.c)) + 2;
  const xy = points.map((p, i) => `${((i / (points.length - 1)) * w).toFixed(1)},${(h - ((p.c - lo) / (hi - lo)) * h).toFixed(1)}`).join(" ");
  const last = points[points.length - 1]!;
  return (
    <figure className="stack" style={{ gap: 2, margin: 0 }} data-testid="temp-trend">
      <svg viewBox={`0 0 ${w} ${h}`} width="100%" height={h} role="img" aria-label={t("dd.tempTrend")}>
        <polyline points={xy} fill="none" stroke="var(--color-primary)" strokeWidth="2" />
      </svg>
      <figcaption className="sub">
        {t("dd.tempTrend")}: {Math.min(...points.map((p) => p.c))}–{Math.max(...points.map((p) => p.c))} °C · {t("dd.tempNow")} {last.c} °C · {points.length}
      </figcaption>
    </figure>
  );
}
