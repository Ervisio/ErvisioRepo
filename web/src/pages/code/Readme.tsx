import { useRouter } from "@tanstack/react-router";
import type { MouseEvent } from "react";
import { rawUrl, useReadme } from "../../api/code.ts";
import { useT } from "../../i18n/useT.ts";
import { joinPath, Markdown, type MarkdownContext } from "../../lib/markdown.tsx";
import { Card } from "../../ui/index.ts";
import { dirOf, Gate } from "./common.tsx";

export const isMarkdown = (path: string) => /\.(md|markdown|mdown|mkd)$/i.test(path);

/**
 * Link and image resolution for Markdown stored at `path`: relative targets open the
 * file (or directory, with a trailing slash) at the same ref; images load from /raw.
 */
export function useMarkdownContext(owner: string, name: string, refName: string, path: string): MarkdownContext {
  const router = useRouter();
  const dir = dirOf(path);
  const split = (href: string) => {
    const i = href.search(/[?#]/);
    return i < 0 ? [href, ""] : [href.slice(0, i), href.slice(i)];
  };
  return {
    resolveLink: (href) => {
      const [target = "", rest = ""] = split(href);
      if (target === "") return href;
      const p = joinPath(dir, decodeURIComponentSafe(target));
      const hash = rest.startsWith("#") ? rest.slice(1) : undefined;
      const loc = target.endsWith("/")
        ? router.buildLocation({ to: "/$owner/$name", params: { owner, name }, search: { ref: refName, path: p || undefined } })
        : router.buildLocation({ to: "/$owner/$name/blob", params: { owner, name }, search: { ref: refName, path: p }, hash });
      return loc.href;
    },
    resolveImage: (src) => {
      const [target = ""] = split(src);
      return target ? rawUrl(owner, name, refName, joinPath(dir, decodeURIComponentSafe(target))) : undefined;
    },
  };
}

function decodeURIComponentSafe(s: string): string {
  try {
    return decodeURIComponent(s);
  } catch {
    return s;
  }
}

/** Click handler that keeps same-origin links inside the single-page app. */
export function useInAppLinks() {
  const router = useRouter();
  return (e: MouseEvent<HTMLElement>) => {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    const a = (e.target as HTMLElement).closest("a");
    if (!a || a.target || a.hasAttribute("download")) return;
    const url = new URL(a.href, window.location.href);
    if (url.origin !== window.location.origin || url.pathname.startsWith("/api/")) return;
    if (url.pathname === window.location.pathname && url.search === window.location.search && url.hash) return;
    e.preventDefault();
    router.history.push(url.pathname + url.search + url.hash);
  };
}

/** README card under the tree. */
export function Readme({ owner, name, refName, path }: { owner: string; name: string; refName: string; path: string }) {
  const t = useT("code");
  const blob = useReadme(owner, name, refName, path, true);
  const ctx = useMarkdownContext(owner, name, refName, path);
  const onClick = useInAppLinks();
  return (
    <Card title={path.split("/").pop()} className="rp-readme">
      <Gate query={blob}>
        {(b) =>
          b.binary || b.content === undefined ? (
            <p className="ui-note acc-pad">{t("blob.binary")}</p>
          ) : isMarkdown(path) ? (
            <div onClick={onClick}>
              <Markdown source={b.content} ctx={ctx} className="rp-md" />
            </div>
          ) : (
            <pre className="rp-pre">{b.content}</pre>
          )
        }
      </Gate>
    </Card>
  );
}
