// Recognises social profile links (by host) for the profile card: a line icon in the kit's style and a
// short handle such as "@name" instead of the bare URL.
import type { ReactNode } from "react";

export type SocialId =
  | "x" | "instagram" | "youtube" | "tiktok" | "twitch" | "discord" | "github" | "gitlab" | "linkedin"
  | "facebook" | "reddit" | "telegram" | "spotify" | "bluesky" | "threads" | "patreon" | "kofi" | "steam" | "web";

const paths: Record<SocialId, ReactNode> = {
  x: <><path d="M4 4h4.5L20 20h-4.5z" /><path d="M20 4l-6.6 7.5M4 20l6.6-7.5" /></>,
  instagram: <><rect x="3" y="3" width="18" height="18" rx="5" /><circle cx="12" cy="12" r="4" /><circle cx="17.3" cy="6.7" r=".6" /></>,
  youtube: <><rect x="2.5" y="5.5" width="19" height="13" rx="4" /><path d="m10 9.3 4.8 2.7-4.8 2.7z" /></>,
  tiktok: <><path d="M14 3v11.5a3.5 3.5 0 1 1-3.5-3.5" /><path d="M14 3c.5 2.6 2.4 4.4 5 4.6" /></>,
  twitch: <><path d="M4.5 3H20v10.5L16 17.5h-4l-3 3v-3H4.5z" /><path d="M11 7.5v4M15.5 7.5v4" /></>,
  discord: <><path d="M8.5 7.6a13 13 0 0 1 7 0L17 6c1.8.6 3 1.5 3.5 2.5 1 3 1 6.5.5 9-1.2 1-2.7 1.7-4 2l-1-2" /><path d="M8.5 7.6 7 6c-1.8.6-3 1.5-3.5 2.5-1 3-1 6.5-.5 9 1.2 1 2.7 1.7 4 2l1-2" /><path d="M7.5 16c3 1.3 6 1.3 9 0" /><circle cx="9.5" cy="12.5" r="1" /><circle cx="14.5" cy="12.5" r="1" /></>,
  github: <><path d="M9 19c-4.3 1.4-4.3-2.5-6-3" /><path d="M15 21v-3.5a3 3 0 0 0-.9-2.4c2.9-.3 5.9-1.4 5.9-6.3a4.9 4.9 0 0 0-1.3-3.4 4.6 4.6 0 0 0-.1-3.4s-1.1-.3-3.5 1.3a12 12 0 0 0-6.2 0C6.5 1.7 5.4 2 5.4 2a4.6 4.6 0 0 0-.1 3.4A4.9 4.9 0 0 0 4 8.8c0 4.9 3 6 5.9 6.3a3 3 0 0 0-.9 2.4V21" /></>,
  gitlab: <path d="M12 21 3 13.6 5.2 4.5 8 11h8l2.8-6.5L21 13.6z" />,
  linkedin: <><rect x="3" y="3" width="18" height="18" rx="4" /><path d="M8 10.5V17M8 7.5v.01M12 17v-6.5M12 13.5a2.5 2.5 0 0 1 5 0V17" /></>,
  facebook: <path d="M15.5 3H13a4 4 0 0 0-4 4v3H7v3.5h2V21h3.5v-7.5H15l.5-3.5h-3V7.5a1 1 0 0 1 1-1h2z" />,
  reddit: <><ellipse cx="12" cy="14.5" rx="8" ry="5.5" /><path d="m12 9 1.2-5 4.3 1" /><circle cx="18.5" cy="5" r="1.5" /><path d="M9.5 16.8c1.5.8 3.5.8 5 0M9 13.5v.01M15 13.5v.01" /></>,
  telegram: <><path d="M21.5 3.5 2.5 11l6.5 2.5 2.5 7 3.5-4.5 4.5 3.5z" /><path d="M9 13.5 18 7" /></>,
  spotify: <><circle cx="12" cy="12" r="9" /><path d="M7.5 9.6c3-1 6.6-.8 9.5.8M8 12.8c2.5-.7 5.2-.5 7.5.7M8.6 15.8c2-.5 4-.3 5.6.5" /></>,
  bluesky: <path d="M12 11C10.5 8 7 5 4.5 4.5 3 4.2 3 5.5 3 6.5s.5 4.5 1 5c1 1.8 3.5 2 5.5 1.5-3 .5-4 2.5-2 4.5 2.5 2.5 4 .5 4.5-1.5.5 2 2 4 4.5 1.5 2-2 1-4-2-4.5 2 .5 4.5.3 5.5-1.5.5-.5 1-4 1-5s0-2.3-1.5-2C17 5 13.5 8 12 11z" />,
  threads: <><path d="M16.5 11.2c-.4-2.6-2.2-3.7-4.3-3.7-1.6 0-3 .7-3.7 2" /><path d="M16.5 11.2c-3.9-.9-7.5.1-7.4 2.6.1 1.6 1.6 2.4 3.2 2.3 2.3-.1 4.1-1.6 4.2-4.9z" /><path d="M16.6 11.3c2.3 1 3 3.2 2.1 5.3-1.2 2.8-4 4.4-7 4.4C7 21 3.5 18 3.5 12S7 3 11.7 3c3.6 0 6.3 1.7 7.5 5" /></>,
  patreon: <><circle cx="14.5" cy="9.5" r="5.5" /><path d="M4.5 3.5v17" /></>,
  kofi: <><path d="M4 8h12v6a5 5 0 0 1-5 5H9a5 5 0 0 1-5-5z" /><path d="M16 9.5h1.5a2.5 2.5 0 0 1 0 5H16" /><path d="M10 11.5c-.9-1-2.5-.3-2.2.9.3 1.1 2.2 2.3 2.2 2.3s1.9-1.2 2.2-2.3c.3-1.2-1.3-1.9-2.2-.9z" /></>,
  steam: <><circle cx="12" cy="12" r="9" /><circle cx="15.5" cy="9.5" r="2.5" /><circle cx="9" cy="15" r="1.8" /><path d="m3.5 13.5 4 1.6M10.6 14.1l3-2.6" /></>,
  web: <><circle cx="12" cy="12" r="9" /><path d="M3 12h18" /><path d="M12 3a14 14 0 0 1 0 18 14 14 0 0 1 0-18z" /></>,
};

