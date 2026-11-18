import { useState } from "react";

// A poster or backdrop that degrades to the title when the image fails
// (for instance TMDB is unreachable), instead of a broken-image icon.
export function Art({ src, title, year }: { src: string; title: string; year: number }) {
  const [broken, setBroken] = useState(false);
  if (broken) {
    return (
      <span className="art-fallback">
        {title}
        <small>{year}</small>
      </span>
    );
  }
  return <img src={src} alt="" loading="lazy" draggable={false} onError={() => setBroken(true)} />;
}
