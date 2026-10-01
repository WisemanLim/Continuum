// CTM-COM-040 Login (FR-S02) — shown when the Controller has users and the session is missing/expired.
import { useState } from "react";
import { ApiError, api, setToken, ssoURL } from "./api";
import { useLoad } from "./ui";
import { useT } from "./i18n";

export function Login({ onDone }: { onDone: () => void }) {
  const t = useT();
  const [user, setUser] = useState("");
  const [pw, setPw] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [code, setCode] = useState("");
  const [needCode, setNeedCode] = useState(false);
  const status = useLoad(() => api.authStatus(), []);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      const s = await api.login(user, pw, code);
      setToken(s.token);
      onDone();
    } catch (x) {
      const e = x as ApiError;
      if (e.info?.mfa_required) setNeedCode(true); // second factor (FR-S02)
      else setErr(e.message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="login">
      <form className="card stack" onSubmit={submit} aria-label={t("auth.title")}>
        <div className="row">
          <span className="brand-mark" aria-hidden="true">
            ∞
          </span>
          <h1 className="spacer">Continuum</h1>
        </div>
        <div className="sub">{t("auth.subtitle")}</div>
        <label className="field">
          {t("auth.username")}
          <input className="input" autoComplete="username" value={user} onChange={(e) => setUser(e.target.value)} autoFocus />
        </label>
        <label className="field">
          {t("auth.password")}
          <input className="input" type="password" autoComplete="current-password" value={pw} onChange={(e) => setPw(e.target.value)} />
        </label>
        {needCode && (
          <label className="field">
            {t("auth.code")}
            <span className="sub">{t("auth.codeHelp")}</span>
            <input className="input mono" inputMode="numeric" autoComplete="one-time-code" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.trim())} autoFocus />
          </label>
        )}
        {err && (
          <div className="banner danger" role="alert">
            {err}
          </div>
        )}
        <button className="btn primary" disabled={busy || !user || !pw || (needCode && code.length !== 6)}>
          {t("auth.login")}
        </button>
        {status.data?.oidc && (
          <a className="btn" href={ssoURL()}>
            {t("auth.sso")}
          </a>
        )}
      </form>
    </div>
  );
}
