"use client";

import { useEffect, useState } from "react";

import { Textarea } from "@/components/ui/textarea";

export const parseLines = (s: string) =>
  s
    .split("\n")
    .map((x) => x.trim())
    .filter(Boolean);

// Zone de texte « une valeur par ligne » qui garde ce que tu tapes (y compris les lignes vides en cours).
export default function ListField({
  value,
  onChange,
  rows = 3,
  placeholder,
  label,
}: {
  value: string[];
  onChange: (v: string[]) => void;
  rows?: number;
  placeholder?: string;
  label: string;
}) {
  const [text, setText] = useState(value.join("\n"));
  useEffect(() => {
    if (JSON.stringify(parseLines(text)) !== JSON.stringify(value)) setText(value.join("\n"));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value]);
  return (
    <Textarea
      aria-label={label}
      rows={rows}
      value={text}
      placeholder={placeholder}
      spellCheck={false}
      autoCapitalize="off"
      onChange={(e) => {
        setText(e.target.value);
        onChange(parseLines(e.target.value));
      }}
    />
  );
}
