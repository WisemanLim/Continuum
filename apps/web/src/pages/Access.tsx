// CTM-VOL-036 Access point — SMB shares of a volume (FR-A01~A05, A08, A09).
import { useState } from "react";
import { ApiError, api, type AccessStatus, type Volume } from "../api";
import { useT } from "../i18n";
import { Status, useLoad, useToast } from "../ui";

export function AccessPoint({ v, onSaved }: { v: Volume; onSaved: (v: Volume) => void }) {
  const t = useT();
  const toast = useToast();
  const { data: s, reload, setData } = useLoad(() => api.access(), [], 10000);
  const [busy, setBusy] = useState(false);

  const MODES: [Volume["access_mode"], string, string][] = [
    ["read-centric", t("ac.modeReadCentric"), t("ac.modeReadCentricHelp")],
    ["full-direct", t("ac.modeFull"), t("ac.modeFullHelp")],
    ["blocked", t("ac.modeBlocked"), t("ac.modeBlockedHelp")],
  ];

  const run = (p: Promise<AccessStatus>, msg: string) => {
    setBusy(true);
    p.then((x) => (setData(x), toast(msg)))
      .catch((e: ApiError) => (toast(e.message, "error"), reload()))
      .finally(() => setBusy(false));
  };
  const setMode = (m: string) =>
    api
      .updateVolume(v.slug, { expected_revision: v.revision, access_mode: m })
      .then((nv) => {
        onSaved(nv);
        toast(t("ac.modeSaved"));
        if (s?.enabled) run(api.accessAction("reload"), t("ac.reloaded"));
      })
      .catch((e: ApiError) => toast(e.message, "error"));
  const shares = (s?.shares ?? []).filter((x) => x.volume === v.slug);
  const main = shares.find((x) => x.kind === "volume");
  const host = main ? (main.url.replace(/^smb:\/\//, "").split("/")[0] ?? "") : "";
  return (
    <div className="stack" style={{ maxWidth: 900 }}>
      <div className="card stack">
        <div className="row">
          <h2 className="spacer">{t("ac.smbTitle")}</h2>
          {s && <Status state={s.running ? "ONLINE" : s.enabled ? "FAILED" : "DISABLED"} label={s.running ? t("ac.running", { n: s.clients }) : s.enabled ? t("ac.notRunning") : t("ac.disabled")} />}
        </div>
        {s && !s.image_present && <div className="banner">{t("ac.imageMissing", { n: s.image })}</div>}
        {s?.error && <div className="banner">{s.error}</div>}
        <div className="row">
          {!s?.enabled ? (
            <button className="btn primary" disabled={busy} onClick={() => run(api.accessEnable(), t("ac.started"))}>
              {t("ac.enable")}
            </button>
          ) : (
            <>
              <button className="btn" disabled={busy} onClick={() => run(api.accessAction("reload"), t("ac.reloaded"))}>
                {t("common.reload")}
              </button>
              <button className="btn danger" disabled={busy} onClick={() => run(api.accessAction("disable"), t("ac.stopped"))}>
                {t("ac.disable")}
              </button>
            </>
          )}
          <span className="spacer" />
          <button className="btn" onClick={() => api.accessCredentials().then((x) => setData(x))}>
            {t("ac.showCredentials")}
          </button>
        </div>
        {s?.password && (
          <div className="mono" data-testid="access-credentials">
            {t("ac.credentials", { n: s.user ?? "", m: s.password })}
          </div>
        )}
        <div className="sub">{t("ac.smbNote", { n: s?.bind ?? "", m: String(s?.port ?? "") })}</div>
      </div>
      <div className="card stack">
        <h2>{t("ac.directMode")}</h2>
        {MODES.map(([m, label, help]) => (
          <label key={m} className="toggle" style={{ alignItems: "flex-start" }}>
            <input type="radio" name="access-mode" checked={v.access_mode === m} onChange={() => setMode(m)} />
            <span>
              <b>{label}</b>
              <div className="sub">{help}</div>
            </span>
          </label>
        ))}
      </div>
      <div className="card">
        <table className="t" data-testid="shares">
          <thead>
            <tr>
              <th>{t("ac.colShare")}</th>
              <th>{t("ac.colMode")}</th>
              <th>{t("ac.colUrl")}</th>
              <th>{t("ac.colDisks")}</th>
            </tr>
          </thead>
          <tbody>
            {shares.map((x) => (
              <tr key={x.name}>
                <td>
                  <b>{x.name}</b>
                  <div className="sub">{x.kind === "volume" ? t("ac.wholeVolume") : t("ac.diskOrdinal", { n: x.disk ?? "", m: String(x.ordinal ?? "") })}</div>
                </td>
                <td>{x.read_only ? t("ac.readOnly") : t("ac.readWrite")}</td>
                <td className="mono">
                  {x.url}{" "}
                  <button className="btn sm" onClick={() => navigator.clipboard?.writeText(x.url).then(() => toast(t("common.copied")))}>
                    {t("common.copy")}
                  </button>
                </td>
                <td className="sub">{x.branches?.join(" · ")}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {shares.length === 0 && <div className="empty">{v.access_mode === "blocked" ? t("ac.blockedVolume") : t("ac.noShare")}</div>}
      </div>
      {main && (
        <div className="card stack">
          <h2>{t("ac.connect")}</h2>
          <div>
            <b>macOS</b> — {t("ac.macConnect")} <span className="mono">{main.url}</span>
          </div>
          <div>
            <b>Windows</b> — <span className="mono">New-PSDrive -Name V -PSProvider FileSystem -Root \\{host.split(":")[0]}\{main.name} -Persist</span> (port 445 only; use 0.0.0.0:445 for LAN)
          </div>
          <div>
            <b>Linux</b> — <span className="mono">sudo mount -t cifs //{host.split(":")[0]}/{main.name} /mnt/{main.name} -o port={host.split(":")[1] ?? 445},username={s?.user}</span>
          </div>
          <div className="sub">{t("ac.connectNote")}</div>
        </div>
      )}
    </div>
  );
}
