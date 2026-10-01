// CTM-ACT-010/020 Activity — event timeline + hash-chained audit log (FR-G06, K06).
import { useState } from "react";
import { api } from "../api";
import { useT } from "../i18n";
import { ErrorState, Status, useLoad } from "../ui";

export function Activity() {
  const t = useT();
  const [tab, setTab] = useState<"events" | "audit">("events");
  const [filter, setFilter] = useState("");
  const events = useLoad(() => api.activity(300), [], 5000);
  const audit = useLoad(() => api.audit(300), [tab]);
  const chain = useLoad(() => api.auditVerify(), [tab]);
  if (events.error) return <ErrorState error={events.error} onRetry={events.reload} />;
  const evs = (events.data ?? []).filter((e) => !filter || e.kind.includes(filter));
  return (
    <>
      <div className="row">
        <h1 className="spacer">{t("nav.activity")}</h1>
        <input className="input" style={{ width: 220 }} placeholder={t("act.filterPlaceholder")} aria-label={t("act.filterLabel")} value={filter} onChange={(e) => setFilter(e.target.value)} />
      </div>
      <div className="tabs" role="tablist">
        <button role="tab" aria-selected={tab === "events"} onClick={() => setTab("events")}>
          {t("act.events")}
        </button>
        <button role="tab" aria-selected={tab === "audit"} onClick={() => setTab("audit")}>
          {t("act.audit")}
        </button>
      </div>
      {tab === "events" ? (
        <div className="card">
          <table className="t">
            <thead>
              <tr>
                <th>{t("act.colTime")}</th>
                <th>{t("act.colKind")}</th>
                <th>{t("act.colObject")}</th>
                <th>{t("act.colDetails")}</th>
              </tr>
            </thead>
            <tbody>
              {evs.map((e) => (
                <tr key={e.event_id}>
                  <td className="sub">{new Date(e.ts).toLocaleString()}</td>
                  <td>{e.kind}</td>
                  <td className="mono sub">{e.job_id || e.disk_id || e.volume_id}</td>
                  <td className="mono sub">
                    <span className="truncate">{e.payload ? JSON.stringify(e.payload) : ""}</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="card">
          <div className="row" style={{ marginBottom: 8 }}>
            {chain.data && <Status state={chain.data.intact ? "ONLINE" : "FAILED"} label={chain.data.intact ? t("act.hashIntact") : t("act.hashBroken", { n: chain.data.first_broken_seq })} />}
          </div>
          <table className="t">
            <thead>
              <tr>
                <th>{t("act.colSeq")}</th>
                <th>{t("act.colTime")}</th>
                <th>{t("act.colActor")}</th>
                <th>{t("act.colAction")}</th>
                <th>{t("act.colTarget")}</th>
                <th>{t("act.colResult")}</th>
                <th>{t("act.colHash")}</th>
              </tr>
            </thead>
            <tbody>
              {(audit.data ?? []).map((a) => (
                <tr key={a.seq}>
                  <td>{a.seq}</td>
                  <td className="sub">{new Date(a.ts).toLocaleString()}</td>
                  <td>{a.actor}</td>
                  <td>{a.action}</td>
                  <td className="mono sub">
                    <span className="truncate">{a.target}</span>
                  </td>
                  <td>{a.result}</td>
                  <td className="mono sub">{a.hash.slice(0, 12)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}
