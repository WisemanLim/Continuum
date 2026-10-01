// CTM-SET-010/070 Settings — General (language, theme, units) · Controller connection · About (FR-G07, G13).
import { useState } from "react";
import { ApiError, api, controllerURL, diagnosticsURL, setController, token, APPROVAL_ACTIONS, ROLES, type Approval, type Me, type User } from "../api";
import { useT } from "../i18n";
import { useLoad, useToast } from "../ui";

export interface Prefs {
  lang: "ko" | "en";
  theme: "system" | "light" | "dark";
  units: "si" | "iec";
}

export function loadPrefs(): Prefs {
  try {
    const p = JSON.parse(localStorage.getItem("continuum.prefs") || "{}");
    return { lang: p.lang ?? "ko", theme: p.theme ?? "system", units: p.units ?? "si" };
  } catch {
    return { lang: "ko", theme: "system", units: "si" };
  }
}

export function Settings({ prefs, setPrefs, me }: { prefs: Prefs; setPrefs: (p: Prefs) => void; me: Me | null }) {
  const t = useT();
  const toast = useToast();
  const [url, setUrl] = useState(controllerURL());
  const [tok, setTok] = useState(token());
  const info = useLoad(() => api.info(), [url]);
  const [maskDiag, setMaskDiag] = useState(true);
  return (
    <>
      <h1>{t("nav.settings")}</h1>
      <div className="grid">
        <section className="card stack">
          <h2>{t("set.general")}</h2>
          <label className="field">
            {t("set.language")}
            <select className="input" value={prefs.lang} onChange={(e) => setPrefs({ ...prefs, lang: e.target.value as Prefs["lang"] })}>
              <option value="ko">한국어</option>
              <option value="en">English</option>
            </select>
          </label>
          <label className="field">
            {t("set.theme")}
            <select className="input" value={prefs.theme} onChange={(e) => setPrefs({ ...prefs, theme: e.target.value as Prefs["theme"] })}>
              <option value="system">{t("set.themeSystem")}</option>
              <option value="light">{t("set.themeLight")}</option>
              <option value="dark">{t("set.themeDark")}</option>
            </select>
          </label>
          <label className="field">
            {t("set.units")}
            <select className="input" value={prefs.units} onChange={(e) => setPrefs({ ...prefs, units: e.target.value as Prefs["units"] })}>
              <option value="si">TB (10^12)</option>
              <option value="iec">TiB (2^40)</option>
            </select>
          </label>
        </section>
        <section className="card stack">
          <h2>{t("set.connection")}</h2>
          <label className="field">
            {t("set.controllerUrl")}
            <input className="input mono" value={url} onChange={(e) => setUrl(e.target.value)} />
          </label>
          <label className="field">
            {t("set.apiToken")}
            <input className="input mono" type="password" value={tok} onChange={(e) => setTok(e.target.value)} />
          </label>
          <div className="row">
            <button
              className="btn primary"
              onClick={() => {
                setController(url, tok);
                toast(t("set.connSaved"));
                info.reload();
              }}
            >
              {t("common.save")}
            </button>
            <span className="sub">{info.data ? t("set.connected", { n: info.data.controller_id }) : info.error ? info.error.message : ""}</span>
          </div>
        </section>
        {me?.auth_enabled && me.auth_source !== "oidc" && <Account />}
        {me?.auth_enabled && me.auth_source !== "oidc" && <Mfa on={!!me.mfa_enabled} />}
        {me?.permissions.includes("security") && <Users me={me} />}
        {me?.auth_enabled && me.permissions.includes("security") && <Approvals canEdit={me.permissions.includes("system")} />}
        <Notifications />
        {me?.permissions.includes("system") && <Sso />}
        {me?.permissions.includes("system") && <Ldap />}
        <section className="card stack">
          <h2>{t("set.diagnostics")}</h2>
          <div className="sub">{t("set.diagNote")}</div>
          <label className="toggle">
            <input type="checkbox" checked={maskDiag} onChange={(e) => setMaskDiag(e.target.checked)} /> {t("set.maskPaths")}
          </label>
          <div className="row">
            <a className="btn" href={diagnosticsURL(maskDiag)} download>
              {t("set.downloadDiag")}
            </a>
            <a className="btn" href={`${controllerURL()}/metrics`} target="_blank" rel="noreferrer">
              {t("set.prometheus")}
            </a>
          </div>
        </section>
        <section className="card stack">
          <h2>{t("set.about")}</h2>
          <div>{t("set.aboutText")}</div>
          <div className="sub">{t("set.controllerInfo", { n: info.data?.version ?? "—", m: info.data?.local_node?.hostname ?? "—" })}</div>
          <div className="sub">{t("set.aboutTagline")}</div>
          <div className="sub">{t("set.aboutLicenses")}</div>
        </section>
      </div>
    </>
  );
}

