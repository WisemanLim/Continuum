// App shell (CTM-COM-010): sidebar 6 items + header (breadcrumb, agent status, user) + hash router.
import { useEffect, useState, type ReactElement } from "react";
import { ApiError, api, setToken, type Me } from "./api";
import { Login } from "./Login";
import { LangContext, useT, type Lang } from "./i18n";
import type { Units } from "./logic";
import { ToastHost, UnitsContext } from "./ui";
import { ErrorBoundary } from "./ErrorBoundary";
import { NotificationCenter } from "./notify";
import { Overview } from "./pages/Overview";
import { Volumes } from "./pages/Volumes";
import { CreateVolume } from "./pages/CreateVolume";
import { VolumeDetail } from "./pages/VolumeDetail";
import { Nodes } from "./pages/Nodes";
import { DiskDetail } from "./pages/DiskDetail";
import { Transfers } from "./pages/Transfers";
import { JobDetail } from "./pages/JobDetail";
import { Activity } from "./pages/Activity";
import { Settings, loadPrefs, type Prefs } from "./pages/Settings";

// SSO callback landing (FR-S02): take the session token out of the URL before the router reads the hash.
if (typeof location !== "undefined") {
  const m = /^#oidc_token=([^&]+)/.exec(location.hash);
  if (m?.[1]) {
    setToken(decodeURIComponent(m[1]));
    history.replaceState(null, "", location.pathname + "#/overview");
  }
}

