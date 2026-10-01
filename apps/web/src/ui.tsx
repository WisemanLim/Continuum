// Shared UI primitives: badges, status (dot + text + icon, never colour only), bars, dialogs, toasts.
import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import type { Disk } from "./api";
import { barLevel, fmtBytes, osOf, usedPct, type Units } from "./logic";
import { useT } from "./i18n";

export const UnitsContext = createContext<Units>("si");
export function useBytes() {
  const u = useContext(UnitsContext);
  return (n: number) => fmtBytes(n, u);
}

export function RoleBadge({ role }: { role: string }) {
  const label: Record<string, string> = {
    "always-on": "Always-on",
    "semi-permanent": "Semi-permanent",
    portable: "Portable",
    import: "Import",
    archive: "Archive",
  };
  return <span className={`badge ${role}`}>{label[role] ?? role}</span>;
}

const stateLevel: Record<string, "ok" | "warn" | "danger" | ""> = {
  ONLINE: "ok",
  DONE: "ok",
  RUNNING: "ok",
  DRAINING: "warn",
  READ_ONLY: "warn",
  PAUSED: "warn",
  PAUSED_NEED_SPACE: "warn",
  QUEUED: "",
  OFFLINE: "danger",
  FAILED: "danger",
  CANCELLED: "",
  DISABLED: "",
  duplicate: "warn",
  conflict: "danger",
};
const stateIcon: Record<string, string> = { ok: "✓", warn: "▲", danger: "✕", "": "○" };

export function Status({ state, label }: { state: string; label?: string }) {
  const lv = stateLevel[state] ?? "";
  return (
    <span className="status">
      <span className={`dot ${lv}`} aria-hidden="true" />
      <span aria-hidden="true" style={{ fontSize: 9 }}>
        {stateIcon[lv]}
      </span>
      {label ?? state}
    </span>
  );
}

export function Bar({ pct, large }: { pct: number; large?: boolean }) {
  const p = Math.max(0, Math.min(100, pct));
  return (
    <div className={`bar ${large ? "lg" : barLevel(p)}`} role="progressbar" aria-valuenow={Math.round(p)} aria-valuemin={0} aria-valuemax={100}>
      <i style={{ width: `${p}%` }} />
    </div>
  );
}

export function OsIcon({ d }: { d: Pick<Disk, "node_name" | "filesystem_type" | "network_backed"> }) {
  const os = osOf(d);
  const label = { linux: "LNX", windows: "WIN", macos: "MAC", nas: "NAS" }[os];
  return (
    <span className="icon" aria-label={os}>
      {label}
    </span>
  );
}

export function DiskFree({ d }: { d: Disk }) {
  const t = useT();
  const b = useBytes();
  return (
    <span className="sub">
      {b(d.effective_free_bytes)} {t("common.of")} {b(d.total_bytes)}
    </span>
  );
}

export function DiskUsage({ d }: { d: Disk }) {
  return <Bar pct={usedPct(d)} />;
}

export function Dialog({ title, onClose, children, actions, labelledBy }: { title: string; onClose: () => void; children: ReactNode; actions?: ReactNode; labelledBy?: string }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    ref.current?.querySelector<HTMLElement>("input, select, button")?.focus();
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("keydown", onKey);
      prev?.focus();
    };
  }, [onClose]);
  const id = labelledBy ?? "dlg-" + title.replace(/\W+/g, "-");
  return (
    <div className="overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="card dialog" role="dialog" aria-modal="true" aria-labelledby={id} ref={ref}>
        <h2 id={id}>{title}</h2>
        {children}
        {actions && <div className="actions">{actions}</div>}
      </div>
    </div>
  );
}

/** Destructive confirmation: type the target name (CTM-COM-050, NFR-U03). */
export function ConfirmDestructive({ action, name, onConfirm, onClose }: { action: string; name: string; onConfirm: () => void; onClose: () => void }) {
  const t = useT();
  const [typed, setTyped] = useState("");
  return (
    <Dialog
      title={`${action} ${name}`}
      onClose={onClose}
      actions={
        <>
          <button className="btn" onClick={onClose}>
            {t("common.cancel")}
          </button>
          <button className="btn danger solid" disabled={typed !== name} onClick={onConfirm}>
            ⚠ {action}
          </button>
        </>
      }
    >
      <p>
        {t("common.confirmPre")}<b className="mono">{name}</b>{t("common.confirmPost")}
      </p>
      <input className="input" aria-label={t("common.confirmAriaLabel")} value={typed} onChange={(e) => setTyped(e.target.value)} />
    </Dialog>
  );
}

type Toast = { id: number; msg: string; kind: "ok" | "error" };
const ToastContext = createContext<(msg: string, kind?: "ok" | "error") => void>(() => {});
export const useToast = () => useContext(ToastContext);

export function ToastHost({ children }: { children: ReactNode }) {
  const t = useT();
  const [toasts, setToasts] = useState<Toast[]>([]);
  const push = useCallback((msg: string, kind: "ok" | "error" = "ok") => {
    const id = Date.now() + Math.random();
    setToasts((t) => [...t, { id, msg, kind }]);
    if (kind === "ok") setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 5000);
  }, []);
  return (
    <ToastContext.Provider value={push}>
      {children}
      <div className="toasts" role="status" aria-live="polite">
        {toasts.map((toast) => (
          <div key={toast.id} className={`toast ${toast.kind === "error" ? "error" : ""}`}>
            <span className="spacer">{toast.msg}</span>
            <button className="btn sm" aria-label={t("common.dismiss")} onClick={() => setToasts((x) => x.filter((y) => y.id !== toast.id))}>
              ✕
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

/** Polling hook with manual refresh. */
export function useLoad<T>(fn: () => Promise<T>, deps: unknown[] = [], everyMs = 0) {
  const [data, setData] = useState<T | undefined>();
  const [error, setError] = useState<Error | undefined>();
  const [loading, setLoading] = useState(true);
  const load = useCallback(async () => {
    try {
      setData(await fn());
      setError(undefined);
    } catch (e) {
      setError(e as Error);
    } finally {
      setLoading(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);
  useEffect(() => {
    load();
    if (!everyMs) return;
    const t = setInterval(load, everyMs);
    return () => clearInterval(t);
  }, [load, everyMs]);
  return { data, error, loading, reload: load, setData };
}

export function ErrorState({ error, onRetry }: { error: Error; onRetry?: () => void }) {
  const t = useT();
  return (
    <div className="banner danger" role="alert">
      <span className="spacer">{error.message}</span>
      {onRetry && (
        <button className="btn sm" onClick={onRetry}>
          {t("common.retry")}
        </button>
      )}
      <a href="#/activity">{t("nav.activity")}</a>
    </div>
  );
}

export function Empty({ text, action }: { text: string; action?: ReactNode }) {
  return (
    <div className="empty card">
      <div style={{ fontSize: 32, marginBottom: 8 }} aria-hidden="true">
        ∞
      </div>
      <p>{text}</p>
      {action}
    </div>
  );
}
