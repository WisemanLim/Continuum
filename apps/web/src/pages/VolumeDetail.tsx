// CTM-VOL-030~034 Volume detail — Disks (order board, Apply order + revision), Files (logical browser),
// Trash, Settings, Activity (FR-V19·V20·K02·T11~T14·T12, FR-G06).
import { DndContext, KeyboardSensor, PointerSensor, closestCenter, useSensor, useSensors, type DragEndEvent } from "@dnd-kit/core";
import { SortableContext, arrayMove, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { useEffect, useState } from "react";
import { ApiError, api, controllerURL, type CatalogObject, type Conflict, type Disk, type ScanReport, type Volume } from "../api";
import { useT } from "../i18n";
import { navigate } from "../nav";
import { parseSize } from "../logic";
import { ConfirmDestructive, Dialog, DiskFree, DiskUsage, ErrorState, RoleBadge, Status, useBytes, useLoad, useToast } from "../ui";
import { CopyDialog } from "./Transfers";
import { RelocateDialog } from "./Relocate";
import { AccessPoint } from "./Access";

const TABS = ["disks", "files", "conflicts", "access", "trash", "settings", "activity"] as const;
type TabKey = "vd.tab.disks" | "vd.tab.files" | "vd.tab.conflicts" | "vd.tab.access" | "vd.tab.trash" | "vd.tab.settings" | "vd.tab.activity";
const TAB_KEYS: TabKey[] = ["vd.tab.disks", "vd.tab.files", "vd.tab.conflicts", "vd.tab.access", "vd.tab.trash", "vd.tab.settings", "vd.tab.activity"];

export function VolumeDetail({ slug, tab }: { slug: string; tab: string }) {
  const t = useT();
  const b = useBytes();
  const { data: v, error, reload, setData } = useLoad(() => api.volume(slug), [slug], 5000);
  if (error) return <ErrorState error={error} onRetry={reload} />;
  if (!v) return <div className="sub">{t("common.loading")}</div>;
  return (
    <>
      <div className="row">
        <h1 className="spacer">{v.name}</h1>
        <span className="sub">
          {v.summary.disk_count} {t("vol.disks")} · {b(v.summary.usable_bytes)} {t("vol.usable")} · {b(v.summary.free_bytes)} {t("vol.free")} · {b(v.summary.stored_bytes)} {t("vol.stored")} · {t("vd.rev")} {v.revision}
        </span>
      </div>
      {v.warnings?.map((w, i) => (
        <div key={i} className="banner">
          ▲ {w}
        </div>
      ))}
      <div className="tabs" role="tablist">
        {TABS.map((tabId, i) => (
          <button key={tabId} role="tab" aria-selected={tab === tabId} onClick={() => navigate(`/volumes/${v.slug}/${tabId}`)}>
            {t(TAB_KEYS[i]!)}
          </button>
        ))}
      </div>
      {tab === "disks" && <DisksBoard v={v} onSaved={(nv) => setData(nv)} reload={reload} />}
      {tab === "files" && <FilesBrowser v={v} />}
      {tab === "conflicts" && <Conflicts v={v} />}
      {tab === "access" && <AccessPoint v={v} onSaved={(nv) => setData(nv)} />}
      {tab === "trash" && <Trash v={v} />}
      {tab === "settings" && <VolumeSettings v={v} onSaved={(nv) => setData(nv)} />}
      {tab === "activity" && <VolumeActivity v={v} />}
    </>
  );
}

function BoardCard({ d, index }: { d: Disk; index: number }) {
  const { attributes, listeners, setNodeRef, transform, transition } = useSortable({ id: d.disk_id });
  return (
    <div ref={setNodeRef} style={{ transform: CSS.Transform.toString(transform), transition }} className={`disk-card ${d.state !== "ONLINE" ? "offline" : ""}`}>
      <span className={`ord ${d.state !== "ONLINE" ? "muted" : ""}`}>{index + 1}</span>
      <div style={{ minWidth: 0 }}>
        <div className="row">
          <a className="title spacer" href={`#/disks/${d.disk_id}`}>
            {d.name}
          </a>
          <RoleBadge role={d.role} />
        </div>
        <div className="sub mono truncate">{d.mount_path}</div>
        <DiskUsage d={d} />
        <div className="row" style={{ marginTop: 4 }}>
          <DiskFree d={d} />
          <span className="spacer" />
          <Status state={d.state} />
        </div>
      </div>
      <button className="handle" aria-label={`Reorder ${d.name}`} {...listeners} {...attributes}>
        ⋮⋮
      </button>
    </div>
  );
}

function DisksBoard({ v, onSaved, reload }: { v: Volume; onSaved: (v: Volume) => void; reload: () => void }) {
  const t = useT();
  const toast = useToast();
  const [order, setOrder] = useState(v.disks.map((d) => d.disk_id));
  const [conflict, setConflict] = useState<string | null>(null);
  const dirty = order.join() !== v.disks.map((d) => d.disk_id).join();
  useEffect(() => {
    if (!dirty) setOrder(v.disks.map((d) => d.disk_id));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [v]);
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }), useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }));
  const byId = new Map(v.disks.map((d) => [d.disk_id, d]));
  const onEnd = (e: DragEndEvent) => {
    if (e.over && e.active.id !== e.over.id) setOrder((o) => arrayMove(o, o.indexOf(String(e.active.id)), o.indexOf(String(e.over!.id))));
  };
  const apply = async () => {
    try {
      const nv = await api.setDisks(v.slug, order, v.revision);
      toast(t("vd.orderApplied"));
      setConflict(null);
      onSaved(nv);
    } catch (e) {
      const ae = e as ApiError;
      if (ae.code === "revision_mismatch") setConflict(t("vd.conflictMsg"));
      else toast(ae.message, "error");
    }
  };
  const detach = async (d: Disk) => {
    try {
      await api.detach(d.disk_id);
      toast(t("vd.detached", { n: d.name }));
      reload();
    } catch (e) {
      toast((e as Error).message, "error");
    }
  };
  const [confirm, setConfirm] = useState<Disk | null>(null);
  const [reloc, setReloc] = useState<string | null>(null);
  return (
    <div className="stack">
      {conflict && (
        <div className="banner" role="alert">
          <span className="spacer">{conflict}</span>
          <button
            className="btn sm"
            onClick={() => {
              setConflict(null);
              setOrder(v.disks.map((d) => d.disk_id));
              reload();
            }}
          >
            {t("common.reload")}
          </button>
        </div>
      )}
      <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={onEnd}>
        <SortableContext items={order} strategy={verticalListSortingStrategy}>
          <div className="stack" style={{ maxWidth: 720 }}>
            {order.map((id, i) => {
              const d = byId.get(id);
              return d ? (
                <div key={id} className="row">
                  <div className="spacer">
                    <BoardCard d={d} index={i} />
                  </div>
                  <div className="stack" style={{ gap: 4 }}>
                    {d.state === "DISABLED" ? (
                      <button className="btn sm" onClick={() => api.enable(d.disk_id).then(reload)}>
                        {t("vd.enable")}
                      </button>
                    ) : (
                      <>
                        <button className="btn sm" disabled={d.state !== "ONLINE"} onClick={() => setReloc(d.disk_id)}>
                          {t("vd.drainBtn")}
                        </button>
                        <button className="btn sm danger" onClick={() => setConfirm(d)}>
                          {t("vd.detachBtn")}
                        </button>
                      </>
                    )}
                  </div>
                </div>
              ) : null;
            })}
          </div>
        </SortableContext>
      </DndContext>
      <div className="row">
        <button className="btn primary" disabled={!dirty} onClick={apply}>
          {t("vd.applyOrder")}
        </button>
        <button className="btn" disabled={!dirty} onClick={() => setOrder(v.disks.map((d) => d.disk_id))}>
          {t("vd.reset")}
        </button>
        <span className="sub spacer">{t("vd.orderNote")}</span>
        <button className="btn" disabled={dirty} onClick={() => setReloc("rebalance")}>
          {t("vd.rebalanceBtn")}
        </button>
      </div>
      {reloc === "rebalance" && (
        <RelocateDialog
          title={t("vd.rebalanceTitle")}
          req={{ type: "REBALANCE", volume: v.slug, source: "/", destination: "/" }}
          note={t("vd.rebalanceNote")}
          onClose={() => setReloc(null)}
          onStarted={() => navigate("/transfers")}
        />
      )}
      {reloc && reloc !== "rebalance" && (
        <RelocateDialog
          title={t("vd.drainTitle")}
          req={{ type: "DRAIN", volume: v.slug, source: reloc, destination: "/" }}
          note={t("vd.drainNote")}
          onClose={() => setReloc(null)}
          onStarted={() => navigate("/transfers")}
        />
      )}
      {confirm && (
        <ConfirmDestructive
          action={t("vd.detachAction")}
          name={confirm.name}
          onClose={() => setConfirm(null)}
          onConfirm={() => {
            detach(confirm);
            setConfirm(null);
          }}
        />
      )}
    </div>
  );
}

