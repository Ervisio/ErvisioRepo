import { useEffect, useRef, useState } from "react";
import type { EditorHandle } from "./cm.ts";

/**
 * CodeMirror editor, loaded on first use so the main bundle does not carry it.
 * Uncontrolled: `initial` is read on mount (and when `resetKey` changes).
 */
export function Editor({ initial, path, label, loadingText, onChange, resetKey }: {
  initial: string;
  path: string;
  label: string;
  loadingText: string;
  onChange: (doc: string) => void;
  resetKey?: unknown;
}) {
  const host = useRef<HTMLDivElement>(null);
  const handle = useRef<EditorHandle | null>(null);
  const change = useRef(onChange);
  const [ready, setReady] = useState(false);
  useEffect(() => {
    change.current = onChange;
  });

  useEffect(() => {
    let cancelled = false;
    void import("./cm.ts").then(({ createEditor }) => {
      if (cancelled || !host.current) return;
      handle.current = createEditor(host.current, initial, path, label, (d) => change.current(d));
      setReady(true);
    });
    return () => {
      cancelled = true;
      handle.current?.destroy();
      handle.current = null;
    };
    // The editor owns the document after mount; `path` changes go through setPath below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resetKey]);

  useEffect(() => {
    handle.current?.setPath(path);
  }, [path, ready]);

  return (
    <div className="ed">
      {!ready && <p className="ui-note acc-pad" role="status">{loadingText}</p>}
      <div ref={host} />
    </div>
  );
}
