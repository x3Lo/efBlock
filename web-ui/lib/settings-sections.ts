import { ListFilter, Lock, Server, SlidersHorizontal } from "lucide-react";
import { useEffect, useState } from "react";

// Sections de la page Réglages. Elles apparaissent en sous-menu de « Réglages » dans la barre
// latérale ; la section affichée est indiquée après le # de l'adresse (/settings/#dns).
export const settingsSections = [
  { id: "general", label: "Général", icon: SlidersHorizontal },
  { id: "dns", label: "DNS", icon: Server },
  { id: "filtrage", label: "Filtrage et cache", icon: ListFilter },
  { id: "acces", label: "Accès", icon: Lock },
] as const;

export type SettingsSectionId = (typeof settingsSections)[number]["id"];

export function sectionFromHash(hash: string): SettingsSectionId {
  const id = hash.replace(/^#/, "");
  return (settingsSections.find((s) => s.id === id)?.id ?? "general") as SettingsSectionId;
}

// useHash suit la partie de l'adresse après #. `dep` force une relecture (changement de page).
export function useHash(dep?: unknown) {
  const [hash, setHash] = useState("");
  useEffect(() => {
    const read = () => setHash(window.location.hash);
    read();
    window.addEventListener("hashchange", read);
    return () => window.removeEventListener("hashchange", read);
  }, [dep]);
  return hash;
}
