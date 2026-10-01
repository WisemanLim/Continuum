// CTM-DSK-P03 Drain · CTM-VOL-P06 Rebalance — plan preview then start (FR-D10, FR-P10).
import { useEffect, useState } from "react";
import { ApiError, api, type CopyRequest, type Plan } from "../api";
import { useT } from "../i18n";
import { Dialog, useToast } from "../ui";
import { PlanView } from "./Transfers";

export function RelocateDialog({ title, req, note, onClose, onStarted }: { title: string; req: CopyRequest; note: string; onClose: () => void; onStarted: () => void }) {
  const t = useT();
  const toast = useToast();
  const [plan, setPlan] = useState<Plan | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [win, setWin] = useState("");
  useEffect(() => {
    api
      .plan(req)
      .then(setPlan)
      .catch((e: ApiError) => setErr(e.message));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const start = () => {
    setBusy(true);
    api
      .start(win ? { ...req, window: win } : req)
      .then(() => (toast(t("rel.started", { n: title })), onStarted()))
      .catch((e: ApiError) => (toast(e.message, "error"), setBusy(false)));
  };
  return (
    <Dialog
      title={title}
      onClose={onClose}
      actions={
        <>
          <button className="btn" onClick={onClose}>
            {t("common.cancel")}
          </button>
          <button className="btn primary" disabled={!plan || !plan.sufficient || busy} onClick={start}>
            {t("rel.start", { n: title })}
          </button>
        </>
      }
    >
      <div className="stack">
        <label className="field">
          {t("rel.window")}
          <input className="input mono" placeholder="22:00-06:00" value={win} onChange={(e) => setWin(e.target.value)} style={{ width: 160 }} />
        </label>
        <div className="sub">{note}</div>
        {err && <div className="banner">{err}</div>}
        {plan ? plan.files === 0 ? <div className="empty">{t("rel.nothingToMove")}</div> : <PlanView plan={plan} /> : !err && <div className="sub">{t("rel.planning")}</div>}
      </div>
    </Dialog>
  );
}
