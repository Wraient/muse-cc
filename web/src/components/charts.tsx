import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { HourPoint } from "../lib/api";
import { fmtNum } from "../lib/format";

const tooltipStyle = {
  background: "rgba(15,20,32,0.95)",
  border: "1px solid rgba(255,255,255,0.08)",
  borderRadius: 12,
  fontSize: 12,
  color: "#e2e8f0",
  backdropFilter: "blur(8px)",
  boxShadow: "0 8px 30px -12px rgba(0,0,0,0.6)",
};

const labelStyle = { color: "#94a3b8", fontSize: 11 };

export function RequestsAreaChart({ data }: { data: HourPoint[] }) {
  const mapped = data.map((d) => ({
    ...d,
    label: new Date(d.hour * 1000).toLocaleTimeString([], {
      hour: "2-digit",
      minute: "2-digit",
    }),
  }));
  return (
    <ResponsiveContainer width="100%" height={240}>
      <AreaChart data={mapped} margin={{ top: 8, right: 8, left: -16, bottom: 0 }}>
        <defs>
          <linearGradient id="gReq" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="#7c5cff" stopOpacity={0.5} />
            <stop offset="100%" stopColor="#7c5cff" stopOpacity={0} />
          </linearGradient>
          <linearGradient id="gErr" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="#f87171" stopOpacity={0.4} />
            <stop offset="100%" stopColor="#f87171" stopOpacity={0} />
          </linearGradient>
        </defs>
        <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.05)" vertical={false} />
        <XAxis dataKey="label" tick={{ fill: "#64748b", fontSize: 11 }} axisLine={false} tickLine={false} minTickGap={24} />
        <YAxis tick={{ fill: "#64748b", fontSize: 11 }} axisLine={false} tickLine={false} allowDecimals={false} width={40} />
        <Tooltip contentStyle={tooltipStyle} labelStyle={labelStyle} cursor={{ stroke: "rgba(255,255,255,0.1)" }} />
        <Area type="monotone" dataKey="requests" name="Requests" stroke="#7c5cff" strokeWidth={2} fill="url(#gReq)" />
        <Area type="monotone" dataKey="errors" name="Errors" stroke="#f87171" strokeWidth={2} fill="url(#gErr)" />
      </AreaChart>
    </ResponsiveContainer>
  );
}

export function TokensAreaChart({ data }: { data: HourPoint[] }) {
  const mapped = data.map((d) => ({
    ...d,
    label: new Date(d.hour * 1000).toLocaleTimeString([], {
      hour: "2-digit",
      minute: "2-digit",
    }),
  }));
  return (
    <ResponsiveContainer width="100%" height={240}>
      <AreaChart data={mapped} margin={{ top: 8, right: 8, left: -8, bottom: 0 }}>
        <defs>
          <linearGradient id="gIn" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="#22d3ee" stopOpacity={0.45} />
            <stop offset="100%" stopColor="#22d3ee" stopOpacity={0} />
          </linearGradient>
          <linearGradient id="gOut" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="#34d399" stopOpacity={0.45} />
            <stop offset="100%" stopColor="#34d399" stopOpacity={0} />
          </linearGradient>
        </defs>
        <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.05)" vertical={false} />
        <XAxis dataKey="label" tick={{ fill: "#64748b", fontSize: 11 }} axisLine={false} tickLine={false} minTickGap={24} />
        <YAxis tick={{ fill: "#64748b", fontSize: 11 }} axisLine={false} tickLine={false} width={48} tickFormatter={fmtNum} />
        <Tooltip contentStyle={tooltipStyle} labelStyle={labelStyle} cursor={{ stroke: "rgba(255,255,255,0.1)" }} formatter={(v: number) => fmtNum(v)} />
        <Area type="monotone" dataKey="input_tokens" name="Input" stroke="#22d3ee" strokeWidth={2} fill="url(#gIn)" />
        <Area type="monotone" dataKey="output_tokens" name="Output" stroke="#34d399" strokeWidth={2} fill="url(#gOut)" />
      </AreaChart>
    </ResponsiveContainer>
  );
}
