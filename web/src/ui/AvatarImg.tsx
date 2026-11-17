import { useState, type CSSProperties, type ReactNode } from "react";

/**
 * An uploaded image (organization, repository or owner avatar) with the
 * placeholder it replaces as fallback: children are shown when src is empty
 * or the image does not load. className styles both, so the size and shape
 * stay those of the placeholder; size and radius set them when the
 * placeholder has none, fill makes the image cover its parent.
 */
export function AvatarImg({ src, className, size, radius, fill, children }: {
  src?: string;
  className?: string;
  size?: number;
  radius?: number | string;
  fill?: boolean;
  children?: ReactNode;
}) {
  const [broken, setBroken] = useState("");
  if (src && broken !== src) {
    const style: CSSProperties = { objectFit: "cover", padding: 0, flex: "none" };
    if (size) Object.assign(style, { width: size, height: size });
    if (radius !== undefined) style.borderRadius = radius;
    if (fill) Object.assign(style, { width: "100%", height: "100%", borderRadius: "inherit", display: "block" });
    return <img className={className} src={src} alt="" aria-hidden="true" loading="lazy" style={style} onError={() => setBroken(src)} />;
  }
  if (children === undefined) return null;
  return (
    <span className={className} aria-hidden="true">
      {children}
    </span>
  );
}

/** The image of a repository before its name; nothing when it has none. */
export function RepoImage({ src, size = 20 }: { src?: string; size?: number }) {
  if (!src) return null;
  return (
    <span style={{ display: "inline-flex", verticalAlign: "middle", marginInlineEnd: 6 }}>
      <AvatarImg src={src} size={size} radius={Math.round(size / 4)} />
    </span>
  );
}