function useHash() {
  const [hash, setHash] = useState(() => location.hash.slice(1) || "/overview");
  useEffect(() => {
    const on = () => setHash(location.hash.slice(1) || "/overview");
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return hash;
}

const NAV = [
  { path: "/overview", key: "nav.overview", icon: "◧" },
  { path: "/volumes", key: "nav.volumes", icon: "▤" },
  { path: "/nodes", key: "nav.nodes", icon: "⌸" },
  { path: "/transfers", key: "nav.transfers", icon: "⇅" },
  { path: "/activity", key: "nav.activity", icon: "☰" },
  { path: "/settings", key: "nav.settings", icon: "⚙" },
] as const;

function AgentStatus() {
  const t = useT();
  const [s, setS] = useState<{ on: number; all: number } | null | "down">(null);
  useEffect(() => {
    let alive = true;
    const tick = () =>
      api
        .info()
        .then((i) => alive && setS({ on: i.agents_online, all: i.agents_total }))
        .catch(() => alive && setS("down"));
    tick();
    const id = setInterval(tick, 10_000);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, []);
  if (s === null) return null;
  if (s === "down")
    return (
      <a className="status" href="#/settings" data-testid="agent-status">
        <span className="dot danger" /> {t("hdr.disconnected")}
      </a>
    );
  const all = s.on === s.all;
  return (
    <span className="status" data-testid="agent-status">
      <span className={`dot ${all ? "ok" : "warn"}`} /> {all ? t("hdr.allOnline") : t("hdr.someOnline", { n: s.on, m: s.all })}
    </span>
  );
}

function UserMenu({ me }: { me: Me | null }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const name = me?.username ?? "local";
  return (
    <div className="notify">
      <button className="avatar" aria-label={t("auth.menu", { n: name })} aria-expanded={open} onClick={() => setOpen((o) => !o)} data-testid="user-menu">
        {name.slice(0, 2).toUpperCase()}
      </button>
      {open && (
        <div className="notify-panel card" role="dialog" aria-label={t("auth.account")}>
          <b>{name}</b>
          <div className="sub">{me?.role}</div>
          {me?.auth_enabled ? (
            <button
              className="btn"
              onClick={() => {
                api.logout().catch(() => {});
                setToken("");
                location.hash = "#/overview";
                location.reload();
              }}
            >
              {t("auth.logout")}
            </button>
          ) : (
            <div className="sub">{t("auth.singleUser")}</div>
          )}
        </div>
      )}
    </div>
  );
}

function Shell({ hash, prefs, setPrefs, me }: { hash: string; prefs: Prefs; setPrefs: (p: Prefs) => void; me: Me | null }) {
  const t = useT();
  const parts = hash.split("/").filter(Boolean);
  const top = "/" + (parts[0] === "disks" ? "nodes" : (parts[0] ?? "overview"));
  let page: ReactElement;
  let crumbs: { label: string; href?: string }[] = [];
  switch (parts[0]) {
    case "volumes":
      if (parts[1] === "new") {
        page = <CreateVolume />;
        crumbs = [{ label: t("nav.volumes"), href: "#/volumes" }, { label: t("vol.create") }];
      } else if (parts[1]) {
        page = <VolumeDetail slug={decodeURIComponent(parts[1])} tab={parts[2] ?? "disks"} />;
        crumbs = [{ label: t("nav.volumes"), href: "#/volumes" }, { label: decodeURIComponent(parts[1]) }];
      } else {
        page = <Volumes />;
        crumbs = [{ label: t("nav.volumes") }];
      }
      break;
    case "disks":
      page = <DiskDetail id={parts[1] ?? ""} tab={parts[2] ?? "overview"} />;
      crumbs = [{ label: t("nav.nodes"), href: "#/nodes" }, { label: parts[1] ?? "" }];
      break;
    case "nodes":
      page = <Nodes />;
      crumbs = [{ label: t("nav.nodes") }];
      break;
    case "transfers":
      if (parts[1]) {
        page = <JobDetail id={parts[1]} />;
        crumbs = [{ label: t("nav.transfers"), href: "#/transfers" }, { label: parts[1] }];
      } else {
        page = <Transfers />;
        crumbs = [{ label: t("nav.transfers") }];
      }
      break;
    case "activity":
      page = <Activity />;
      crumbs = [{ label: t("nav.activity") }];
      break;
    case "settings":
      page = <Settings prefs={prefs} setPrefs={setPrefs} me={me} />;
      crumbs = [{ label: t("nav.settings") }];
      break;
    default:
      page = <Overview />;
      crumbs = [{ label: t("nav.overview") }];
  }
  return (
    <div className="shell">
      <nav className="sidebar" aria-label="Main">
        <a className="brand" href="#/overview" aria-label="Continuum">
          <span className="brand-mark" aria-hidden="true">
            ∞
          </span>
          <span className="brand-name">Continuum</span>
        </a>
        <div className="nav">
          {NAV.map((n) => (
            <a key={n.path} href={"#" + n.path} aria-current={top === n.path ? "page" : undefined}>
              <span className="nav-icon" aria-hidden="true">
                {n.icon}
              </span>
              <span className="nav-label">{t(n.key)}</span>
            </a>
          ))}
        </div>
        <div className="sidebar-foot">v0.3.0 · P3</div>
      </nav>
      <div className="main">
        <header className="header">
          <nav className="crumbs" aria-label="Breadcrumb">
            {crumbs.map((c, i) => (
              <span key={i}>
                {i > 0 && " › "}
                {c.href ? <a href={c.href}>{c.label}</a> : <span aria-current="page">{c.label}</span>}
              </span>
            ))}
          </nav>
          <div className="header-right">
            <NotificationCenter />
            <AgentStatus />
            <UserMenu me={me} />
          </div>
        </header>
        <main className="content" id="main">
          <ErrorBoundary resetKey={hash}>{page}</ErrorBoundary>
        </main>
      </div>
    </div>
  );
}

export function App() {
  const hash = useHash();
  const [prefs, setPrefsState] = useState<Prefs>(loadPrefs);
  const setPrefs = (p: Prefs) => {
    setPrefsState(p);
    try {
      localStorage.setItem("continuum.prefs", JSON.stringify(p));
    } catch {
      /* ignore */
    }
  };
  const [me, setMe] = useState<Me | null>(null);
  const [needLogin, setNeedLogin] = useState(false);
  const loadMe = () =>
    api
      .me()
      .then((m) => (setMe(m), setNeedLogin(false)))
      .catch((e: ApiError) => e.status === 401 && setNeedLogin(true));
  useEffect(() => {
    loadMe();
    const on = () => setNeedLogin(true);
    window.addEventListener("continuum:auth-required", on);
    return () => window.removeEventListener("continuum:auth-required", on);
  }, []);
  useEffect(() => {
    const root = document.documentElement;
    if (prefs.theme === "system") root.removeAttribute("data-theme");
    else root.setAttribute("data-theme", prefs.theme);
    root.lang = prefs.lang;
  }, [prefs]);
  return (
    <LangContext.Provider value={prefs.lang as Lang}>
      <UnitsContext.Provider value={prefs.units as Units}>
        <ToastHost>
          {needLogin ? <Login onDone={loadMe} /> : <Shell hash={hash} prefs={prefs} setPrefs={setPrefs} me={me} />}
        </ToastHost>
      </UnitsContext.Provider>
    </LangContext.Provider>
  );
}