// CTM-SET-040 Notifications — webhook (Slack / Discord / generic JSON) (FR-D26).
function Notifications() {
  const t = useT();
  const toast = useToast();
  const { data } = useLoad(() => api.settings(), []);
  const [url, setUrl] = useState<string | null>(null);
  const [format, setFormat] = useState<string | null>(null);
  const [mail, setMail] = useState<Record<string, string>>({});
  const u = url ?? data?.["notify.webhook_url"] ?? "";
  const f = format ?? (data?.["notify.format"] || "slack");
  const mv = (k: string) => mail[k] ?? data?.[k] ?? "";
  const mailFields: [string, string, string][] = [
    ["notify.smtp_host", "ntf.smtpHost", "smtp.example.com:587"],
    ["notify.smtp_user", "ntf.smtpUser", "alerts@example.com"],
    ["notify.smtp_password", "ntf.smtpPassword", ""],
    ["notify.email_from", "ntf.emailFrom", "alerts@example.com"],
    ["notify.email_to", "ntf.emailTo", "ops@example.com, oncall@example.com"],
  ];
  return (
    <section className="card stack">
      <h2>{t("ntf.title")}</h2>
      <div className="sub">{t("ntf.triggers")}</div>
      <label className="field">
        {t("ntf.webhookUrl")}
        <input className="input mono" placeholder="https://hooks.slack.com/services/…" value={u} onChange={(e) => setUrl(e.target.value)} />
      </label>
      <label className="field">
        {t("ntf.format")}
        <select className="input" value={f} onChange={(e) => setFormat(e.target.value)}>
          <option value="slack">{t("ntf.formatSlack")}</option>
          <option value="discord">{t("ntf.formatDiscord")}</option>
          <option value="generic">{t("ntf.formatGeneric")}</option>
        </select>
      </label>
      <h3>{t("ntf.email")}</h3>
      {mailFields.map(([k, label, ph]) => (
        <label key={k} className="field">
          {t(label as "ntf.email")}
          <input
            className="input mono"
            type={k === "notify.smtp_password" ? "password" : "text"}
            placeholder={ph}
            value={mv(k)}
            onChange={(e) => setMail({ ...mail, [k]: e.target.value })}
          />
        </label>
      ))}
      <label className="field">
        {t("ntf.smtpTls")}
        <select className="input" value={mv("notify.smtp_tls") || "starttls"} onChange={(e) => setMail({ ...mail, "notify.smtp_tls": e.target.value })}>
          <option value="starttls">STARTTLS (587)</option>
          <option value="tls">TLS (465)</option>
          <option value="none">{t("ntf.smtpNone")}</option>
        </select>
      </label>
      <div className="row">
        <button
          className="btn primary"
          onClick={() =>
            api
              .saveSettings({ ...mail, "notify.webhook_url": u, "notify.format": f })
              .then(() => toast(t("ntf.saved")))
              .catch((e: ApiError) => toast(e.message, "error"))
          }
        >
          {t("common.save")}
        </button>
        <button
          className="btn"
          disabled={!u && !(mv("notify.smtp_host") && mv("notify.email_to"))}
          onClick={() =>
            api
              .notifyTest()
              .then(() => toast(t("ntf.testSent")))
              .catch((e: ApiError) => toast(e.message, "error"))
          }
        >
          {t("ntf.sendTest")}
        </button>
      </div>
    </section>
  );
}

