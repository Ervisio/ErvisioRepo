import { useMemo } from "react";
import { encodeQR, qrPath } from "../../lib/qr.ts";

/** Inline SVG QR code with a quiet zone; always black on white so scanners can read it. */
export function QrCode({ value, label }: { value: string; label: string }) {
  const matrix = useMemo(() => {
    try {
      return encodeQR(value);
    } catch {
      return undefined;
    }
  }, [value]);
  if (!matrix) return null;
  const quiet = 4;
  const size = matrix.length + quiet * 2;
  return (
    <svg className="acc-qr" viewBox={`0 0 ${size} ${size}`} role="img" aria-label={label} shapeRendering="crispEdges">
      <rect width={size} height={size} fill="#fff" />
      <path transform={`translate(${quiet} ${quiet})`} d={qrPath(matrix)} fill="#000" />
    </svg>
  );
}
