// CTM-VOL-020/022 Create Volume — 3-column DnD (Available storage · Drop zone · ordered Volume list),
// insert slots, Volume configuration form, 3-step stepper (FR-V01~V18, U01~U34, PRD §8.2–8.3).
import {
  DndContext,
  DragOverlay,
  KeyboardSensor,
  PointerSensor,
  closestCenter,
  useDraggable,
  useDroppable,
  useSensor,
  useSensors,
  type Announcements,
  type DragEndEvent,
  type DragStartEvent,
} from "@dnd-kit/core";
import { SortableContext, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { useMemo, useState } from "react";
import { ApiError, api, type Disk } from "../api";
import { navigate } from "../nav";
import { useT } from "../i18n";
import { defaultMountPath, insertAt, parseSize, portableInOrder, slugify, summarize } from "../logic";
import { Dialog, DiskFree, DiskUsage, Empty, ErrorState, OsIcon, RoleBadge, Status, useBytes, useLoad, useToast } from "../ui";

// POLICIES built inside component to use t() — see usePolicies() below


function AvailableCard({ d, onAdd }: { d: Disk; onAdd: () => void }) {
  const t = useT();
  const offline = d.state !== "ONLINE";
  const { attributes, listeners, setNodeRef, isDragging } = useDraggable({ id: `avail:${d.disk_id}`, disabled: offline });
  return (
    <div ref={setNodeRef} className={`disk-card ${offline ? "offline" : ""} ${isDragging ? "dragging" : ""}`} data-testid={`avail-${d.name}`}>
      <OsIcon d={d} />
      <div style={{ minWidth: 0 }}>
        <div className="title">
          {d.node_name ? `${d.node_name.split(".")[0]} · ` : ""}
          {d.name}
        </div>
        <div className="row" style={{ margin: "4px 0" }}>
          <span className="badge">{d.filesystem_type || d.provider_type}</span>
          <RoleBadge role={d.role} />
        </div>
        <DiskUsage d={d} />
        <div className="row" style={{ marginTop: 4 }}>
          <DiskFree d={d} />
          <span className="spacer" />
          <Status state={d.state} label={d.state === "ONLINE" ? t("common.online") : d.state} />
        </div>
      </div>
      <div className="stack" style={{ gap: 4 }}>
        <button className="handle" aria-label={`Drag ${d.name}`} {...listeners} {...attributes}>
          ⋮⋮
        </button>
        <button className="btn sm" aria-label={`Add ${d.name} to volume`} onClick={onAdd} disabled={offline}>
          +
        </button>
      </div>
    </div>
  );
}

function OrderCard({ d, index, total, onMove, onRemove }: { d: Disk; index: number; total: number; onMove: (to: number) => void; onRemove: () => void }) {
  const t = useT();
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: d.disk_id });
  const style = { transform: CSS.Transform.toString(transform), transition };
  const portable = d.role === "portable" || d.role === "import";
  return (
    <div ref={setNodeRef} style={style} className={`disk-card ${isDragging ? "dragging" : ""}`} data-testid={`order-${index + 1}`}>
      <span className="ord" aria-label={`ordinal ${index + 1}`}>
        {index + 1}
      </span>
      <div style={{ minWidth: 0 }}>
        <div className="title">
          {d.name} {portable && <span title={t("cv.excludedTitle")}>⚠</span>}
        </div>
        <div className="sub">{t("cv.diskNode", { n: d.node_name ?? "" })}</div>
        <div className="row">
          <DiskFree d={d} />
          <span className="spacer" />
          <RoleBadge role={d.role} />
        </div>
        <div className="row" style={{ marginTop: 4 }}>
          <Status state={d.state} label={d.state === "ONLINE" ? t("common.healthy") : d.state} />
          <span className="spacer" />
          <button className="btn sm" aria-label={`Move ${d.name} up`} disabled={index === 0} onClick={() => onMove(index - 1)}>
            ↑
          </button>
          <button className="btn sm" aria-label={`Move ${d.name} down`} disabled={index === total - 1} onClick={() => onMove(index + 1)}>
            ↓
          </button>
          <button className="btn sm" aria-label={`Remove ${d.name}`} onClick={onRemove}>
            ✕
          </button>
        </div>
      </div>
      <button className="handle" aria-label={`Reorder ${d.name}`} {...listeners} {...attributes}>
        ⋮⋮
      </button>
    </div>
  );
}

