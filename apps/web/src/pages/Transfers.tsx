// CTM-TRF-010 Transfers + CTM-TRF-P01 Copy to Volume + CTM-TRF-P02 Preview Plan (FR-G05, T08, T09, T19, P07).
import { useEffect, useState } from "react";
import { ApiError, api, streamProgress, type CopyRequest, type Job, type Plan, type Progress } from "../api";
import { FolderPicker } from "../FolderPicker";
import { useT } from "../i18n";
import { fmtETA, parseSize } from "../logic";
import { Bar, Dialog, Empty, ErrorState, Status, useBytes, useLoad, useToast } from "../ui";

const ACTIVE = ["QUEUED", "RUNNING"];
const WAITING = ["PAUSED", "PAUSED_NEED_SPACE"];
const COLORS = ["var(--color-primary)", "#0f8b8d", "#e8a317", "#7048e8", "#2f9e44"];

export function Transfers() {
  const t = useT();
  const b = useBytes();
  const toast = useToast();
  const { data, error, reload, setData } = useLoad(() => api.jobs("?limit=100"), [], 10000);
  const [tab, setTab] = useState<"active" | "waiting" | "done">("active");
  const [copy, setCopy] = useState(false);
  useEffect(
    () =>
      streamProgress((p: Progress) => {
        setData((jobs) => {
          if (!jobs) return jobs;
          if (!jobs.some((j) => j.job_id === p.job_id)) {
            reload();
            return jobs;
          }
          return jobs.map((j) => (j.job_id === p.job_id ? { ...j, state: p.state || j.state, progress: p, error: p.reason || j.error } : j));
        });
      }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );
  if (error) return <ErrorState error={error} onRetry={reload} />;
  const jobs = data ?? [];
  const groups = {
    active: jobs.filter((j) => ACTIVE.includes(j.state)),
    waiting: jobs.filter((j) => WAITING.includes(j.state)),
    done: jobs.filter((j) => !ACTIVE.includes(j.state) && !WAITING.includes(j.state)),
  };
  const act = async (j: Job, a: "pause" | "resume" | "cancel" | "retry") => {
    try {
      await api.jobAction(j.job_id, a);
      reload();
    } catch (e) {
      toast((e as Error).message, "error");
    }
  };
  return (
    <>
      <div className="row">
        <h1 className="spacer">{t("nav.transfers")}</h1>
        <button className="btn primary" onClick={() => setCopy(true)} data-testid="new-copy">
          + {t("trf.newCopy")}
        </button>
      </div>
      <div className="tabs" role="tablist">
        {(
          [
            ["active", t("trf.activeTab", { n: groups.active.length })],
            ["waiting", t("trf.queuedTab", { n: groups.waiting.length })],
            ["done", t("trf.completedTab", { n: groups.done.length })],
          ] as const
        ).map(([k, label]) => (
          <button key={k} role="tab" aria-selected={tab === k} onClick={() => setTab(k)}>
            {label}
          </button>
        ))}
      </div>
      {groups[tab].length === 0 ? (
        <Empty text={t("trf.empty")} action={<button className="btn primary" onClick={() => setCopy(true)}>+ {t("trf.newCopy")}</button>} />
      ) : (
        <div className="stack">
          {groups[tab].map((j) => {
            const p = j.progress;
            const pct = p && p.bytes_total ? (p.bytes_done * 100) / p.bytes_total : j.state === "DONE" ? 100 : 0;
            return (
              <div key={j.job_id} className="card stack" style={{ gap: 6 }} data-testid={`job-${j.job_id}`}>
                <div className="row">
                  <b className="spacer">
                    {j.type} <span className="mono sub">{j.source}</span> → <span className="mono">{j.destination}</span>
                  </b>
                  <Status state={j.state} />
                  {j.state === "RUNNING" && (
                    <button className="btn sm" aria-label={t("job.pause")} onClick={() => act(j, "pause")}>
                      ⏸
                    </button>
                  )}
                  {WAITING.includes(j.state) && (
                    <button className="btn sm" aria-label={t("job.resume")} onClick={() => act(j, "resume")}>
                      ▶
                    </button>
                  )}
                  {(ACTIVE.includes(j.state) || WAITING.includes(j.state)) && (
                    <button className="btn sm danger" aria-label={t("job.cancel")} onClick={() => act(j, "cancel")}>
                      ✕
                    </button>
                  )}
                  {j.state === "FAILED" && (
                    <button className="btn sm" onClick={() => act(j, "retry")}>
                      {t("trf.retryFailed")}
                    </button>
                  )}
                </div>
                <Bar pct={pct} large />
                {p && (
                  <div className="sub">
                    {Math.round(pct)}% · {p.files_done + p.files_skipped}/{p.files_total} {t("job.files")} · {b(p.bytes_done)} / {b(p.bytes_total)} · {b(p.rate_bps)}/s · ETA {fmtETA(p.eta_seconds)}
                    {p.files_skipped > 0 && ` · ${p.files_skipped} ${t("job.skipped")}`}
                    {p.files_failed > 0 && ` · ${p.files_failed} ${t("job.failed")}`}
                  </div>
                )}
                {p && Object.keys(p.placements ?? {}).length > 0 && (
                  <div className="row wrap sub">
                    {Object.entries(p.placements).map(([id, pl]) => (
                      <span key={id} className="badge">
                        {pl.name}: {pl.files} {t("job.files")} · {b(pl.bytes)}
                      </span>
                    ))}
                  </div>
                )}
                {j.error && <div className="sub" style={{ color: j.state === "DONE" ? "inherit" : "var(--warn)" }}>{j.error}</div>}
                <a className="sub mono" href={`#/transfers/${j.job_id}`}>
                  {j.job_id} ›
                </a>
              </div>
            );
          })}
        </div>
      )}
      {copy && (
        <CopyDialog
          onClose={() => setCopy(false)}
          onStarted={() => {
            setCopy(false);
            setTab("active");
            reload();
          }}
        />
      )}
    </>
  );
}

export function CopyDialog({ onClose, onStarted, defaultVolume, defaultDest }: { onClose: () => void; onStarted: () => void; defaultVolume?: string; defaultDest?: string }) {
  const t = useT();
  const b = useBytes();
  const toast = useToast();
  const vols = useLoad(() => api.volumes(), []);
  const [source, setSource] = useState("");
  const [picking, setPicking] = useState(false);
  const [volume, setVolume] = useState(defaultVolume ?? "");
  const [dest, setDest] = useState(defaultDest ?? "/");
  const [start, setStart] = useState("");
  const [forced, setForced] = useState("");
  const [policy, setPolicy] = useState("");
  const [spill, setSpill] = useState("");
  const [minFree, setMinFree] = useState("");
  const [verify, setVerify] = useState(true);
  const [onConflict, setOnConflict] = useState("skip");
  const [onInsufficient, setOnInsufficient] = useState("fail");
  const [plan, setPlan] = useState<Plan | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (!volume && vols.data?.[0]) setVolume(vols.data[0].slug);
  }, [vols.data, volume]);
  const req = (): CopyRequest | null => {
    if (!source || !volume) return null;
    const r: CopyRequest = { volume, source, destination: dest || "/", verify: verify ? "blake3" : "none", on_conflict: onConflict, on_insufficient: onInsufficient };
    if (start) r.start_ordinal = Number(start);
    if (forced) r.forced_ordinal = Number(forced);
    if (policy) r.placement_policy = policy;
    if (spill) r.spillover = spill === "next";
    if (minFree) {
      const mf = parseSize(minFree);
      if (mf === null) return null;
      r.min_free_bytes = mf;
    }
    return r;
  };
  const preview = async () => {
    const r = req();
    if (!r) return toast(t("trf.sourceRequired"), "error");
    setBusy(true);
    try {
      setPlan(await api.plan(r));
    } catch (e) {
      toast((e as Error).message, "error");
    } finally {
      setBusy(false);
    }
  };
  const start_ = async () => {
    const r = req();
    if (!r) return;
    setBusy(true);
    try {
      const j = await api.start(r);
      toast(t("trf.jobStarted", { n: j.job_id }));
      onStarted();
    } catch (e) {
      const ae = e as ApiError;
      toast(ae.message, "error");
      if (ae.info?.plan) setPlan(ae.info.plan as Plan);
    } finally {
      setBusy(false);
    }
  };
  const vol = vols.data?.find((v) => v.slug === volume);
  return (
    <Dialog
      title={t("trf.copyTitle")}
      onClose={onClose}
      actions={
        <>
          <button className="btn" onClick={onClose}>
            {t("common.cancel")}
          </button>
          <button className="btn" onClick={preview} disabled={busy} data-testid="preview-plan">
            {t("trf.preview")}
          </button>
          <button className="btn primary" onClick={start_} disabled={busy || !plan || (!plan.sufficient && onInsufficient === "fail")} data-testid="start-copy">
            {t("trf.copy")}
          </button>
        </>
      }
    >
      <div className="stack">
        <label className="field">
          {t("trf.source")}
          <div className="row">
            <input className="input mono spacer" value={source} onChange={(e) => (setSource(e.target.value), setPlan(null))} placeholder="/Users/me/Downloads/research-data" data-testid="copy-source" />
            <button type="button" className="btn" onClick={() => setPicking(true)}>
              {t("common.browse")}
            </button>
          </div>
        </label>
        {picking && <FolderPicker start={source} onClose={() => setPicking(false)} onPick={(p) => (setSource(p), setPlan(null), setPicking(false))} />}
        <div className="row">
          <label className="field" style={{ width: 200 }}>
            {t("trf.destVolume")}
            <select className="input" value={volume} onChange={(e) => (setVolume(e.target.value), setPlan(null))}>
              {(vols.data ?? []).map((v) => (
                <option key={v.slug} value={v.slug}>
                  {v.name}
                </option>
              ))}
            </select>
          </label>
          <label className="field spacer">
            {t("trf.destPath")}
            <input className="input mono" value={dest} onChange={(e) => (setDest(e.target.value), setPlan(null))} data-testid="copy-dest" />
          </label>
        </div>
        <div className="row">
          <label className="field spacer">
            {t("trf.startDisk")}
            <select className="input" value={start} onChange={(e) => (setStart(e.target.value), setPlan(null))}>
              <option value="">{t("trf.startDefault")}</option>
              {vol?.disks.map((d) => (
                <option key={d.disk_id} value={d.ordinal}>
                  {d.ordinal}. {d.name}
                </option>
              ))}
            </select>
          </label>
          <label className="field spacer">
            {t("trf.forceDisk")}
            <select className="input" value={forced} onChange={(e) => (setForced(e.target.value), setPlan(null))}>
              <option value="">{t("trf.auto")}</option>
              {vol?.disks.map((d) => (
                <option key={d.disk_id} value={d.ordinal}>
                  {d.ordinal}. {d.name}
                </option>
              ))}
            </select>
          </label>
          <label className="field spacer">
            {t("trf.folderPolicy")}
            <select className="input" value={policy} onChange={(e) => (setPolicy(e.target.value), setPlan(null))}>
              <option value="">{t("trf.volDefault", { n: vol?.placement_policy ?? "" })}</option>
              <option value="topdir-affinity">{t("trf.keepTopLevel")}</option>
              <option value="fill-in-order">{t("trf.fileSpill")}</option>
              <option value="whole-tree-only">{t("trf.wholeTree")}</option>
            </select>
          </label>
        </div>
        <div className="row">
          <label className="field spacer">
            {t("trf.onInsufficientSpace")}
            <select className="input" value={spill} onChange={(e) => (setSpill(e.target.value), setPlan(null))}>
              <option value="">{t("trf.volDefaultPlain")}</option>
              <option value="next">{t("trf.continueNext")}</option>
              <option value="fail">{t("trf.failStrict")}</option>
            </select>
          </label>
          <label className="field spacer">
            {t("trf.poolFull")}
            <select className="input" value={onInsufficient} onChange={(e) => setOnInsufficient(e.target.value)}>
              <option value="fail">{t("trf.fail")}</option>
              <option value="wait">{t("trf.waitDisk")}</option>
            </select>
          </label>
          <label className="field spacer">
            {t("trf.minFreeOverride")}
            <input className="input" value={minFree} placeholder={vol ? b(vol.min_free_bytes) : ""} onChange={(e) => (setMinFree(e.target.value), setPlan(null))} />
          </label>
          <label className="field spacer">
            {t("trf.onConflict")}
            <select className="input" value={onConflict} onChange={(e) => setOnConflict(e.target.value)}>
              <option value="skip">{t("trf.skip")}</option>
              <option value="rename">{t("trf.keepBothRename")}</option>
              <option value="newer">{t("trf.replaceNewer")}</option>
              <option value="replace">{t("trf.replace")}</option>
            </select>
          </label>
        </div>
        <label className="toggle">
          <input type="checkbox" checked={verify} onChange={(e) => setVerify(e.target.checked)} /> {t("trf.verify")}
        </label>
        {plan && <PlanView plan={plan} />}
      </div>
    </Dialog>
  );
}

