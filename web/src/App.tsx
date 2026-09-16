import { FormEvent, ReactNode, useEffect, useState } from "react";
import { NavLink, Outlet } from "react-router-dom";
import { api } from "./lib/api";

function Logo() {
  return (
    <div className="flex items-center gap-2.5">
      <div className="w-9 h-9 rounded-xl bg-gradient-to-br from-accent to-accent-cyan flex items-center justify-center shadow-glow">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="white" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round">
          <path d="M6 8l6 8 6-8" />
        </svg>
      </div>
      <div className="leading-tight">
        <div className="text-sm font-semibold text-white tracking-tight">muse-cc</div>
        <div className="text-[10px] text-slate-500 font-mono">anthropic ⇄ openai</div>
      </div>
    </div>
  );
}

const NAV = [
  { to: "/", label: "Dashboard", icon: DashIcon, end: true },
  { to: "/logs", label: "Request Logs", icon: ListIcon },
  { to: "/keys", label: "API Keys", icon: KeyNavIcon },
  { to: "/models", label: "Model Routes", icon: CubeIcon },
  { to: "/settings", label: "Settings", icon: GearIcon },
];

function NavItem({
  to,
  label,
  icon: Icon,
  end,
}: {
  to: string;
  label: string;
  icon: (p: { className?: string }) => JSX.Element;
  end?: boolean;
}) {
  return (
    <NavLink
      to={to}
      end={end}
      className={({ isActive }) =>
        `group flex items-center gap-3 rounded-xl px-3 py-2.5 text-sm font-medium transition-all ${
          isActive
            ? "bg-white/[0.06] text-white border border-white/[0.06] shadow-card"
            : "text-slate-400 hover:text-slate-200 hover:bg-white/[0.03] border border-transparent"
        }`
      }
    >
      <Icon className="w-[18px] h-[18px]" />
      {label}
    </NavLink>
  );
}

function StatusBar() {
  const [online, setOnline] = useState<boolean | null>(null);
  useEffect(() => {
    let alive = true;
    const tick = async () => {
      try {
        const r = await fetch("/api/health");
        setOnline(r.ok);
      } catch {
        setOnline(false);
      }
      return () => {
        alive = false;
      };
    };
    tick();
    const id = setInterval(tick, 15000);
    return () => {
      clearInterval(id);
      alive = false;
    };
  }, []);

  const tone =
    online === null
      ? "bg-slate-500"
      : online
      ? "bg-accent-green"
      : "bg-accent-red";
  const text =
    online === null ? "Connecting…" : online ? "Proxy online" : "Proxy offline";

  return (
    <div className="flex items-center gap-2 text-xs text-slate-400">
      <span className={`relative flex h-2 w-2`}>
        <span
          className={`absolute inline-flex h-full w-full rounded-full ${tone} opacity-60 animate-pulse-dot`}
        />
        <span className={`relative inline-flex rounded-full h-2 w-2 ${tone}`} />
      </span>
      {text}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Authentication guard
// ---------------------------------------------------------------------------

/** Wraps the whole app; shows login page when the panel requires auth. */
export function AuthGuard({ children }: { children: ReactNode }) {
  const [state, setState] = useState<"loading" | "open" | "needlogin" | "authed">("loading");

  useEffect(() => {
    api
      .checkAuth()
      .then((r) => {
        if (!r.need_auth) setState("open");
        else if (r.authenticated) setState("authed");
        else setState("needlogin");
      })
      .catch(() => setState("needlogin"));
  }, []);

  if (state === "loading") {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <div className="w-6 h-6 rounded-full border-2 border-accent border-t-transparent animate-spin" />
      </div>
    );
  }

  if (state === "needlogin") {
    return <LoginPage onLogin={() => setState("authed")} />;
  }

  return <>{children}</>;
}

function LoginPage({ onLogin }: { onLogin: () => void }) {
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!password) return;
    setLoading(true);
    setError("");
    try {
      await api.login(password);
      onLogin();
    } catch {
      setError("Wrong password, please try again");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen flex items-center justify-center px-4">
      <div className="w-full max-w-sm">
        <div className="flex justify-center mb-8">
          <Logo />
        </div>
        <div className="rounded-2xl border border-white/[0.06] bg-ink-950/60 backdrop-blur-xl shadow-card p-8">
          <h2 className="text-lg font-semibold text-white mb-1 text-center">Admin login</h2>
          <p className="text-sm text-slate-500 text-center mb-6">Enter the panel password to continue</p>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <div className="flex flex-col gap-1.5">
              <label className="text-xs text-slate-400 font-medium">Panel password</label>
              <input
                type="password"
                autoFocus
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••••"
                className="w-full rounded-xl border border-white/[0.08] bg-white/[0.04] px-3 py-2.5 text-sm text-white placeholder-slate-600 focus:outline-none focus:ring-1 focus:ring-accent"
              />
            </div>
            {error && (
              <p className="text-xs text-accent-red text-center">{error}</p>
            )}
            <button
              type="submit"
              disabled={loading || !password}
              className="mt-1 w-full rounded-xl bg-accent hover:bg-accent/90 disabled:opacity-50 disabled:cursor-not-allowed px-4 py-2.5 text-sm font-medium text-white transition-colors"
            >
              {loading ? "Verifying…" : "Log in"}
            </button>
          </form>
        </div>
        <p className="text-center text-xs text-slate-600 mt-4">
          You can change or clear the panel password on the Settings page
        </p>
      </div>
    </div>
  );
}

