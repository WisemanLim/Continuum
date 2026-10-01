// CTM-NOD-010 Storage Nodes + CTM-DSK-010/011 Attach Storage wizard (FR-G04, D01~D04, D07).
import { useState } from "react";
import { ApiError, api, type Discovered, type EnrollToken, type Node, type Preflight } from "../api";
import { FolderPicker } from "../FolderPicker";
import { useT } from "../i18n";
import { parseSize } from "../logic";
import { ConfirmDestructive, Dialog, DiskUsage, Empty, ErrorState, RoleBadge, Status, useBytes, useLoad, useToast } from "../ui";

const ROLES = ["always-on", "semi-permanent", "portable", "import", "archive"];

export function Nodes() {
  const t = useT();
  const b = useBytes();
  const toast = useToast();
  const nodes = useLoad(() => api.nodes(), [], 10000);
  const disks = useLoad(() => api.disks(), [], 5000);
  const [attach, setAttach] = useState(false);
  const [token, setToken] = useState<EnrollToken | null>(null);
  const [revoke, setRevoke] = useState<Node | null>(null);
  if (nodes.error) return <ErrorState error={nodes.error} onRetry={nodes.reload} />;
  const setRole = async (id: string, role: string) => {
    try {
      await api.patchDisk(id, { role });
      disks.reload();
    } catch (e) {
      toast((e as Error).message, "error");
    }
  };
  return (
    <>
      <div className="row">
        <h1 className="spacer">{t("nod.title")}</h1>
        <button
          className="btn"
          onClick={() =>
            api
              .nodeToken("ui")
              .then(setToken)
              .catch((e: ApiError) => toast(e.message, "error"))
          }
          data-testid="token-open"
        >
          {t("nod.newToken")}
        </button>
        <button className="btn primary" onClick={() => setAttach(true)} data-testid="attach-open">
          + {t("nod.attach")}
        </button>
      </div>
      <div className="grid" style={{ marginBottom: 24 }}>
        {(nodes.data ?? []).map((n) => (
          <div key={n.node_id} className="card stack" style={{ gap: 4 }}>
            <div className="row">
              <h2 className="spacer">{n.hostname}</h2>
              <Status state={n.state} />
            </div>
            <div className="sub">
              {n.os} {n.os_version} · agent {n.agent_version} · {n.disk_count} {t("vol.disks")}
            </div>
            <div className="sub">{t("nod.heartbeatLabel")} {n.last_heartbeat ? new Date(n.last_heartbeat).toLocaleTimeString() : "—"}</div>
            {n.remote ? (
              <div className="row">
                <span className="sub spacer">
                  {t("nod.remoteAgent")} · {n.arch} · {t("nod.certUntil")} {n.cert_not_after ? new Date(n.cert_not_after).toLocaleDateString() : "—"}
                </span>
                {n.state !== "REVOKED" && (
                  <button className="btn sm danger" onClick={() => setRevoke(n)}>
                    {t("nod.revoke")}
                  </button>
                )}
              </div>
            ) : (
              <div className="sub">{t("nod.nodeNote")}</div>
            )}
          </div>
        ))}
      </div>
      <section className="card">
        <h2 style={{ marginBottom: 8 }}>{t("nod.diskInventory")}</h2>
        {disks.data && disks.data.length === 0 ? (
          <Empty text={t("nod.empty")} action={<button className="btn primary" onClick={() => setAttach(true)}>+ {t("nod.attach")}</button>} />
        ) : (
          <table className="t">
            <thead>
              <tr>
                <th>{t("nod.colDisk")}</th>
                <th>{t("nod.colNode")}</th>
                <th>{t("nod.colVolumeOrd")}</th>
                <th>{t("nod.colFs")}</th>
                <th>{t("nod.colUsage")}</th>
                <th>{t("nod.colFreeTotal")}</th>
                <th>{t("nod.colRole")}</th>
                <th>{t("nod.colState")}</th>
                <th>{t("nod.colPath")}</th>
              </tr>
            </thead>
            <tbody>
              {(disks.data ?? []).map((d) => (
                <tr key={d.disk_id}>
                  <td>
                    <a href={`#/disks/${d.disk_id}`}>
                      <b>{d.name}</b>
                    </a>
                    <div className="sub mono">{d.disk_id}</div>
                  </td>
                  <td className="sub">{d.node_name}</td>
                  <td>{d.volume_id ? <span className="chip">{d.ordinal}</span> : <span className="sub">{t("nod.unassigned")}</span>}</td>
                  <td>{d.filesystem_type}</td>
                  <td style={{ width: 120 }}>
                    <DiskUsage d={d} />
                    <span className="sub">{d.used_pct}%</span>
                  </td>
                  <td>
                    {b(d.effective_free_bytes)} / {b(d.total_bytes)}
                  </td>
                  <td>
                    <select className="input" aria-label={`${t("nod.colRole")} ${d.name}`} value={d.role} onChange={(e) => setRole(d.disk_id, e.target.value)} style={{ width: 150 }}>
                      {ROLES.map((r) => (
                        <option key={r}>{r}</option>
                      ))}
                    </select>
                  </td>
                  <td>
                    <Status state={d.state} />
                  </td>
                  <td className="mono sub">
                    <span className="truncate" title={d.mount_path}>
                      {d.mount_path}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
      {token && (
        <Dialog title={t("nod.tokenTitle")} onClose={() => setToken(null)} actions={<button className="btn primary" onClick={() => setToken(null)}>{t("common.close")}</button>}>
          <div className="stack">
            <div className="sub">{t("nod.tokenHelp", { n: new Date(token.expires_at).toLocaleString() })}</div>
            <pre className="mono codeblock" data-testid="token-command">{token.command}</pre>
            <button className="btn" onClick={() => navigator.clipboard?.writeText(token.command).then(() => toast(t("common.copied")))}>
              {t("common.copy")}
            </button>
            <div className="sub">CA SHA-256 {token.ca_sha256}</div>
          </div>
        </Dialog>
      )}
      {revoke && (
        <ConfirmDestructive
          action={t("nod.revoke")}
          name={revoke.hostname}
          onClose={() => setRevoke(null)}
          onConfirm={() => {
            api
              .revokeNode(revoke.node_id)
              .then(() => (toast(t("nod.revoked", { n: revoke.hostname })), nodes.reload(), disks.reload()))
              .catch((e: ApiError) => toast(e.message, "error"));
            setRevoke(null);
          }}
        />
      )}
      {attach && (
        <AttachWizard
          nodes={nodes.data ?? []}
          onClose={() => setAttach(false)}
          onDone={() => {
            setAttach(false);
            disks.reload();
            nodes.reload();
          }}
        />
      )}
    </>
  );
}

function AttachWizard({ nodes, onClose, onDone }: { nodes: Node[]; onClose: () => void; onDone: () => void }) {
  const t = useT();
  const toast = useToast();
  const b = useBytes();
  const local = nodes.find((n) => !n.remote);
  const [node, setNode] = useState(local?.node_id ?? "");
  const remote = !!nodes.find((n) => n.node_id === node && n.remote);
  const disc = useLoad(() => (remote ? api.inventory(node).then((i) => [...i.roots.map((r) => ({ mount_path: r, volume_label: "", filesystem_type: "folder", bus_type: "", removable: false, network_backed: false, available_bytes: 0 }) as unknown as Discovered), ...i.disks]) : api.discover()), [node], remote ? 3000 : 0);
  const [step, setStep] = useState(1);
  const [path, setPath] = useState("");
  const [browsing, setBrowsing] = useState(false);
  const [name, setName] = useState("");
  const [role, setRole] = useState("always-on");
  const [minFree, setMinFree] = useState("1 GB");
  const [report, setReport] = useState<Preflight | null>(null);
  const [busy, setBusy] = useState(false);
  const selectDisk = (d: Discovered) => {
    setPath(d.mount_path);
    setName(d.volume_label || "");
    if (d.removable) setRole("portable");
    if (d.network_backed) setRole("semi-permanent");
  };
  const check = async () => {
    const mf = parseSize(minFree);
    if (!path || mf === null) return toast(t("nod.pathRequired"), "error");
    setBusy(true);
    try {
      setReport(await api.preflight(path, mf, remote ? node : undefined));
      setStep(2);
    } catch (e) {
      toast((e as Error).message, "error");
    } finally {
      setBusy(false);
    }
  };
  const attach = async () => {
    setBusy(true);
    try {
      const r = await api.attach({ path, name: name || undefined, role, min_free_bytes: parseSize(minFree) ?? undefined, node_id: remote ? node : undefined });
      toast(r.reattached ? t("nod.reattached", { n: r.disk.name }) : t("nod.attached", { n: r.disk.name }));
      onDone();
    } catch (e) {
      const ae = e as ApiError;
      toast(ae.message, "error");
      const pf = ae.info?.preflight as Preflight | undefined;
      if (pf) setReport(pf);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog
      title={t("nod.attachTitle", { n: step })}
      onClose={onClose}
      actions={
        step === 1 ? (
          <>
            <button className="btn" onClick={onClose}>
              {t("common.cancel")}
            </button>
            <button className="btn primary" onClick={check} disabled={busy || !path}>
              {t("nod.nextPreflight")}
            </button>
          </>
        ) : (
          <>
            <button className="btn" onClick={() => setStep(1)}>
              {t("nod.back")}
            </button>
            <button className="btn primary" onClick={attach} disabled={busy || !report?.ok} data-testid="attach-confirm">
              {t("nod.attachBtn")}
            </button>
          </>
        )
      }
    >
      {step === 1 ? (
        <div className="stack">
          {nodes.length > 1 && (
            <label className="field">
              {t("nod.nodeLabel")}
              <select className="input" value={node} onChange={(e) => (setNode(e.target.value), setPath(""))} data-testid="attach-node">
                {nodes
                  .filter((n) => n.state !== "REVOKED")
                  .map((n) => (
                    <option key={n.node_id} value={n.node_id}>
                      {n.hostname} {n.remote ? `(${n.os}, ${n.state})` : ""}
                    </option>
                  ))}
              </select>
            </label>
          )}
          <div className="sub">{remote ? t("nod.remoteNote") : t("nod.phase2Note")}</div>
          <table className="t">
            <thead>
              <tr>
                <th>{t("nod.colMount")}</th>
                <th>{t("nod.colLabel")}</th>
                <th>{t("nod.colFs")}</th>
                <th>{t("nod.colBus")}</th>
                <th>{t("nod.colFree")}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {(disc.data ?? []).map((d) => (
                <tr key={d.mount_path}>
                  <td className="mono">{d.mount_path}</td>
                  <td>{d.volume_label}</td>
                  <td>{d.filesystem_type}</td>
                  <td>
                    {d.bus_type} {d.removable && <span className="badge portable">removable</span>}
                  </td>
                  <td>{b(d.available_bytes)}</td>
                  <td>{d.disk_id ? <span className="sub">{t("nod.registered")}</span> : <button className="btn sm" onClick={() => selectDisk(d)}>{t("nod.use")}</button>}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <label className="field">
            {t("nod.pathLabel")}
            <div className="row">
              <input className="input mono spacer" value={path} onChange={(e) => setPath(e.target.value)} placeholder="/Volumes/ExternalSSD" data-testid="attach-path" />
              {!remote && (
                <button type="button" className="btn" onClick={() => setBrowsing(true)}>
                  {t("common.browse")}
                </button>
              )}
            </div>
          </label>
          {browsing && <FolderPicker start={path} onClose={() => setBrowsing(false)} onPick={(p) => (setPath(p), setBrowsing(false))} />}
          <div className="row">
            <label className="field spacer">
              {t("nod.nameLabel")}
              <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder={t("nod.namePlaceholder")} />
            </label>
            <label className="field spacer">
              {t("nod.roleLabel")}
              <select className="input" value={role} onChange={(e) => setRole(e.target.value)}>
                {ROLES.map((r) => (
                  <option key={r}>{r}</option>
                ))}
              </select>
            </label>
            <label className="field spacer">
              {t("nod.minFreeLabel")}
              <input className="input" value={minFree} onChange={(e) => setMinFree(e.target.value)} data-testid="attach-minfree" />
            </label>
          </div>
          <div className="row">
            <span className="sub">{t("nod.selectedRole")}</span> <RoleBadge role={role} />
          </div>
        </div>
      ) : (
        report && (
          <div>
            <div className="sub mono">{report.data_root}</div>
            <ul className="checklist" data-testid="preflight">
              {report.checks.map((c) => (
                <li key={c.no}>
                  <span aria-label={c.status} style={{ color: c.status === "pass" ? "var(--ok)" : c.status === "warn" ? "var(--warn)" : "var(--danger)" }}>
                    {c.status === "pass" ? "✓" : c.status === "warn" ? "!" : "✗"}
                  </span>
                  <b style={{ width: 110 }}>
                    {c.no}. {c.name}
                  </b>
                  <span className="sub">{c.detail}</span>
                </li>
              ))}
            </ul>
            {!report.ok && <div className="banner danger">{t("nod.preflightFail")}</div>}
            {report.suggest_role && <div className="banner info">{t("nod.suggestRole", { n: report.suggest_role })}</div>}
          </div>
        )
      )}
    </Dialog>
  );
}
