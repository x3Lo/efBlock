import * as React from "react";

import { cn } from "@/lib/utils";

// Liste déroulante native, habillée comme les autres champs : plus légère et plus
// accessible au clavier et sur mobile que le composant Radix, pour un choix de 2 ou 3 valeurs.
function Select({ className, ...props }: React.ComponentProps<"select">) {
  return (
    <select
      data-slot="select"
      className={cn("h-8 rounded-md border border-input bg-card px-2 text-sm transition-colors", className)}
      {...props}
    />
  );
}

export { Select };
