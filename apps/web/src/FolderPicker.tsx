// CTM-COM-P02 Folder picker — browses folders on the Controller host (/v1/fs/browse). Same in browser,
// desktop and extension, and it shows exactly the paths the Controller can read.
import { useEffect, useState } from "react";
import { ApiError, api, type Browse } from "./api";
import { useT } from "./i18n";
import { Dialog } from "./ui";

export function FolderPicker({ start, onPick, onClose }: { start?: string; onPick: (path: string) => void; onClose: () => void }) {
  const t = useT();
  const [data, setData] = useState<Browse | null>(null);
  const [err, setErr] = useState("");
  const open = (p: string) =>
    api
      .browse(p)
      .then((d) => (setData(d), setErr("")))
      .catch((e: ApiError) => setErr(e.message));
  useEffect(() => {
    open(start ?? "");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  return (
    <Dialog
      title={t("pick.title")}
      onClose={onClose}
      actions={
        <>
          <button className="btn" onClick={onClose}>
            {t("common.cancel")}
          </button>
          <button className="btn primary" disabled={!data} onClick={() => data && onPick(data.path)} data-testid="pick-choose">
            {t("pick.choose")}
          </button>
        </>
      }
    >
      <div className="stack">
        <div className="row wrap">
          {data?.roots.map((r) => (
            <button key={r.path} className="btn sm" onClick={() => open(r.path)}>
              {r.name}
            </button>
          ))}
        </div>
        <div className="mono" data-testid="pick-path">
          {data?.path}
        </div>
        {err && <div className="banner">{err}</div>}
        <div className="picker-list" role="listbox" aria-label={t("pick.title")}>
          {data?.parent && (
            <button className="picker-item" onClick={() => open(data.parent)}>
              ↰ ..
            </button>
          )}
          {data?.dirs.map((d) => (
            <button key={d.path} className="picker-item" role="option" aria-selected={false} onDoubleClick={() => onPick(d.path)} onClick={() => open(d.path)}>
              📁 {d.name}
            </button>
          ))}
          {data && data.dirs.length === 0 && <div className="sub">{t("pick.noFolders")}</div>}
        </div>
        {data && <div className="sub">{t("pick.files", { n: data.files })}</div>}
      </div>
    </Dialog>
  );
}
