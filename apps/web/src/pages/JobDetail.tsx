// CTM-TRF-020 Job detail — progress, per-disk placement, items with state filter, errors CSV (FR-T09, T21).
import { useEffect, useState } from "react";
import { ApiError, api, jobErrorsCSV, streamProgress, type Job } from "../api";
import { useT } from "../i18n";
import { fmtETA } from "../logic";
import { Bar, ErrorState, Status, useBytes, useLoad, useToast } from "../ui";

const FILTERS = ["", "failed", "unplaced", "skipped", "pending", "done"] as const;
const WAITING = ["PAUSED", "PAUSED_NEED_SPACE"];

export function JobDetail({ id }: { id: string }) {
  const t = useT();
  const b = useBytes();
  const toast = useToast();
  const { data: j, error, reload, setData } = useLoad(() => api.job(id), [id], 10000);
  const [filter, setFilter] = useState<(typeof FILTERS)[number]>("");
  const items = useLoad(() => api.jobItems(id, filter), [id, filter]);
  useEffect(
    () =>
      streamProgress((p) => {
        if (p.job_id !== id) return;
        setData((cur) => (cur ? ({ ...cur, state: p.state || cur.state, progress: p, error: p.reason || cur.error } as Job) : cur));
        if (["DONE", "FAILED", "CANCELLED"].includes(p.state)) items.reload();
      }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [id],
  );
  if (error) return <ErrorState error={error} onRetry={reload} />;
  if (!j) return <div className="sub">{t("common.loading")}</div>;
  const p = j.progress;
  const pct = p && p.bytes_total ? (p.bytes_done * 100) / p.bytes_total : j.state === "DONE" ? 100 : 0;
  const act = (a: "pause" | "resume" | "cancel" | "retry") =>
    api
      .jobAction(j.job_id, a)
      .then((nj) => (setData(nj), items.reload()))
      .catch((e: ApiError) => toast(e.message, "error"));
  const when = (s?: string) => (s ? new Date(s).toLocaleString() : "—");
  return (
    <div className="stack">
      <div className="row">
        <h1 className="spacer">
          {j.type} <span className="mono sub">{j.job_id}</span>
        </h1>
        <Status state={j.state} />
        {j.state === "RUNNING" && (
          <button className="btn" onClick={() => act("pause")}>
            {t("job.pause")}
          </button>
        )}
        {WAITING.includes(j.state) && (
          <button className="btn" onClick={() => act("resume")}>
            {t("job.resume")}
          </button>
        )}
        {["QUEUED", "RUNNING", ...WAITING].includes(j.state) && (
          <button className="btn danger" onClick={() => act("cancel")}>
            {t("job.cancel")}
          </button>
        )}
        {j.state === "FAILED" && (
          <button className="btn" onClick={() => act("retry")}>
            {t("job.retry")}
          </button>
        )}
      </div>
      <div className="card stack">
        <div className="mono">
          {j.source} → {j.destination}
        </div>
        <Bar pct={pct} large />
        {p && (
          <div className="sub" data-testid="job-progress">
            {Math.round(pct)}% · {p.files_done + p.files_skipped}/{p.files_total} {t("job.files")} · {b(p.bytes_done)} / {b(p.bytes_total)} · {b(p.rate_bps)}/s · ETA{" "}
            {fmtETA(p.eta_seconds)}
            {p.files_skipped > 0 && ` · ${p.files_skipped} ${t("job.skipped")}`}
            {p.files_failed > 0 && ` · ${p.files_failed} ${t("job.failed")}`}
          </div>
        )}
        {j.error && <div className="banner">{j.error}</div>}
        <div className="row wrap sub">
          <span>
            {t("job.created")} {when(j.created_at)}
          </span>
          <span>
            {t("job.started")} {when(j.started_at)}
          </span>
          <span>
            {t("job.finished")} {when(j.finished_at)}
          </span>
        </div>
        {p && Object.keys(p.placements ?? {}).length > 0 && (
          <div className="row wrap">
            {Object.entries(p.placements).map(([d, pl]) => (
              <span key={d} className="badge">
                {pl.name}: {pl.files} {t("job.files")} · {b(pl.bytes)}
              </span>
            ))}
          </div>
        )}
      </div>
      <div className="card stack">
        <div className="row">
          <h2 className="spacer">{t("job.items")}</h2>
          <select className="input" style={{ width: 160 }} aria-label={t("job.filter")} value={filter} onChange={(e) => setFilter(e.target.value as (typeof FILTERS)[number])}>
            {FILTERS.map((f) => (
              <option key={f} value={f}>
                {f ? f : t("job.all")}
              </option>
            ))}
          </select>
          {j.errors_count > 0 && (
            <a className="btn" href={jobErrorsCSV(j.job_id)} download={`${j.job_id}-errors.csv`}>
              {t("job.errorsCsv")} ({j.errors_count})
            </a>
          )}
        </div>
        <table className="t" data-testid="job-items">
          <thead>
            <tr>
              <th>#</th>
              <th>{t("job.path")}</th>
              <th>{t("job.size")}</th>
              <th>{t("job.state")}</th>
              <th>{t("job.error")}</th>
            </tr>
          </thead>
          <tbody>
            {(items.data ?? []).slice(0, 500).map((it) => (
              <tr key={it.item_no}>
                <td className="sub">{it.item_no}</td>
                <td style={{ wordBreak: "break-all" }}>{it.logical_path}</td>
                <td>{b(it.size)}</td>
                <td>{it.state}</td>
                <td className="sub">{it.error}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {items.data && items.data.length > 500 && <div className="sub">{t("job.more", { n: items.data.length - 500 })}</div>}
        {items.data?.length === 0 && <div className="empty">{t("job.noItems")}</div>}
      </div>
    </div>
  );
}
