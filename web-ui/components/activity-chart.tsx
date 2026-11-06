"use client";

import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";

import { ChartConfig, ChartContainer, ChartLegend, ChartTooltip, ChartTooltipContent } from "@/components/ui/chart";
import { Bucket, nf } from "@/lib/api";

// Axe vertical court (« 6 k » plutôt que « 6000 ») : les grands nombres restent lisibles et ne sont plus coupés.
const compact = new Intl.NumberFormat("fr-FR", { notation: "compact", maximumFractionDigits: 1 });

// Deux courbes superposées : toutes les requêtes (couleur de l'interface) et, par-dessus, celles
// qui ont été bloquées (orange). La part bloquée se lit directement comme une partie du total.
const config = {
  total: { label: "Toutes les requêtes", color: "var(--primary)" },
  blocked: { label: "Bloquées", color: "var(--blocked)" },
} satisfies ChartConfig;

export default function ActivityChart({ data, hours }: { data: Bucket[]; hours: number }) {
  const label = (iso: string) =>
    new Date(iso).toLocaleString(
      "fr-FR",
      hours > 24
        ? { weekday: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }
        : { hour: "2-digit", minute: "2-digit" },
    );

  const rows = data.map((b) => ({ start: b.start, label: label(b.start), total: b.total, blocked: b.blocked }));

  if (rows.length === 0) return <p className="py-8 text-muted-foreground">Pas encore de données.</p>;

  return (
    <div>
      <ChartContainer config={config} className="aspect-auto h-60 w-full justify-start">
        <AreaChart data={rows} margin={{ top: 8, right: 8, bottom: 0, left: 4 }}>
          <defs>
            <linearGradient id="fill-total" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="var(--color-total)" stopOpacity={0.35} />
              <stop offset="100%" stopColor="var(--color-total)" stopOpacity={0.02} />
            </linearGradient>
            <linearGradient id="fill-blocked" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="var(--color-blocked)" stopOpacity={0.55} />
              <stop offset="100%" stopColor="var(--color-blocked)" stopOpacity={0.05} />
            </linearGradient>
          </defs>
          <CartesianGrid vertical={false} strokeDasharray="3 4" strokeOpacity={0.5} />
          <XAxis dataKey="label" tickLine={false} axisLine={false} minTickGap={36} tickMargin={8} />
          <YAxis width={44} tickLine={false} axisLine={false} allowDecimals={false} tickFormatter={(v: number) => compact.format(v)} />
          <ChartTooltip
            cursor={{ strokeOpacity: 0.4 }}
            content={
              <ChartTooltipContent
                labelFormatter={(_, p) => label(String(p[0]?.payload?.start))}
                valueFormatter={(v) => nf.format(Number(v))}
              />
            }
          />
          <Area
            dataKey="total"
            type="monotone"
            stroke="var(--color-total)"
            strokeWidth={2}
            fill="url(#fill-total)"
            dot={false}
            activeDot={{ r: 4, strokeWidth: 0 }}
            isAnimationActive={false}
          />
          <Area
            dataKey="blocked"
            type="monotone"
            stroke="var(--color-blocked)"
            strokeWidth={2}
            fill="url(#fill-blocked)"
            dot={false}
            activeDot={{ r: 4, strokeWidth: 0 }}
            isAnimationActive={false}
          />
        </AreaChart>
      </ChartContainer>
      <ChartLegend config={config} keys={["total", "blocked"]} className="mt-2" />
    </div>
  );
}
