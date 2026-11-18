import { useState } from "react";

export function Art({ src, title, year }: { src: string; title: string; year: number }) {
  const [brokenSrc, setBrokenSrc] = useState<string | null>(null);
  if (brokenSrc === src) {
    return (
      <span className="art-fallback">
        {title}
        <small>{year}</small>
      </span>
    );
  }
  return <img src={src} alt="" loading="lazy" draggable={false} onError={() => setBrokenSrc(src)} />;
}
