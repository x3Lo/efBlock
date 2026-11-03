import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

// cn : assemble des classes CSS conditionnelles et résout les conflits Tailwind
// (ex. "px-2 px-4" devient "px-4"). Utilisé par tous les composants shadcn.
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
