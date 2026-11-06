"use client";

import { useEffect, useState } from "react";

import { Button } from "@/components/ui/button";

// Bouton de suppression en deux temps : le premier clic demande confirmation,
// le second exécute. Revient à l'état normal au bout de 3 secondes.
export default function ConfirmButton({
  onConfirm,
  label = "Retirer",
  ariaLabel,
  variant = "ghost",
}: {
  onConfirm: () => void;
  label?: string;
  ariaLabel?: string;
  variant?: "ghost" | "default" | "outline"; // aspect avant confirmation
}) {
  const [armed, setArmed] = useState(false);

  useEffect(() => {
    if (!armed) return;
    const id = setTimeout(() => setArmed(false), 3000);
    return () => clearTimeout(id);
  }, [armed]);

  return (
    <Button
      variant={armed ? "destructive" : variant}
      size="sm"
      aria-label={ariaLabel}
      onClick={() => (armed ? onConfirm() : setArmed(true))}
    >
      {armed ? "Confirmer ?" : label}
    </Button>
  );
}
