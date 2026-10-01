// CTM-VOL-010 Volumes list — cards with usage bar, ordinal chips, summary (FR-G03, V19, V10).
import { useState } from "react";
import { api } from "../api";
import { useT } from "../i18n";
import { Bar, Empty, ErrorState, useBytes, useLoad } from "../ui";

export function Volumes() {
  const t = useT();
  const b = useBytes();
  const [query, setQuery] = useState("");
  const { data, error, reload } = useLoad(() => api.volumes(), [], 10000);
  const access = useLoad(() => api.access(), [], 15000);
  if (error) return <ErrorState error={error} onRetry={reload} />;
  const vols = (data ?? []).filter((v) => v.name.toLowerCase().includes(query.toLowerCase()));
  return (
    <>
      <div className="row">
        <h1 className="spacer">{t("nav.volumes")}</h1>
        <input className="input" style={{ width: 220 }} placeholder={t("vol.searchPlaceholder")} aria-label={t("vol.search")} value={query} onChange={(e) => setQuery(e.target.value)} />
        <a className="btn primary" href="#/volumes/new">
          + {t("vol.create")}
        </a>
      </div>
      {data && vols.length === 0 ? (
        <Empty
          text={t("vol.empty")}
          action={
            <a className="btn primary" href="#/volumes/new">
              {t("vol.create")}
            </a>
          }
        />
      ) : (
        <div className="grid">
          {vols.map((v) => {
            const used = v.summary.total_bytes ? 100 - (v.summary.free_bytes * 100) / v.summary.total_bytes : 0;
            return (
              <a key={v.volume_id} className="card stack" href={`#/volumes/${v.slug}`} style={{ textDecoration: "none", color: "inherit", gap: 8 }} data-testid={`volume-card-${v.slug}`}>
                <div className="row">
                  <h2 className="spacer">{v.name}</h2>
                  <span className="badge">{v.placement_policy}</span>
                </div>
                <div className="sub mono">{v.mount_path}</div>
                <Bar pct={used} />
                <div className="sub">
                  {v.summary.disk_count} {t("vol.disks")} · {b(v.summary.usable_bytes)} {t("vol.usable")} · {b(v.summary.free_bytes)} {t("vol.free")} · {b(v.summary.stored_bytes)} {t("vol.stored")}
                </div>
                <div className="row wrap">
                  {v.disks.map((d) => (
                    <span key={d.disk_id} className={`chip ${d.state === "ONLINE" ? "" : "off"}`} title={`${d.ordinal}. ${d.name} (${d.state})`}>
                      {d.ordinal}
                    </span>
                  ))}
                </div>
                {access.data?.enabled &&
                  access.data.shares
                    .filter((s) => s.kind === "volume" && s.volume === v.slug)
                    .map((s) => (
                      <div key={s.name} className="sub mono" data-testid={`access-${v.slug}`}>
                        <span className={`dot ${access.data!.running ? "ok" : "danger"}`} /> {s.url} · {access.data!.running ? t("vol.mounted") : t("vol.stopped")} · {t("vol.clients", { n: access.data!.clients })}
                      </div>
                    ))}
                {v.warnings?.map((w, i) => (
                  <div key={i} className="sub" style={{ color: "var(--warn)" }}>
                    ▲ {w}
                  </div>
                ))}
              </a>
            );
          })}
        </div>
      )}
    </>
  );
}
