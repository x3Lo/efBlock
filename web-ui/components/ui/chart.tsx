"use client";

import * as React from "react";
import * as RechartsPrimitive from "recharts";

import { cn } from "@/lib/utils";

// Configuration d'un graphique : pour chaque série, son libellé et sa couleur.
// ChartContainer expose la couleur sous la forme d'une variable CSS --color-<série>,
// que les barres et la légende utilisent (ex. fill="var(--color-blocked)").
export type ChartConfig = Record<string, { label?: React.ReactNode; color?: string }>;

const ChartContext = React.createContext<{ config: ChartConfig } | null>(null);

function useChart() {
  const ctx = React.useContext(ChartContext);
  if (!ctx) throw new Error("useChart doit être utilisé dans un <ChartContainer />");
  return ctx;
}

function ChartContainer({
  id,
  className,
  children,
  config,
  ...props
}: React.ComponentProps<"div"> & {
  config: ChartConfig;
  children: React.ReactElement;
}) {
  const uniqueId = React.useId();
  const chartId = `chart-${id || uniqueId.replace(/:/g, "")}`;

  return (
    <ChartContext.Provider value={{ config }}>
      <div
        data-slot="chart"
        data-chart={chartId}
        className={cn(
          "flex aspect-video justify-center text-xs",
          "[&_.recharts-cartesian-axis-tick_text]:fill-muted-foreground",
          "[&_.recharts-cartesian-grid_line]:stroke-border/70",
          "[&_.recharts-rectangle.recharts-tooltip-cursor]:fill-muted/60",
          "[&_.recharts-surface]:outline-hidden",
          className,
        )}
        {...props}
      >
        <ChartStyle id={chartId} config={config} />
        <RechartsPrimitive.ResponsiveContainer>{children}</RechartsPrimitive.ResponsiveContainer>
      </div>
    </ChartContext.Provider>
  );
}

function ChartStyle({ id, config }: { id: string; config: ChartConfig }) {
  const colored = Object.entries(config).filter(([, c]) => c.color);
  if (colored.length === 0) return null;
  const css = `[data-chart=${id}] {\n${colored.map(([k, c]) => `  --color-${k}: ${c.color};`).join("\n")}\n}`;
  return <style dangerouslySetInnerHTML={{ __html: css }} />;
}

const ChartTooltip = RechartsPrimitive.Tooltip;

type TooltipItem = {
  dataKey?: string | number;
  name?: string | number;
  value?: number | string;
  payload?: Record<string, unknown>;
};

function ChartTooltipContent({
  active,
  payload,
  label,
  labelFormatter,
  valueFormatter,
  className,
}: {
  active?: boolean;
  payload?: TooltipItem[];
  label?: unknown;
  labelFormatter?: (label: unknown, payload: TooltipItem[]) => React.ReactNode;
  valueFormatter?: (value: number | string) => string;
  className?: string;
}) {
  const { config } = useChart();
  if (!active || !payload?.length) return null;

  return (
    <div className={cn("min-w-32 rounded-md border bg-card px-2.5 py-2 text-xs", className)}>
      <div className="mb-1 font-medium">{labelFormatter ? labelFormatter(label, payload) : String(label ?? "")}</div>
      <div className="grid gap-1">
        {payload.map((item) => {
          const key = String(item.dataKey ?? item.name);
          return (
            <div key={key} className="flex items-center gap-2">
              <span className="size-2 shrink-0 rounded-[2px]" style={{ background: `var(--color-${key})` }} />
              <span className="text-muted-foreground">{config[key]?.label ?? key}</span>
              <span className="ml-auto pl-4 font-mono tabular-nums">
                {item.value === undefined ? "" : valueFormatter ? valueFormatter(item.value) : String(item.value)}
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}

// Légende simple : une pastille de couleur + le libellé de chaque série.
// Elle reçoit la config en paramètre : elle se place à côté du graphique, pas dedans.
function ChartLegend({ config, keys, className }: { config: ChartConfig; keys: string[]; className?: string }) {
  return (
    <div className={cn("flex gap-4 text-xs text-muted-foreground", className)}>
      {keys.map((k) => (
        <span key={k} className="flex items-center gap-1.5">
          <span className="size-2 rounded-[2px]" style={{ background: config[k]?.color }} />
          {config[k]?.label ?? k}
        </span>
      ))}
    </div>
  );
}

export { ChartContainer, ChartTooltip, ChartTooltipContent, ChartLegend };
