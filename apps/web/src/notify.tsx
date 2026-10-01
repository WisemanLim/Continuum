// CTM-COM-030 Notification center (FR-G16): header bell, unread count, toast + optional OS notification.
import { useEffect, useRef, useState } from "react";
import { api, type Notice } from "./api";
import { useT } from "./i18n";
import { useToast } from "./ui";

const LS_SEEN = "continuum.notices.seen";
const LS_OS = "continuum.notices.os";

function read(k: string): string {
  try {
    return localStorage.getItem(k) ?? "";
  } catch {
    return "";
  }
}
function write(k: string, v: string) {
  try {
    localStorage.setItem(k, v);
  } catch {
    /* private mode */
  }
}

export function NotificationCenter() {
  const t = useT();
  const toast = useToast();
  const [items, setItems] = useState<Notice[]>([]);
  const [seen, setSeen] = useState(read(LS_SEEN));
  const [open, setOpen] = useState(false);
  const newest = useRef<string | null>(null); // null until the first poll: history is not toasted
  useEffect(() => {
    let alive = true;
    const tick = () =>
      api
        .notifications()
        .then((n) => {
          if (!alive) return;
          const last = newest.current;
          if (last !== null) {
            for (const x of n.filter((x) => x.event_id > last).reverse()) {
              toast(x.message, x.kind.includes("failed") || x.kind === "disk.offline" ? "error" : undefined);
              if (read(LS_OS) === "1" && typeof Notification !== "undefined" && Notification.permission === "granted") new Notification("Continuum", { body: x.message });
            }
          }
          newest.current = n[0]?.event_id ?? last ?? "";
          setItems(n);
        })
        .catch(() => {});
    tick();
    const id = setInterval(tick, 10_000);
    return () => {
      alive = false;
      clearInterval(id);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const unread = items.filter((x) => x.event_id > seen).length;
  const markRead = () => {
    const top = items[0]?.event_id ?? seen;
    setSeen(top);
    write(LS_SEEN, top);
  };
  return (
    <div className="notify">
      <button className="btn sm bell" aria-label={t("ntf.bell", { n: unread })} aria-expanded={open} onClick={() => setOpen((o) => !o)}>
        <span aria-hidden="true">🔔</span>
        {unread > 0 && (
          <span className="chip" data-testid="unread">
            {unread}
          </span>
        )}
      </button>
      {open && (
        <div className="notify-panel card" role="dialog" aria-label={t("ntf.title")}>
          <div className="row">
            <b className="spacer">{t("ntf.title")}</b>
            <button className="btn sm" onClick={markRead} disabled={unread === 0}>
              {t("ntf.markRead")}
            </button>
          </div>
          <div className="stack" style={{ gap: 6, maxHeight: 360, overflow: "auto" }}>
            {items.length === 0 && <div className="sub">{t("ntf.empty")}</div>}
            {items.map((x) => (
              <div key={x.event_id} className={`notice ${x.event_id > seen ? "unread" : ""}`}>
                <div>{x.message}</div>
                <div className="sub">
                  {new Date(x.ts).toLocaleString()} · {x.kind}
                </div>
              </div>
            ))}
          </div>
          {typeof Notification !== "undefined" && (
            <label className="toggle sub">
              <input
                type="checkbox"
                defaultChecked={read(LS_OS) === "1"}
                onChange={async (e) => {
                  const on = e.target.checked && (await Notification.requestPermission()) === "granted";
                  write(LS_OS, on ? "1" : "");
                }}
              />{" "}
              {t("ntf.desktop")}
            </label>
          )}
        </div>
      )}
    </div>
  );
}