function Slot({ index, active }: { index: number; active: boolean }) {
  const t = useT();
  const { setNodeRef, isOver } = useDroppable({ id: `slot:${index}` });
  return (
    <div ref={setNodeRef} className={`slot ${active ? "active" : ""} ${isOver ? "over" : ""}`} aria-hidden={!active}>
      {active ? t("cv.insert") : ""}
    </div>
  );
}

function DropZone({ big, empty, children }: { big?: boolean; empty: boolean; children?: React.ReactNode }) {
  const t = useT();
  const { setNodeRef, isOver } = useDroppable({ id: "dropzone" });
  return (
    <div ref={setNodeRef} className={`dropzone ${big ? "big" : ""} ${isOver ? "over" : ""}`} data-testid="dropzone">
      {children ?? (
        <div>
          <div style={{ fontSize: 28 }} aria-hidden="true">
            ⊕
          </div>
          <b>{t("cv.dropTitle")}</b>
          <div>{empty ? t("cv.dropSub") : t("cv.dropAppend")}</div>
        </div>
      )}
    </div>
  );
}

export function CreateVolume() {
  const t = useT();
  const b = useBytes();
  const POLICIES = [
    { v: "fill-in-order", label: t("cv.policyFillOrder") },
    { v: "topdir-affinity", label: t("cv.policyTopdir") },
    { v: "whole-tree-only", label: t("cv.policyWholeTree") },
    { v: "most-free", label: t("cv.policyMostFree") },
    { v: "balanced", label: t("cv.policyBalanced") },
  ];
  const toast = useToast();
  const { data: disks, error, reload } = useLoad(() => api.disks("?unassigned=1"), []);
  const [order, setOrder] = useState<string[]>([]);
  const [name, setName] = useState("Research Pool");
  const [mount, setMount] = useState(() => defaultMountPath(slugify("Research Pool")));
  const [mountTouched, setMountTouched] = useState(false);
  const [policy, setPolicy] = useState("fill-in-order");
  const [minFree, setMinFree] = useState("100 GB");
  const [spill, setSpill] = useState(true);
  const [step, setStep] = useState(1);
  const [dragging, setDragging] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [warnPortable, setWarnPortable] = useState(false);

  const byId = useMemo(() => new Map((disks ?? []).map((d) => [d.disk_id, d])), [disks]);
  const inVolume = order.map((id) => byId.get(id)).filter(Boolean) as Disk[];
  const available = (disks ?? []).filter((d) => !order.includes(d.disk_id));
  const minFreeBytes = parseSize(minFree);
  const summary = summarize(inVolume, minFreeBytes ?? 0);
  const portable = portableInOrder(inVolume);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }), // 5 px before drag starts (§8.3-1)
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }), // Space pick · ↑↓ move · Space/Enter drop · Esc cancel
  );
  const nameOf = (id: string | number) => byId.get(String(id).replace(/^avail:/, ""))?.name ?? String(id);
  const announcements: Announcements = {
    onDragStart: ({ active }) => `Picked up disk ${nameOf(active.id)}.`,
    onDragOver: ({ active, over }) => (over ? `Disk ${nameOf(active.id)} is over ${String(over.id).startsWith("slot:") ? "insert slot " + (Number(String(over.id).slice(5)) + 1) : nameOf(over.id)}.` : ""),
    onDragEnd: ({ active, over }) => (over ? `Disk ${nameOf(active.id)} dropped.` : `Disk ${nameOf(active.id)} returned.`),
    onDragCancel: ({ active }) => `Dragging ${nameOf(active.id)} cancelled.`,
  };

  const place = (id: string, index: number) => {
    setOrder((o) => insertAt(o, id, index));
    if (step === 1) setStep(2);
  };

  const onDragStart = (e: DragStartEvent) => setDragging(String(e.active.id));
  const onDragEnd = (e: DragEndEvent) => {
    setDragging(null);
    const a = String(e.active.id);
    const over = e.over ? String(e.over.id) : null;
    if (!over) return;
    const id = a.replace(/^avail:/, "");
    if (over === "available") {
      setOrder((o) => o.filter((x) => x !== id)); // drag back = remove from volume
      return;
    }
    if (over === "dropzone") return place(id, order.length);
    if (over.startsWith("slot:")) {
      let idx = Number(over.slice(5));
      const from = order.indexOf(id);
      if (from >= 0 && from < idx) idx -= 1; // moving down: account for removal
      return place(id, idx);
    }
    const target = order.indexOf(over);
    if (target >= 0) return place(id, target);
  };

  const { setNodeRef: availRef } = useDroppable({ id: "available" });

  const validate = (): string | null => {
    const n = name.trim();
    if (n.length < 1 || n.length > 64) return t("cv.errName");
    if (inVolume.length === 0) return t("cv.errNoDisk");
    if (minFreeBytes === null) return t("cv.errMinFree");
    if (inVolume.some((d) => d.state !== "ONLINE")) return t("cv.errOffline");
    return null;
  };

  const save = async (confirmedPortable = false) => {
    const v = validate();
    setErr(v);
    if (v) return;
    if (portable.length > 0 && !confirmedPortable) {
      setWarnPortable(true);
      return;
    }
    setSaving(true);
    try {
      const vol = await api.createVolume({
        name: name.trim(),
        mount_path: mount,
        placement_policy: policy,
        min_free_bytes: minFreeBytes!,
        allow_spillover: spill,
        disk_ids: order,
      });
      toast(t("cv.volumeSaved", { n: vol.name }));
      navigate(`/volumes/${vol.slug}`);
    } catch (e) {
      const ae = e as ApiError;
      setErr(ae.code === "conflict" ? t("cv.nameInUse") : ae.message);
    } finally {
      setSaving(false);
    }
  };

  if (error) return <ErrorState error={error} onRetry={reload} />;
  const dragDisk = dragging ? byId.get(dragging.replace(/^avail:/, "")) : undefined;
  const steps = [t("cv.step1"), t("cv.step2"), t("cv.step3")];

  return (
    <>
      <h1>{t("cv.title")}</h1>
      <div className="sr-only" aria-live="polite">
        {inVolume.map((d, i) => `${i + 1}: ${d.name}`).join(", ")}
      </div>
      {step < 3 ? (
        <DndContext sensors={sensors} collisionDetection={closestCenter} onDragStart={onDragStart} onDragEnd={onDragEnd} onDragCancel={() => setDragging(null)} accessibility={{ announcements }}>
          <div className="cv-grid">
            <section className="card stack" ref={availRef} aria-labelledby="cv-avail">
              <div>
                <h2 id="cv-avail">{t("cv.available")}</h2>
                <div className="sub">{t("cv.availableSub")}</div>
              </div>
              {disks && disks.length === 0 && <Empty text={t("cv.noDisks")} action={<a className="btn primary" href="#/nodes">+ Attach storage</a>} />}
              {available.map((d) => (
                <AvailableCard key={d.disk_id} d={d} onAdd={() => place(d.disk_id, order.length)} />
              ))}
            </section>
            <DropZone big empty={order.length === 0} />
            <section className="card stack" aria-labelledby="cv-vol" style={{ gap: 8 }}>
              <div>
                <h2 id="cv-vol">
                  {t("cv.volume")}: {name || "—"}
                </h2>
                <div className="sub">{t("cv.volumeSub")}</div>
              </div>
              <SortableContext items={order} strategy={verticalListSortingStrategy}>
                <Slot index={0} active={!!dragging} />
                {inVolume.map((d, i) => (
                  <div key={d.disk_id} className="stack" style={{ gap: 8 }}>
                    <OrderCard d={d} index={i} total={inVolume.length} onMove={(to) => place(d.disk_id, to)} onRemove={() => setOrder((o) => o.filter((x) => x !== d.disk_id))} />
                    <Slot index={i + 1} active={!!dragging} />
                  </div>
                ))}
              </SortableContext>
              {order.length === 0 && <DropZone empty />}
              <div className="row" style={{ marginTop: 8 }}>
                <span className="spacer" data-testid="summary">
                  {summary.count} {t("vol.disks")} · {b(summary.usable)} {t("vol.usable")} · {b(summary.free)} {t("vol.free")}
                </span>
                <button className="btn primary" onClick={() => (validate() ? setErr(validate()) : setStep(3))} disabled={order.length === 0}>
                  {t("cv.save")}
                </button>
              </div>
            </section>
          </div>
          <DragOverlay>{dragDisk ? <div className="disk-card dragging">{dragDisk.name}</div> : null}</DragOverlay>
        </DndContext>
      ) : (
        <section className="card">
          <h2>{t("cv.step3")}</h2>
          <table className="t" style={{ marginTop: 8 }}>
            <thead>
              <tr>
                <th>#</th>
                <th>{t("nod.colDisk")}</th>
                <th>{t("cv.colNode")}</th>
                <th>{t("nod.colRole")}</th>
                <th>{t("nod.colFree")}</th>
                <th>{t("cv.colTotal")}</th>
              </tr>
            </thead>
            <tbody>
              {inVolume.map((d, i) => (
                <tr key={d.disk_id}>
                  <td>
                    <span className="ord">{i + 1}</span>
                  </td>
                  <td>
                    {d.name} {(d.role === "portable" || d.role === "import") && "⚠"}
                  </td>
                  <td>{d.node_name}</td>
                  <td>
                    <RoleBadge role={d.role} />
                  </td>
                  <td>{b(d.effective_free_bytes)}</td>
                  <td>{b(d.total_bytes)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className="sub">
            {POLICIES.find((p) => p.v === policy)?.label} · {t("cv.minFree")} {minFree} · {t("cv.spill")} {spill ? t("common.on") : t("common.off")} · {summary.count} {t("vol.disks")} · {b(summary.usable)} {t("vol.usable")} · {b(summary.free)} {t("vol.free")}
          </p>
          {portable.length > 0 && <div className="banner">⚠ {t("cv.portableMsg")}</div>}
          <div className="row">
            <button className="btn" onClick={() => setStep(2)}>
              ← {t("cv.step2")}
            </button>
            <span className="spacer" />
            <button className="btn primary" onClick={() => save()} disabled={saving} data-testid="save-volume">
              {saving ? t("cv.saving") : t("cv.save")}
            </button>
          </div>
        </section>
      )}

      <section className="card section" aria-labelledby="cv-config">
        <h2 id="cv-config" style={{ marginBottom: 12 }}>
          {t("cv.config")}
        </h2>
        <div className="config">
          <label className="field">
            {t("cv.name")}
            <input
              className="input"
              value={name}
              maxLength={64}
              onChange={(e) => {
                setName(e.target.value);
                if (!mountTouched) setMount(defaultMountPath(slugify(e.target.value)));
              }}
            />
          </label>
          <label className="field" title="macOS /Volumes/<slug> · Windows \\controller\<slug> · Linux /mnt/<slug>. SMB access point is Phase 2.">
            {t("cv.mount")} ⓘ
            <input
              className="input mono"
              value={mount}
              onChange={(e) => {
                setMount(e.target.value);
                setMountTouched(true);
              }}
            />
          </label>
          <label className="field">
            {t("cv.policy")}
            <select className="input" value={policy} onChange={(e) => setPolicy(e.target.value)}>
              {POLICIES.map((p) => (
                <option key={p.v} value={p.v}>
                  {p.label}
                </option>
              ))}
            </select>
          </label>
          <label className="field" title="Reserve kept free on every disk (per-disk override in volume settings).">
            {t("cv.minFree")} ⓘ
            <input className="input" value={minFree} onChange={(e) => setMinFree(e.target.value)} aria-invalid={minFreeBytes === null} />
          </label>
          <label className="toggle">
            <input type="checkbox" checked={spill} onChange={(e) => setSpill(e.target.checked)} /> {t("cv.spill")} {spill ? t("common.on") : t("common.off")}
          </label>
        </div>
        {err && (
          <p className="err" role="alert">
            {err}
          </p>
        )}
      </section>

      <nav className="stepper" aria-label="Steps">
        {steps.map((s, i) => (
          <span key={s} className="row">
            {i > 0 && <span className="step-line" />}
            <button
              className={`step ${step === i + 1 ? "current" : step > i + 1 ? "done" : ""}`}
              aria-current={step === i + 1 ? "step" : undefined}
              onClick={() => (i + 1 === 3 ? (validate() ? setErr(validate()) : setStep(3)) : setStep(i + 1))}
            >
              <span className="n">{step > i + 1 ? "✓" : i + 1}</span>
              {s}
            </button>
          </span>
        ))}
      </nav>

      {warnPortable && (
        <Dialog
          title={t("cv.portableTitle")}
          onClose={() => setWarnPortable(false)}
          actions={
            <>
              <button className="btn" onClick={() => setWarnPortable(false)}>
                {t("common.cancel")}
              </button>
              <button
                className="btn primary"
                onClick={() => {
                  setWarnPortable(false);
                  save(true);
                }}
              >
                {t("cv.saveAnyway")}
              </button>
            </>
          }
        >
          <p>{t("cv.portableMsg")}</p>
          <ul>
            {portable.map((d) => (
              <li key={d.disk_id}>
                {d.name} — <RoleBadge role={d.role} />
              </li>
            ))}
          </ul>
        </Dialog>
      )}
    </>
  );
}
