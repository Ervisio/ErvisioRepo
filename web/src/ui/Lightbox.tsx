import * as RDialog from "@radix-ui/react-dialog";
import { useEffect, useState, type ReactNode } from "react";
import { useT } from "../i18n/useT.ts";
import { Icon } from "./Icon.tsx";

type Shown = { src: string; alt: string };

/** Smaller images (icons, emoji-sized badges) are not worth enlarging. */
const MIN_SIZE = 40;

/** The image a click or key press should enlarge: Markdown images outside links and images marked data-zoom. */
function zoomTarget(t: EventTarget | null): HTMLImageElement | null {
  if (!(t instanceof HTMLImageElement)) return null;
  if (t.dataset.zoom !== undefined) return t;
  if (!t.closest(".md") || t.closest("a")) return null;
  return Math.max(t.naturalWidth, t.width) >= MIN_SIZE ? t : null;
}

/** Click-to-enlarge viewer for images in Markdown and for avatars marked data-zoom. Mount once. */
export function LightboxProvider({ children }: { children: ReactNode }) {
  const [shown, setShown] = useState<Shown | null>(null);
  useEffect(() => {
    const open = (img: HTMLImageElement) => setShown({ src: img.currentSrc || img.src, alt: img.alt });
    const onClick = (e: MouseEvent) => {
      if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.defaultPrevented) return;
      const img = zoomTarget(e.target);
      if (!img) return;
      e.preventDefault();
      open(img);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Enter" && e.key !== " ") return;
      const img = zoomTarget(e.target);
      if (!img || img.dataset.zoom === undefined) return;
      e.preventDefault();
      open(img);
    };
    document.addEventListener("click", onClick);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("click", onClick);
      document.removeEventListener("keydown", onKey);
    };
  }, []);
  return (
    <>
      {children}
      {shown && <Viewer image={shown} onClose={() => setShown(null)} />}
    </>
  );
}

function Viewer({ image, onClose }: { image: Shown; onClose: () => void }) {
  const t = useT("ui");
  const [actual, setActual] = useState(false);
  return (
    <RDialog.Root open onOpenChange={(o) => !o && onClose()}>
      <RDialog.Portal>
        <RDialog.Overlay className="lb-ov" />
        <RDialog.Content className="lb" aria-describedby={undefined} onClick={(e) => e.target === e.currentTarget && onClose()}>
          <RDialog.Title className="sr-only">{image.alt || t("lightbox.title")}</RDialog.Title>
          <div className="lb-bar">
            <button type="button" className="lb-btn" onClick={() => setActual((a) => !a)} aria-pressed={actual} title={actual ? t("lightbox.fit") : t("lightbox.actual")}>
              <Icon name="search" />
              <span>{actual ? t("lightbox.fit") : t("lightbox.actual")}</span>
            </button>
            <a className="lb-btn" href={image.src} target="_blank" rel="noopener noreferrer" title={t("lightbox.original")}>
              <Icon name="link" />
              <span>{t("lightbox.original")}</span>
            </a>
            <RDialog.Close className="lb-btn" aria-label={t("dialog.close")} title={t("dialog.close")}>
              <Icon name="x" />
            </RDialog.Close>
          </div>
          <div className={actual ? "lb-stage lb-actual" : "lb-stage"} onClick={(e) => e.target === e.currentTarget && onClose()}>
            <img src={image.src} alt={image.alt} referrerPolicy="no-referrer" onClick={() => setActual((a) => !a)} />
          </div>
          {image.alt && <p className="lb-cap">{image.alt}</p>}
        </RDialog.Content>
      </RDialog.Portal>
    </RDialog.Root>
  );
}
