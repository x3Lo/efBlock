"use client";

import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts";

import { ChartConfig, ChartContainer, ChartLegend, ChartTooltip, ChartTooltipContent } from "@/components/ui/chart";
import { Bucket, nf } from "@/lib/api";

const config = {
  allowed: { label: "Autorisées", color: "var(--chart-1)" },
  blocked: { label: "Bloquées", color: "var(--chart-2)" },
} satisfies ChartConfig;

// Histogramme empilé : requêtes autorisées + requêtes bloquées par tranche de temps.
export default function ActivityChart({ data, hours }: { data: Bucket[]; hours: number }) {
  const label = (iso: string) =>
    new Date(iso).toLocaleString(
      "fr-FR",
      hours > 24
        ? { weekday: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }
        : { hour: "2-digit", minute: "2-digit" },
    );

  const rows = data.map((b) => ({
    start: b.start,
    label: label(b.start),
    allowed: b.total - b.blocked,
    blocked: b.blocked,
  }));

  if (rows.length === 0) return <p className="py-8 text-muted-foreground">Pas encore de données.</p>;

  return (
    <div>
      <ChartContainer config={config} className="aspect-auto h-52 w-full justify-start">
        <BarChart data={rows} margin={{ top: 4, right: 0, bottom: 0, left: 0 }} barCategoryGap="12%">
          <CartesianGrid vertical={false} />
          <XAxis dataKey="label" tickLine={false} axisLine={false} minTickGap={36} tickMargin={6} />
          <YAxis width={36} tickLine={false} axisLine={false} allowDecimals={false} />
          <ChartTooltip
            cursor
            content={
              <ChartTooltipContent
                labelFormatter={(_, p) => label(String(p[0]?.payload?.start))}
                valueFormatter={(v) => nf.format(Number(v))}
              />
            }
          />
          <Bar dataKey="allowed" stackId="a" fill="var(--color-allowed)" isAnimationActive={false} />
          <Bar dataKey="blocked" stackId="a" fill="var(--color-blocked)" isAnimationActive={false} />
        </BarChart>
      </ChartContainer>
      <ChartLegend config={config} keys={["allowed", "blocked"]} className="mt-2" />
    </div>
  );
}
