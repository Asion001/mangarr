import { useState } from "react";
import clsx from "clsx";
import { BookImage } from "lucide-react";

/**
 * Cover shows a series or manga cover in a fixed-ratio box. Images far from
 * the usual 2:3 shape (banners, square art) are shown whole over a blurred
 * copy instead of being cropped.
 */
export function Cover({ src, alt, className }: { src?: string; alt: string; className?: string }) {
  const [failed, setFailed] = useState(false);
  const [odd, setOdd] = useState(false);
  return (
    <div className={clsx("relative overflow-hidden rounded-md bg-panel-2", className)}>
      {src && !failed ? (
        <>
          {odd && <img src={src} alt="" aria-hidden referrerPolicy="no-referrer" className="absolute inset-0 h-full w-full scale-110 object-cover opacity-50 blur-md" />}
          <img
            src={src}
            alt={alt}
            loading="lazy"
            decoding="async"
            // some providers (MangaDex) answer a link from another site with a placeholder
            referrerPolicy="no-referrer"
            onError={() => setFailed(true)}
            onLoad={(e) => {
              const { naturalWidth: w, naturalHeight: h } = e.currentTarget;
              // 2:3 is 0.67; allow the usual variation (0.5 to 0.85)
              setOdd(h > 0 && (w / h < 0.5 || w / h > 0.85));
            }}
            className={clsx("relative h-full w-full", odd ? "object-contain" : "object-cover")}
          />
        </>
      ) : (
        <div className="flex h-full w-full items-center justify-center text-muted">
          <BookImage className="size-8" />
        </div>
      )}
    </div>
  );
}