export const socialNames: Record<SocialId, string> = {
  x: "X", instagram: "Instagram", youtube: "YouTube", tiktok: "TikTok", twitch: "Twitch", discord: "Discord",
  github: "GitHub", gitlab: "GitLab", linkedin: "LinkedIn", facebook: "Facebook", reddit: "Reddit", telegram: "Telegram",
  spotify: "Spotify", bluesky: "Bluesky", threads: "Threads", patreon: "Patreon", kofi: "Ko-fi", steam: "Steam", web: "",
};

const hosts: [RegExp, SocialId][] = [
  [/^(www\.|mobile\.)?(x|twitter)\.com$/, "x"],
  [/^(www\.)?instagram\.com$|^instagr\.am$/, "instagram"],
  [/^(www\.|m\.|music\.)?youtube\.com$|^youtu\.be$/, "youtube"],
  [/^(www\.|vm\.)?tiktok\.com$/, "tiktok"],
  [/^(www\.|m\.)?twitch\.tv$/, "twitch"],
  [/^(www\.)?discord(app)?\.(com|gg)$|^dsc\.gg$/, "discord"],
  [/^(www\.)?github\.com$/, "github"],
  [/^(www\.)?gitlab\.com$/, "gitlab"],
  [/^([a-z]{2,3}\.)?linkedin\.com$/, "linkedin"],
  [/^(www\.|m\.|web\.)?facebook\.com$|^fb\.com$/, "facebook"],
  [/^(www\.|old\.|new\.)?reddit\.com$/, "reddit"],
  [/^(t\.me|telegram\.me)$/, "telegram"],
  [/^open\.spotify\.com$/, "spotify"],
  [/^bsky\.app$/, "bluesky"],
  [/^(www\.)?threads\.(net|com)$/, "threads"],
  [/^(www\.)?patreon\.com$/, "patreon"],
  [/^(www\.)?ko-fi\.com$/, "kofi"],
  [/^steamcommunity\.com$/, "steam"],
];

/** Kind of link and the text to show: "@name" for profiles, otherwise host and path. */
export function socialLink(url: string): { id: SocialId; text: string } {
  let u: URL;
  try {
    u = new URL(url);
  } catch {
    return { id: "web", text: url };
  }
  const host = u.hostname.toLowerCase();
  const id = hosts.find(([re]) => re.test(host))?.[1] ?? "web";
  const seg = u.pathname.split("/").filter(Boolean).map((s) => decodeURIComponentSafe(s));
  const fallback = (u.host + u.pathname).replace(/\/$/, "");
  const at = (s?: string) => (s ? `@${s.replace(/^@/, "")}` : fallback);
  const first = seg[0];
  switch (id) {
    case "x":
    case "instagram":
    case "tiktok":
    case "threads":
    case "twitch":
      return { id, text: first && !["p", "reel", "video", "status"].includes(first) ? at(first) : fallback };
    case "youtube":
      return { id, text: first?.startsWith("@") ? first : first === "c" || first === "user" ? at(seg[1]) : fallback };
    case "github":
    case "gitlab":
    case "facebook":
    case "patreon":
    case "kofi":
      return { id, text: first ?? fallback };
    case "linkedin":
      return { id, text: (first === "in" || first === "company") && seg[1] ? seg[1] : fallback };
    case "reddit":
      return { id, text: (first === "u" || first === "user") && seg[1] ? `u/${seg[1]}` : first === "r" && seg[1] ? `r/${seg[1]}` : fallback };
    case "telegram":
      return { id, text: at(first) };
    case "bluesky":
      return { id, text: first === "profile" && seg[1] ? at(seg[1]) : fallback };
    case "steam":
      return { id, text: (first === "id" || first === "profiles") && seg[1] ? seg[1] : fallback };
    case "discord":
      return { id, text: host === "dsc.gg" || host.endsWith("discord.gg") ? (first ?? fallback) : first === "invite" && seg[1] ? seg[1] : fallback };
    default:
      return { id, text: fallback };
  }
}

function decodeURIComponentSafe(s: string): string {
  try {
    return decodeURIComponent(s);
  } catch {
    return s;
  }
}

/** Brand icon drawn like the kit's Icon (24px grid, 1.75 stroke, current colour). */
export function SocialIcon({ id }: { id: SocialId }) {
  return (
    <svg className="ui-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      {paths[id]}
    </svg>
  );
}