function LogoutButton() {
  const [pending, setPending] = useState(false);

  const handleLogout = async () => {
    setPending(true);
    try {
      await api.logout();
    } finally {
      window.location.reload();
    }
  };

  return (
    <button
      onClick={handleLogout}
      disabled={pending}
      className="flex items-center gap-2 text-xs text-slate-500 hover:text-slate-300 transition-colors disabled:opacity-50"
      title="Log out"
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
        <polyline points="16 17 21 12 16 7" />
        <line x1="21" y1="12" x2="9" y2="12" />
      </svg>
      Log out
    </button>
  );
}

export default function App() {
  return (
    <div className="min-h-screen flex">
      {/* Sidebar */}
      <aside className="hidden md:flex w-64 shrink-0 flex-col border-r border-white/[0.05] bg-ink-950/40 backdrop-blur-xl">
        <div className="px-5 py-5">
          <Logo />
        </div>
        <nav className="px-3 flex flex-col gap-1">
          {NAV.map((n) => (
            <NavItem key={n.to} {...n} />
          ))}
        </nav>
        <div className="mt-auto px-5 py-4 border-t border-white/[0.05] flex flex-col gap-2">
          <StatusBar />
          <LogoutButton />
        </div>
      </aside>

      {/* Mobile top bar */}
      <div className="md:hidden fixed top-0 inset-x-0 z-20 bg-ink-950/80 backdrop-blur-xl border-b border-white/[0.05]">
        <div className="flex items-center justify-between px-4 py-3">
          <Logo />
          <StatusBar />
        </div>
        <nav className="flex px-2 pb-2 gap-1 overflow-x-auto">
          {NAV.map((n) => (
            <NavItem key={n.to} {...n} />
          ))}
        </nav>
      </div>

      {/* Content */}
      <main className="flex-1 min-w-0 px-5 md:px-8 py-6 md:py-8 pt-24 md:pt-8 max-w-[1400px] mx-auto w-full">
        <Outlet />
      </main>
    </div>
  );
}

// --- Icons (inline SVGs, no extra deps) ---
type IconProps = { className?: string };
function KeyNavIcon({ className }: IconProps) {
  return (
    <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <circle cx="8" cy="15" r="4" />
      <path d="M10.85 12.15L19 4M18 5l2 2M15 8l2 2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}
function DashIcon({ className }: IconProps) {
  return (
    <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <rect x="3" y="3" width="7" height="9" rx="1.5" />
      <rect x="14" y="3" width="7" height="5" rx="1.5" />
      <rect x="14" y="12" width="7" height="9" rx="1.5" />
      <rect x="3" y="16" width="7" height="5" rx="1.5" />
    </svg>
  );
}
function ListIcon({ className }: IconProps) {
  return (
    <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="M8 6h13M8 12h13M8 18h13" strokeLinecap="round" />
      <circle cx="3.5" cy="6" r="1.3" fill="currentColor" />
      <circle cx="3.5" cy="12" r="1.3" fill="currentColor" />
      <circle cx="3.5" cy="18" r="1.3" fill="currentColor" />
    </svg>
  );
}
function CubeIcon({ className }: IconProps) {
  return (
    <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="M12 2l9 5v10l-9 5-9-5V7l9-5z" strokeLinejoin="round" />
      <path d="M3 7l9 5 9-5M12 12v10" />
    </svg>
  );
}
function GearIcon({ className }: IconProps) {
  return (
    <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-2.82 1.17V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 8 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 3 15a1.65 1.65 0 0 0-1.51-1H1a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 3 8.6a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 8 4.6h.09A1.65 1.65 0 0 0 9 3.09V3a2 2 0 1 1 4 0v.09c0 .67.39 1.27 1 1.51.61.24 1.31.11 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9c.24-.61.84-1 1.51-1H21a2 2 0 1 1 0 4h-.09c-.67 0-1.27.39-1.51 1z" />
    </svg>
  );
}

export function PageHeader({ title, desc, actions }: { title: string; desc?: string; actions?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-4 mb-6">
      <div>
        <h1 className="text-xl font-semibold text-white tracking-tight">{title}</h1>
        {desc && <p className="text-sm text-slate-500 mt-1">{desc}</p>}
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  );
}
