import * as React from "react";

import { cn } from "@/lib/utils";

// Carte : un bloc de contenu sur fond légèrement différent de la page.
function Card({ className, ...props }: React.ComponentProps<"div">) {
  return <div data-slot="card" className={cn("rounded-xl border bg-card text-card-foreground", className)} {...props} />;
}

function CardHeader({
  title,
  icon: Icon,
  actions,
  className,
}: {
  title: React.ReactNode;
  icon?: React.ComponentType<{ className?: string }>;
  actions?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex items-center justify-between gap-3 px-4 pt-4 pb-2", className)}>
      <h2 className="flex items-center gap-2 text-sm font-semibold">
        {Icon && <Icon className="size-4 text-muted-foreground" aria-hidden />}
        {title}
      </h2>
      {actions}
    </div>
  );
}

export { Card, CardHeader };
