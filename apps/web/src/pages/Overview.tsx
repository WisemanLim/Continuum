// CTM-OVW-010 Overview — 8 tiles + volume cards + warnings + recent activity (FR-G02).
import { api } from "../api";
import { useT } from "../i18n";
import { Bar, Empty, ErrorState, useBytes, useLoad } from "../ui";

export function Overview() {
  const t = useT();
  const b = useBytes();
  const { data, error, reload } = useLoad(() => api.overview(), [], 5000);
  if (error) return <ErrorState error={error} onRetry={reload} />;
  if (!data) return <div className="sub">{t("common.loading")}</div>;
  const k = data.tiles;
  const tiles: [string, string, string][] = [
    [t("ovw.total"), b(k.total_bytes), "#/volumes"],
    [t("ovw.free"), b(k.free_bytes), "#/volumes"],
    [t("ovw.volumes"), String(k.volumes), "#/volumes"],
    [t("ovw.nodes"), t("ovw.nodesOnline", { n: k.nodes_online, m: k.nodes_total }), "#/nodes"],
    [t("ovw.running"), String(k.running_jobs), "#/transfers"],
    [t("ovw.warnings"), String(k.warnings), "#/overview"],
    [t("ovw.offline"), String(k.offline_disks), "#/nodes"],
    [t("ovw.conflicts"), String(k.conflicts), "#/volumes"],
  ];
  return (
    <>
      <h1>{t("nav.overview")}</h1>
      <div className="tiles">
        {tiles.map(([label, v, href]) => (
          <a key={label} className="card tile" href={href} style={{ textDecoration: "none", color: "inherit" }}>
            <div className="sub">{label}</div>
            <div className="v" data-testid={`tile-${label}`}>
              {v}
            </div>
          </a>
        ))}
      </div>
      {data.warnings.length > 0 && (
        <div className="banner" role="alert">
          <div className="stack" style={{ gap: 4 }}>
            {data.warnings.map((w, i) => (
              <span key={i}>▲ {w}</span>
            ))}
          </div>
        </div>
      )}
      {data.volumes.length === 0 ? (
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
          {data.volumes.map((v) => {
            const used = v.summary.usable_bytes ? 100 - (v.summary.free_bytes * 100) / Math.max(1, v.summary.total_bytes) : 0;
            return (
              <a key={v.volume_id} className="card" href={`#/volumes/${v.slug}`} style={{ textDecoration: "none", color: "inherit" }}>
                <h2>{v.name}</h2>
                <div className="sub" style={{ marginBottom: 8 }}>
                  {v.summary.disk_count} {t("vol.disks")} · {b(v.summary.usable_bytes)} {t("vol.usable")} · {b(v.summary.free_bytes)} {t("vol.free")}
                </div>
                <Bar pct={used} />
              </a>
            );
          })}
        </div>
      )}
      <section className="section card">
        <h2>{t("ovw.recent")}</h2>
        <table className="t">
          <tbody>
            {data.recent.map((e) => (
              <tr key={e.event_id}>
                <td className="sub" style={{ width: 200 }}>
                  {new Date(e.ts).toLocaleString()}
                </td>
                <td>{e.kind}</td>
                <td className="mono sub">{e.job_id || e.disk_id || e.volume_id}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
    </>
  );
}
