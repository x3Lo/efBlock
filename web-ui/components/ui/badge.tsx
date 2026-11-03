import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";

import { cn } from "@/lib/utils";

const badgeVariants = cva(
  "inline-flex items-center rounded-sm border px-1.5 py-px text-xs font-medium whitespace-nowrap",
  {
    variants: {
      variant: {
        default: "border-border text-muted-foreground",
        blocked: "border-blocked/50 text-blocked",
        ok: "border-ok/50 text-ok",
        warn: "border-warn/60 text-warn",
        destructive: "border-destructive/50 text-destructive",
      },
    },
    defaultVariants: { variant: "default" },
  },
);

function Badge({ className, variant, ...props }: React.ComponentProps<"span"> & VariantProps<typeof badgeVariants>) {
  return <span data-slot="badge" className={cn(badgeVariants({ variant }), className)} {...props} />;
}

export { Badge, badgeVariants };