// CTM-SET-050 Users & roles (FR-S01) — security-admin / system-admin.
function Users({ me }: { me: Me }) {
  const t = useT();
  const toast = useToast();
  const { data, reload } = useLoad(() => api.users(), []);
  const [name, setName] = useState("");
  const [pw, setPw] = useState("");
  const [role, setRole] = useState<string>("viewer");
  const run = (p: Promise<unknown>, msg: string) =>
    p.then(() => (toast(msg), reload())).catch((e: ApiError) => toast(e.message, "error"));
  return (
    <section className="card stack" style={{ gridColumn: "1 / -1" }}>
      <h2>{t("usr.title")}</h2>
      <div className="sub">{t("usr.help")}</div>
      <table className="t" data-testid="users">
        <thead>
          <tr>
            <th>{t("auth.username")}</th>
            <th>{t("usr.role")}</th>
            <th>{t("usr.lastLogin")}</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {(data ?? []).map((u: User) => (
            <tr key={u.user_id}>
              <td>
                <b>{u.username}</b> {u.disabled && <span className="badge">{t("usr.disabled")}</span>}
              </td>
              <td>
                <select className="input" aria-label={t("usr.roleOf", { n: u.username })} value={u.role} onChange={(e) => run(api.patchUser(u.user_id, { role: e.target.value }), t("usr.saved"))}>
                  {ROLES.map((r) => (
                    <option key={r}>{r}</option>
                  ))}
                </select>
              </td>
              <td className="sub">{u.last_login_at ? new Date(u.last_login_at).toLocaleString() : "—"}</td>
              <td>
                {u.username !== me.username && (
                  <div className="row" style={{ gap: 4 }}>
                    <button className="btn sm" onClick={() => run(api.patchUser(u.user_id, { disabled: !u.disabled }), t("usr.saved"))}>
                      {u.disabled ? t("usr.enable") : t("usr.disable")}
                    </button>
                    <button className="btn sm danger" onClick={() => run(api.deleteUser(u.user_id), t("usr.deleted", { n: u.username }))}>
                      {t("usr.delete")}
                    </button>
                  </div>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <div className="row wrap">
        <input className="input" placeholder={t("auth.username")} aria-label={t("usr.newName")} value={name} onChange={(e) => setName(e.target.value)} style={{ width: 180 }} />
        <input className="input" type="password" placeholder={t("usr.pwHint")} aria-label={t("usr.newPassword")} value={pw} onChange={(e) => setPw(e.target.value)} style={{ width: 200 }} />
        <select className="input" aria-label={t("usr.newRole")} value={role} onChange={(e) => setRole(e.target.value)} style={{ width: 170 }}>
          {ROLES.map((r) => (
            <option key={r}>{r}</option>
          ))}
        </select>
        <button
          className="btn primary"
          disabled={!name || pw.length < 10}
          onClick={() => run(api.createUser(name, pw, role).then(() => (setName(""), setPw(""))), t("usr.created", { n: name }))}
        >
          {t("usr.add")}
        </button>
      </div>
    </section>
  );
}

function Account() {
  const t = useT();
  const toast = useToast();
  const [oldPw, setOld] = useState("");
  const [newPw, setNew] = useState("");
  return (
    <section className="card stack">
      <h2>{t("auth.account")}</h2>
      <label className="field">
        {t("auth.oldPassword")}
        <input className="input" type="password" autoComplete="current-password" value={oldPw} onChange={(e) => setOld(e.target.value)} />
      </label>
      <label className="field">
        {t("auth.newPassword")}
        <input className="input" type="password" autoComplete="new-password" value={newPw} onChange={(e) => setNew(e.target.value)} />
      </label>
      <button
        className="btn"
        disabled={!oldPw || newPw.length < 10}
        onClick={() =>
          api
            .changePassword(oldPw, newPw)
            .then(() => (toast(t("auth.pwChanged")), (location.hash = "#/overview"), location.reload()))
            .catch((e: ApiError) => toast(e.message, "error"))
        }
      >
        {t("auth.changePassword")}
      </button>
    </section>
  );
}

// CTM-SET-060 Two-factor authentication (FR-S02): TOTP setup → confirm with the first code.
function Mfa({ on }: { on: boolean }) {
  const t = useT();
  const toast = useToast();
  const [setup, setSetup] = useState<{ secret: string; otpauth_url: string } | null>(null);
  const [code, setCode] = useState("");
  const done = (msg: string) => (toast(msg), setTimeout(() => location.reload(), 300));
  return (
    <section className="card stack" data-testid="mfa">
      <h2>{t("mfa.title")}</h2>
      <div className="sub">{on ? t("mfa.on") : t("mfa.off")}</div>
      {!on && !setup && (
        <button className="btn" onClick={() => api.mfaSetup().then(setSetup).catch((e: ApiError) => toast(e.message, "error"))}>
          {t("mfa.setup")}
        </button>
      )}
      {setup && (
        <>
          <div className="sub">{t("mfa.secretHelp")}</div>
          <code className="mono" data-testid="mfa-secret">
            {setup.secret}
          </code>
          <a className="sub mono" href={setup.otpauth_url}>
            {setup.otpauth_url}
          </a>
        </>
      )}
      {(setup || on) && (
        <div className="row">
          <input className="input mono" aria-label={t("auth.code")} inputMode="numeric" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.trim())} style={{ width: 120 }} />
          <button
            className={on ? "btn danger" : "btn primary"}
            disabled={code.length !== 6}
            onClick={() =>
              (on ? api.mfaDisable(code) : api.mfaEnable(code))
                .then(() => done(on ? t("mfa.disabled") : t("mfa.enabled")))
                .catch((e: ApiError) => toast(e.message, "error"))
            }
          >
            {on ? t("mfa.disable") : t("mfa.enable")}
          </button>
        </div>
      )}
    </section>
  );
}

// CTM-SET-080 Two-person approval (FR-S05): policy + pending requests.
function Approvals({ canEdit }: { canEdit: boolean }) {
  const t = useT();
  const toast = useToast();
  const list = useLoad(() => api.approvals(), [], 10000);
  const cfg = useLoad(() => (canEdit ? api.settings() : Promise.resolve({} as Record<string, string>)), []);
  const [sel, setSel] = useState<string[] | null>(null);
  const cur = sel ?? (cfg.data?.["security.approval_actions"] ?? "").split(",").filter(Boolean);
  return (
    <section className="card stack" style={{ gridColumn: "1 / -1" }} data-testid="approvals">
      <h2>{t("apr.title")}</h2>
      <div className="sub">{t("apr.help")}</div>
      {canEdit && (
        <div className="row wrap">
          <span>{t("apr.actions")}</span>
          {APPROVAL_ACTIONS.map((a) => (
            <label key={a} className="toggle">
              <input type="checkbox" checked={cur.includes(a)} onChange={(e) => setSel(e.target.checked ? [...cur, a] : cur.filter((x) => x !== a))} /> <span className="mono">{a}</span>
            </label>
          ))}
          <button
            className="btn"
            onClick={() =>
              api
                .saveSettings({ "security.approval_actions": cur.join(",") })
                .then(() => toast(t("apr.saved")))
                .catch((e: ApiError) => toast(e.message, "error"))
            }
          >
            {t("common.save")}
          </button>
        </div>
      )}
      {(list.data ?? []).length === 0 ? (
        <div className="sub">{t("apr.none")}</div>
      ) : (
        <table className="t">
          <tbody>
            {(list.data ?? []).map((a: Approval) => (
              <tr key={a.approval_id}>
                <td className="mono">{a.approval_id}</td>
                <td className="mono">{a.action}</td>
                <td className="mono">{a.target}</td>
                <td>{a.requested_by}</td>
                <td className="sub">{new Date(a.created_at).toLocaleString()}</td>
                <td>
                  <div className="row" style={{ gap: 4 }}>
                    {[true, false].map((ok) => (
                      <button
                        key={String(ok)}
                        className={ok ? "btn sm primary" : "btn sm danger"}
                        onClick={() =>
                          api
                            .decide(a.approval_id, ok)
                            .then((r) => (toast(t("apr.decided", { n: r.state })), list.reload()))
                            .catch((e: ApiError) => toast(e.message, "error"))
                        }
                      >
                        {ok ? t("apr.approve") : t("apr.reject")}
                      </button>
                    ))}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

// CTM-SET-090 Single sign-on (FR-S02): OpenID Connect provider settings (client secret is write-only).
const SSO_KEYS = ["oidc.issuer", "oidc.client_id", "oidc.client_secret", "oidc.redirect_url", "oidc.default_role", "oidc.username_claim"];
const LDAP_KEYS = ["ldap.url", "ldap.starttls", "ldap.bind_dn", "ldap.bind_password", "ldap.user_base", "ldap.user_filter", "ldap.group_attr", "ldap.role_map", "ldap.default_role", "ldap.ca_file"];
const SECRET_KEYS = ["oidc.client_secret", "ldap.bind_password"];
const ROLE_KEYS = ["oidc.default_role", "ldap.default_role"];

function Sso() {
  const t = useT();
  return (
    <KeyCard
      testid="sso"
      title={t("sso.title")}
      help={t("sso.help")}
      saved={t("sso.saved")}
      keys={SSO_KEYS}
      placeholder={{
        "oidc.issuer": "https://idp.example.com/realms/main",
        "oidc.redirect_url": `${controllerURL()}/v1/auth/oidc/callback`,
        "oidc.username_claim": "preferred_username",
      }}
    />
  );
}

// CTM-SET-095 LDAP / Active Directory (FR-S02): the bind password is write-only; roles follow groups.
function Ldap() {
  const t = useT();
  return (
    <KeyCard
      testid="ldap"
      title={t("ldap.title")}
      help={t("ldap.help")}
      saved={t("ldap.saved")}
      keys={LDAP_KEYS}
      placeholder={{
        "ldap.url": "ldaps://dc.example.com:636",
        "ldap.starttls": "true",
        "ldap.bind_dn": "cn=continuum,ou=svc,dc=example,dc=com",
        "ldap.user_base": "ou=people,dc=example,dc=com",
        "ldap.user_filter": "(&(objectClass=person)(uid=%s))",
        "ldap.group_attr": "memberOf",
        "ldap.role_map": "cn=storage,ou=groups,dc=example,dc=com=storage-admin;cn=ops,ou=groups,dc=example,dc=com=operator",
        "ldap.ca_file": "/etc/continuum/ldap-ca.pem",
      }}
    />
  );
}

// Settings card editing a group of keys; secrets show the server's mask and are only sent when changed.
function KeyCard(props: { testid: string; title: string; help: string; saved: string; keys: string[]; placeholder: Record<string, string> }) {
  const t = useT();
  const toast = useToast();
  const { data } = useLoad(() => api.settings(), []);
  const [edit, setEdit] = useState<Record<string, string>>({});
  const v = (k: string) => edit[k] ?? data?.[k] ?? "";
  return (
    <section className="card stack" data-testid={props.testid}>
      <h2>{props.title}</h2>
      <div className="sub">{props.help}</div>
      {props.keys.map((k) => (
        <label key={k} className="field">
          <span className="mono">{k}</span>
          {ROLE_KEYS.includes(k) ? (
            <select className="input" value={v(k)} onChange={(e) => setEdit({ ...edit, [k]: e.target.value })}>
              <option value="">{k.startsWith("ldap.") ? t("ldap.noDefault") : "viewer"}</option>
              {ROLES.map((r) => (
                <option key={r}>{r}</option>
              ))}
            </select>
          ) : (
            <input
              className="input mono"
              type={SECRET_KEYS.includes(k) ? "password" : "text"}
              placeholder={props.placeholder[k] ?? ""}
              value={v(k)}
              onChange={(e) => setEdit({ ...edit, [k]: e.target.value })}
            />
          )}
        </label>
      ))}
      <button
        className="btn primary"
        onClick={() =>
          api
            .saveSettings(edit)
            .then(() => (toast(props.saved), setEdit({})))
            .catch((e: ApiError) => toast(e.message, "error"))
        }
      >
        {t("common.save")}
      </button>
    </section>
  );
}