function FilesBrowser({ v }: { v: Volume }) {
  const t = useT();
  const b = useBytes();
  const toast = useToast();
  const [path, setPath] = useState("/");
  const { data, error, reload } = useLoad(() => api.files(v.slug, path), [v.slug, path]);
  const [locate, setLocate] = useState<CatalogObject[] | null>(null);
  const [dlg, setDlg] = useState<null | "mkdir" | "copy" | { rename: CatalogObject } | { del: CatalogObject }>(null);
  const [text, setText] = useState("");
  const crumbs = path.split("/").filter(Boolean);
  const run = async (fn: () => Promise<unknown>, ok: string) => {
    try {
      await fn();
      toast(ok);
      setDlg(null);
      reload();
    } catch (e) {
      toast((e as Error).message, "error");
    }
  };
  return (
    <div className="card">
      <div className="row" style={{ marginBottom: 12 }}>
        <nav className="spacer mono" aria-label="Path">
          <a href="#" onClick={(e) => (e.preventDefault(), setPath("/"))}>
            {v.slug}:/
          </a>
          {crumbs.map((c, i) => (
            <span key={i}>
              <a href="#" onClick={(e) => (e.preventDefault(), setPath("/" + crumbs.slice(0, i + 1).join("/")))}>
                {c}
              </a>
              /
            </span>
          ))}
        </nav>
        <button className="btn" onClick={() => (setText(""), setDlg("mkdir"))}>
          {t("vd.newFolder")}
        </button>
        <button className="btn primary" onClick={() => setDlg("copy")}>
          {t("vd.copyHere")}
        </button>
      </div>
      {error && <ErrorState error={error} onRetry={reload} />}
      <table className="t">
        <thead>
          <tr>
            <th>{t("vd.colName")}</th>
            <th>{t("job.size")}</th>
            <th>{t("vd.colDisk")}</th>
            <th>{t("job.state")}</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {path !== "/" && (
            <tr>
              <td colSpan={5}>
                <a href="#" onClick={(e) => (e.preventDefault(), setPath("/" + crumbs.slice(0, -1).join("/")))}>
                  ↰ ..
                </a>
              </td>
            </tr>
          )}
          {(data ?? []).map((o) => (
            <tr key={o.object_id}>
              <td>
                {o.type === "dir" ? (
                  <a href="#" onClick={(e) => (e.preventDefault(), setPath(o.logical_path))}>
                    📁 {o.name}
                  </a>
                ) : (
                  <span>📄 {o.name}</span>
                )}
              </td>
              <td>{o.type === "file" ? b(o.size) : ""}</td>
              <td>{o.ordinal ? <span className="chip">{o.ordinal}</span> : ""} {o.disk_name}</td>
              <td>{o.type === "file" && <Status state={o.state === "ok" ? "ONLINE" : "OFFLINE"} label={o.state} />}</td>
              <td className="row">
                {o.type === "file" && (
                  <button className="btn sm" onClick={() => api.locate(v.slug, o.logical_path).then(setLocate)}>
                    {t("vd.showLocation")}
                  </button>
                )}
                <button className="btn sm" onClick={() => (setText(o.name), setDlg({ rename: o }))}>
                  {t("vd.rename")}
                </button>
                <button className="btn sm danger" onClick={() => setDlg({ del: o })}>
                  {t("vd.delete")}
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {data && data.length === 0 && <div className="empty">{t("vd.emptyFolder")}</div>}
      {locate && (
        <Dialog title={t("vd.physLocation")} onClose={() => setLocate(null)} actions={<button className="btn" onClick={() => setLocate(null)}>{t("common.close")}</button>}>
          {locate.map((o) => (
            <div key={o.object_id} className="stack" style={{ gap: 4, marginBottom: 12 }}>
              <div>
                <span className="chip">{o.ordinal}</span> {o.disk_name}
              </div>
              <code className="mono">{o.physical_path}</code>
              <span className="sub mono">blake3 {o.checksum}</span>
            </div>
          ))}
        </Dialog>
      )}
      {dlg === "mkdir" && (
        <Dialog
          title={t("vd.newFolder")}
          onClose={() => setDlg(null)}
          actions={
            <button className="btn primary" onClick={() => run(() => api.mkdir(v.slug, (path === "/" ? "" : path) + "/" + text), t("vd.folderCreated"))}>
              {t("vd.create")}
            </button>
          }
        >
          <input className="input" value={text} onChange={(e) => setText(e.target.value)} aria-label={t("vd.newFolder")} />
        </Dialog>
      )}
      {dlg && typeof dlg === "object" && "rename" in dlg && (
        <Dialog
          title={t("vd.renameDlg", { n: dlg.rename.name })}
          onClose={() => setDlg(null)}
          actions={
            <button className="btn primary" onClick={() => run(() => api.rename(v.slug, dlg.rename.logical_path, dlg.rename.logical_path.replace(/[^/]+$/, text)), t("vd.renamedToast"))}>
              {t("vd.rename")}
            </button>
          }
        >
          <input className="input" value={text} onChange={(e) => setText(e.target.value)} aria-label={t("vd.colName")} />
        </Dialog>
      )}
      {dlg && typeof dlg === "object" && "del" in dlg && (
        <Dialog
          title={t("vd.deleteDlg", { n: dlg.del.name })}
          onClose={() => setDlg(null)}
          actions={
            <>
              <button className="btn" onClick={() => setDlg(null)}>
                {t("common.cancel")}
              </button>
              <button className="btn danger" onClick={() => run(() => api.remove(v.slug, dlg.del.logical_path, true, false), t("vd.movedTrash"))}>
                {t("vd.moveTrash")}
              </button>
            </>
          }
        >
          <p>
            <b>{dlg.del.logical_path}</b> {t("vd.deleteMsg", { n: dlg.del.logical_path, m: v.trash_retention_days })}
          </p>
        </Dialog>
      )}
      {dlg === "copy" && <CopyDialog defaultVolume={v.slug} defaultDest={path} onClose={() => setDlg(null)} onStarted={() => navigate("/transfers")} />}
    </div>
  );
}

// CTM-VOL-035 Conflict Center — same logical path on several disks (FR-K04·G15·A07) + SCAN (FR-K03).
function Conflicts({ v }: { v: Volume }) {
  const t = useT();
  const b = useBytes();
  const toast = useToast();
  const { data, reload } = useLoad(() => api.conflicts(v.slug), [v.slug]);
  const [rep, setRep] = useState<ScanReport | null>(null);
  const [busy, setBusy] = useState(false);
  const scan = async (full: boolean) => {
    setBusy(true);
    try {
      setRep(await api.scan(v.slug, { full }));
      reload();
    } catch (e) {
      toast((e as ApiError).message, "error");
    } finally {
      setBusy(false);
    }
  };
  const resolve = (c: Conflict, keep: string) =>
    api
      .resolveConflict(c.conflict_id, keep)
      .then(() => (toast(t("cf.resolved", { n: c.logical_path })), reload()))
      .catch((e) => toast(e.message, "error"));
  const dups = (data ?? []).filter((c) => c.kind === "duplicate").length;
  return (
    <div className="stack">
      <div className="card row">
        <span className="spacer sub">{t("cf.lastScan")} {v.last_scan_at ? new Date(v.last_scan_at).toLocaleString() : t("cf.never")}</span>
        <button className="btn" disabled={busy} onClick={() => scan(false)}>
          {t("cf.scan")}
        </button>
        <button className="btn" disabled={busy} onClick={() => scan(true)}>
          {t("cf.fullScan")}
        </button>
        <button
          className="btn"
          disabled={dups === 0}
          onClick={() =>
            api
              .resolveDuplicates(v.slug)
              .then((r) => (toast(t("cf.resolved", { n: String(r.resolved) })), reload()))
              .catch((e) => toast(e.message, "error"))
          }
        >
          {t("cf.resolveDups", { n: dups })}
        </button>
      </div>
      {rep && (
        <div className="banner" role="status" data-testid="scan-report">
          {t("cf.scanReport", { mode: rep.mode, f: rep.files_seen, a: rep.added.length, r: rep.removed.length, m: rep.modified.length, c: rep.corrupt.length, p: rep.partials.length, cf: rep.conflicts_open })}
          {rep.skipped_disks?.length ? t("cf.scanSkipped", { n: rep.skipped_disks.join(", ") }) : ""}
        </div>
      )}
      <div className="card">
        <table className="t">
          <thead>
            <tr>
              <th>{t("job.path")}</th>
              <th>{t("cf.colKind")}</th>
              <th>{t("cf.colCopies")}</th>
              <th>{t("cf.colResolve")}</th>
            </tr>
          </thead>
          <tbody>
            {(data ?? []).map((c) => (
              <tr key={c.conflict_id}>
                <td>{c.logical_path}</td>
                <td>
                  <Status state={c.kind} />
                </td>
                <td className="sub">
                  {c.candidates.map((d, i) => (
                    <div key={d.object_id}>
                      #{d.ordinal} {d.disk} · {b(d.size)} · {new Date(d.mtime_ns / 1e6).toLocaleString()}
                      {i === 0 ? " (visible)" : ""}
                    </div>
                  ))}
                </td>
                <td>
                  <div className="row" style={{ gap: 4, flexWrap: "wrap" }}>
                    <button className="btn sm" onClick={() => resolve(c, "lowest")}>
                      {t("cf.keepLowest")}
                    </button>
                    <button className="btn sm" onClick={() => resolve(c, "newest")}>
                      {t("cf.keepNewest")}
                    </button>
                    <button className="btn sm" onClick={() => resolve(c, "both")}>
                      {t("cf.keepBoth")}
                    </button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {data && data.length === 0 && <div className="empty">{t("cf.empty")}</div>}
        <div className="sub" style={{ marginTop: 8 }}>
          {t("cf.note")}
        </div>
      </div>
    </div>
  );
}

function Trash({ v }: { v: Volume }) {
  const t = useT();
  const b = useBytes();
  const toast = useToast();
  const { data, reload } = useLoad(() => api.trash(v.slug), [v.slug]);
  return (
    <div className="card">
      <table className="t">
        <thead>
          <tr>
            <th>{t("job.path")}</th>
            <th>{t("job.size")}</th>
            <th>{t("vd.trDeleted")}</th>
            <th>{t("vd.trPurge")}</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {(data ?? []).map((tr) => (
            <tr key={tr.tomb_id}>
              <td>{tr.logical_path}</td>
              <td>{b(tr.size)}</td>
              <td className="sub">{new Date(tr.deleted_at).toLocaleString()}</td>
              <td className="sub">{tr.purge_after ? new Date(tr.purge_after).toLocaleDateString() : ""}</td>
              <td>
                <button
                  className="btn sm"
                  onClick={() =>
                    api
                      .restore(tr.tomb_id)
                      .then(() => (toast(t("vd.restoredToast")), reload()))
                      .catch((e) => toast(e.message, "error"))
                  }
                >
                  {t("vd.restore")}
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {data && data.length === 0 && <div className="empty">{t("vd.trEmpty")}</div>}
    </div>
  );
}

function VolumeSettings({ v, onSaved }: { v: Volume; onSaved: (v: Volume) => void }) {
  const t = useT();
  const toast = useToast();
  const b = useBytes();
  const [name, setName] = useState(v.name);
  const [policy, setPolicy] = useState(v.placement_policy);
  const [minFree, setMinFree] = useState(b(v.min_free_bytes));
  const [spill, setSpill] = useState(v.allow_spillover);
  const [orphan, setOrphan] = useState(v.orphan_policy);
  const [scanMin, setScanMin] = useState(String(v.scan_interval_min));
  const [del, setDel] = useState(false);
  const save = async () => {
    const mf = parseSize(minFree);
    if (mf === null) return toast(t("vd.invalidMinFree"), "error");
    try {
      onSaved(await api.updateVolume(v.slug, { expected_revision: v.revision, name, placement_policy: policy, min_free_bytes: mf, allow_spillover: spill,
        orphan_policy: orphan, scan_interval_min: Math.max(1, parseInt(scanMin, 10) || 1) }));
      toast(t("vd.settingsSaved"));
    } catch (e) {
      toast((e as ApiError).message, "error");
    }
  };
  return (
    <div className="card stack" style={{ maxWidth: 720 }}>
      <label className="field">
        {t("cv.name")}
        <input className="input" value={name} onChange={(e) => setName(e.target.value)} />
      </label>
      <label className="field">
        {t("cv.policy")}
        <select className="input" value={policy} onChange={(e) => setPolicy(e.target.value)}>
          {["fill-in-order", "topdir-affinity", "whole-tree-only", "most-free", "balanced"].map((p) => (
            <option key={p}>{p}</option>
          ))}
        </select>
      </label>
      <label className="field">
        {t("cv.minFree")}
        <input className="input" value={minFree} onChange={(e) => setMinFree(e.target.value)} />
      </label>
      <label className="toggle">
        <input type="checkbox" checked={spill} onChange={(e) => setSpill(e.target.checked)} /> {t("vd.allowSpillover")}
      </label>
      <label className="field">
        {t("vd.orphanLabel")}
        <select className="input" value={orphan} onChange={(e) => setOrphan(e.target.value as Volume["orphan_policy"])}>
          <option value="auto">{t("vd.orphanAuto")}</option>
          <option value="review">{t("vd.orphanReview")}</option>
        </select>
      </label>
      <label className="field">
        {t("vd.scanInterval")}
        <input className="input" inputMode="numeric" value={scanMin} onChange={(e) => setScanMin(e.target.value)} />
      </label>
      <div className="row">
        <button className="btn primary" onClick={save}>
          {t("common.save")}
        </button>
        <span className="spacer" />
        <a className="btn" href={`${controllerURL()}/v1/volumes/${encodeURIComponent(v.slug)}/export`} download={`${v.slug}.yaml`}>
          {t("vd.exportYaml")}
        </a>
        <button className="btn danger" onClick={() => setDel(true)}>
          {t("vd.deleteVolume")}
        </button>
      </div>
      {del && (
        <ConfirmDestructive
          action={t("vd.deleteVolumeAction")}
          name={v.slug}
          onClose={() => setDel(false)}
          onConfirm={() =>
            api
              .deleteVolume(v.slug)
              .then(() => (toast(t("vd.volumeDeleted")), navigate("/volumes")))
              .catch((e) => toast(e.message, "error"))
          }
        />
      )}
    </div>
  );
}

function VolumeActivity({ v }: { v: Volume }) {
  const t = useT();
  const { data } = useLoad(() => api.activity(200), [v.slug]);
  const rows = (data ?? []).filter((e) => e.volume_id === v.volume_id);
  return (
    <div className="card">
      <table className="t">
        <tbody>
          {rows.map((e) => (
            <tr key={e.event_id}>
              <td className="sub" style={{ width: 200 }}>
                {new Date(e.ts).toLocaleString()}
              </td>
              <td>{e.kind}</td>
              <td className="mono sub">{e.job_id || e.disk_id}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {rows.length === 0 && <div className="empty">{t("vd.activityEmpty")}</div>}
    </div>
  );
}