export function PlanView({ plan }: { plan: Plan }) {
  const t = useT();
  const b = useBytes();
  const total = Math.max(1, plan.bytes);
  return (
    <div className="card" style={{ background: "var(--color-bg)" }} data-testid="plan-view">
      <div className="row">
        <b className="spacer">
          {t("trf.estimated", { f: plan.files, d: plan.dirs, s: b(plan.bytes) })}
        </b>
        <Status state={plan.sufficient ? "ONLINE" : "FAILED"} label={plan.sufficient ? t("trf.capacitySufficient", { n: fmtETA(plan.eta_seconds) }) : t("trf.insufficient", { n: plan.error ?? "" })} />
      </div>
      <div className="placement-bar" style={{ margin: "8px 0" }} aria-label="Planned placement">
        {plan.placements.map((p, i) => (
          <span key={p.disk_id} title={`${p.name}: ${b(p.bytes)}`} style={{ width: `${(p.bytes * 100) / total}%`, background: COLORS[i % COLORS.length] }} />
        ))}
      </div>
      <table className="t">
        <tbody>
          {plan.placements.map((p, i) => (
            <tr key={p.disk_id}>
              <td>
                <span className="ord" style={{ background: COLORS[i % COLORS.length] }}>
                  {p.ordinal}
                </span>
              </td>
              <td>{p.name}</td>
              <td>{b(p.bytes)}</td>
              <td>{p.files} {t("job.files")}</td>
              <td className="sub">{b(p.remaining_above_reserve)} {t("trf.aboveReserve")}</td>
            </tr>
          ))}
          {plan.excluded.map((e) => (
            <tr key={e.disk_id}>
              <td>
                <span className="ord muted">{e.ordinal}</span>
              </td>
              <td>{e.name}</td>
              <td colSpan={3} className="sub">
                {t("trf.excluded", { n: e.reason })}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {plan.unplaced_files > 0 && (
        <div className="banner danger">
          {t("trf.unplacedMsg", { n: plan.unplaced_files, s: b(plan.unplaced_bytes) })}
        </div>
      )}
      {plan.existing_paths > 0 && <div className="sub">{t("trf.existingPaths", { n: plan.existing_paths })}</div>}
      {plan.warnings.slice(0, 8).map((w, i) => (
        <div key={i} className="sub" style={{ color: "var(--warn)" }}>
          ▲ {w.code} {w.path} {w.fix && `→ ${w.fix}`}
        </div>
      ))}
    </div>
  );
}
