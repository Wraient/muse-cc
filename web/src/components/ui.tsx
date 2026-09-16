import { ReactNode } from "react";

export function Card({
  children,
  className = "",
}: {
  children: ReactNode;
  className?: string;
}) {
  return <div className={`card ${className}`}>{children}</div>;
}

export function StatCard({
  label,
  value,
  sub,
  accent = "text-white",
  icon,
}: {
  label: string;
  value: ReactNode;
  sub?: ReactNode;
  accent?: string;
  icon?: ReactNode;
}) {
  return (
    <div className="card relative overflow-hidden group">
      <div className="absolute -right-6 -top-6 w-24 h-24 rounded-full bg-accent/10 blur-2xl opacity-0 group-hover:opacity-100 transition-opacity" />
      <div className="flex items-start justify-between">
        <div>
          <div className="text-xs uppercase tracking-wider text-slate-500 font-medium">
            {label}
          </div>
          <div className={`stat-value mt-2 ${accent}`}>{value}</div>
          {sub && <div className="text-xs text-slate-500 mt-1">{sub}</div>}
        </div>
        {icon && <div className="text-slate-600">{icon}</div>}
      </div>
    </div>
  );
}

export function Badge({
  children,
  tone = "default",
}: {
  children: ReactNode;
  tone?: "default" | "green" | "amber" | "red" | "cyan" | "violet";
}) {
  const tones: Record<string, string> = {
    default: "bg-white/[0.05] border-white/[0.06] text-slate-300",
    green: "bg-accent-green/10 border-accent-green/20 text-accent-green",
    amber: "bg-accent-amber/10 border-accent-amber/20 text-accent-amber",
    red: "bg-accent-red/10 border-accent-red/20 text-accent-red",
    cyan: "bg-accent-cyan/10 border-accent-cyan/20 text-accent-cyan",
    violet: "bg-accent/10 border-accent/20 text-accent-glow",
  };
  return (
    <span className={`chip border ${tones[tone]}`}>
      {children}
    </span>
  );
}

export function EmptyState({
  title,
  hint,
}: {
  title: string;
  hint?: string;
}) {
  return (
    <div className="flex flex-col items-center justify-center py-16 text-center">
      <div className="w-12 h-12 rounded-2xl bg-white/[0.04] border border-white/[0.06] flex items-center justify-center mb-4">
        <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-slate-500">
          <path d="M3 3v18h18" />
          <path d="M7 14l3-3 3 2 4-5" />
        </svg>
      </div>
      <div className="text-slate-300 font-medium">{title}</div>
      {hint && <div className="text-sm text-slate-500 mt-1 max-w-sm">{hint}</div>}
    </div>
  );
}

export function Spinner({ className = "" }: { className?: string }) {
  return (
    <svg className={`animate-spin ${className}`} width="16" height="16" viewBox="0 0 24 24" fill="none">
      <circle cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="3" opacity="0.2" />
      <path d="M22 12a10 10 0 0 1-10 10" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
    </svg>
  );
}
