import { useState } from "react";
import { useEmails, useMyOrgs, type Org } from "../../api/identity.ts";
import { useProfileDetails, useUpdateProfileDetails, type ProfileDetails } from "../../api/profile.ts";
import { useT } from "../../i18n/useT.ts";
import { AvatarImg } from "../../ui/AvatarImg.tsx";
import { Button, Input, SaveBar, Select, SettingRow, SettingsCard, SettingsSection, useDraft } from "../../ui/index.ts";
import { useNotify } from "./common.tsx";

const SOCIAL = 4;
const NONE = "-";

// orgs is the organization order, names joined by newlines (draft fields are strings).
type Fields = { bio: string; company: string; location: string; website: string; timezone: string; publicEmail: string; s0: string; s1: string; s2: string; s3: string; orgs: string };

const toFields = (d: ProfileDetails): Fields => ({
  bio: d.bio, company: d.company, location: d.location, website: d.website, timezone: d.timezone, publicEmail: d.publicEmail,
  s0: d.socialLinks[0] ?? "", s1: d.socialLinks[1] ?? "", s2: d.socialLinks[2] ?? "", s3: d.socialLinks[3] ?? "",
  orgs: (d.orgOrder ?? []).join("\n"),
});

/** The user's organizations in the saved order, then the others alphabetically (as the profile shows them). */
function ordered(orgs: Org[], order: string): Org[] {
  const rank = new Map(order.split("\n").filter(Boolean).map((n, i) => [n.toLowerCase(), i]));
  return [...orgs].sort((a, b) => {
    const ra = rank.get(a.name.toLowerCase());
    const rb = rank.get(b.name.toLowerCase());
    if (ra !== undefined && rb !== undefined) return ra - rb;
    if (ra !== undefined) return -1;
    if (rb !== undefined) return 1;
    return a.name.toLowerCase().localeCompare(b.name.toLowerCase());
  });
}

/** Organizations reorderable by drag and drop, with up and down buttons for the keyboard. */
function OrgOrder({ orgs, onChange }: { orgs: Org[]; onChange: (names: string[]) => void }) {
  const t = useT("account");
  const [dragged, setDragged] = useState<number | null>(null);
  const move = (from: number, to: number) => {
    if (to < 0 || to >= orgs.length || from === to) return;
    const names = orgs.map((o) => o.name);
    const [n] = names.splice(from, 1);
    names.splice(to, 0, n!);
    onChange(names);
  };
  if (orgs.length === 0) return <p className="ui-note">{t("details.orgOrder.none")}</p>;
  return (
    <ol className="acc-form" style={{ listStyle: "none", margin: 0, padding: 0 }}>
      {orgs.map((o, i) => {
        const label = o.displayName || o.name;
        return (
          <li
            key={o.name}
            draggable
            onDragStart={(e) => {
              setDragged(i);
              e.dataTransfer.effectAllowed = "move";
            }}
            onDragOver={(e) => e.preventDefault()}
            onDrop={(e) => {
              e.preventDefault();
              if (dragged !== null) move(dragged, i);
              setDragged(null);
            }}
            onDragEnd={() => setDragged(null)}
            className="ui-inline"
            style={{ cursor: "grab", opacity: dragged === i ? 0.5 : 1 }}
          >
            <AvatarImg src={o.avatarUrl} className="px-org px-sm">{label.slice(0, 1).toUpperCase()}</AvatarImg>
            <span style={{ flex: 1 }}>{label}</span>
            <Button size="sm" disabled={i === 0} aria-label={t("details.orgOrder.up", { name: label })} title={t("details.orgOrder.up", { name: label })} onClick={() => move(i, i - 1)}>↑</Button>
            <Button size="sm" disabled={i === orgs.length - 1} aria-label={t("details.orgOrder.down", { name: label })} title={t("details.orgOrder.down", { name: label })} onClick={() => move(i, i + 1)}>↓</Button>
          </li>
        );
      })}
    </ol>
  );
}

/** "GMT+1", "GMT-5:30"… for zone right now (daylight saving included); "" if the browser cannot tell. */
function gmtOffset(zone: string): string {
  try {
    const part = new Intl.DateTimeFormat("en-US", { timeZone: zone, timeZoneName: "shortOffset" }).formatToParts(new Date()).find((p) => p.type === "timeZoneName");
    return part?.value ?? "";
  } catch {
    return "";
  }
}

/** Time zones the browser knows, for the local time on the profile. */
function zones(): string[] {
  try {
    return Intl.supportedValuesOf("timeZone");
  } catch {
    return ["UTC"];
  }
}

/** Public profile details (bio, company, location, links, local time, public email) shown on the profile page. */
export function ProfileDetailsSection() {
  const details = useProfileDetails();
  if (!details.data) return null;
  return <DetailsForm key={JSON.stringify(details.data)} initial={details.data} />;
}

function DetailsForm({ initial }: { initial: ProfileDetails }) {
  const t = useT("account");
  const notify = useNotify();
  const update = useUpdateProfileDetails();
  const emails = useEmails();
  const myOrgs = useMyOrgs().data ?? [];
  const draft = useDraft(toFields(initial));
  const v = draft.value;
  const verified = (emails.data ?? []).filter((e) => e.verified).map((e) => e.address);
  const zoneList = zones();
  if (v.timezone && !zoneList.includes(v.timezone)) zoneList.unshift(v.timezone);

  function save() {
    const next: ProfileDetails = {
      bio: v.bio.trim(), company: v.company.trim(), location: v.location.trim(), website: v.website.trim(), timezone: v.timezone, publicEmail: v.publicEmail,
      socialLinks: [v.s0, v.s1, v.s2, v.s3].map((s) => s.trim()).filter(Boolean),
      orgOrder: v.orgs.split("\n").filter(Boolean),
    };
    update.mutate(next, {
      onSuccess: (d) => {
        draft.rebase(toFields(d));
        notify.ok(t("details.saved"));
      },
      onError: notify.fail,
    });
  }

  const text = (key: "company" | "location", max: number) => (
    <SettingRow label={t(`details.${key}`)} dirty={draft.isDirty(key)} was={draft.initial[key]}>
      <Input label={t(`details.${key}`)} hideLabel maxLength={max} value={v[key]} onChange={(e) => draft.set(key, e.target.value)} />
    </SettingRow>
  );
  const socialKeys = (["s0", "s1", "s2", "s3"] as const).slice(0, SOCIAL);

  return (
    <SettingsSection title={t("details.title")} description={t("details.hint")}>
      <SettingsCard>
        <SettingRow label={t("details.bio")} description={t("details.bio.hint")} dirty={draft.isDirty("bio")}>
          <textarea aria-label={t("details.bio")} className="ui-input acc-area" rows={3} maxLength={640} value={v.bio} onChange={(e) => draft.set("bio", e.target.value)} />
        </SettingRow>
        {text("company", 100)}
        {text("location", 100)}
        <SettingRow label={t("details.website")} dirty={draft.isDirty("website")} was={draft.initial.website}>
          <Input label={t("details.website")} hideLabel type="url" inputMode="url" maxLength={255} placeholder="https://" value={v.website} onChange={(e) => draft.set("website", e.target.value)} />
        </SettingRow>
        <SettingRow label={t("details.social")} description={t("details.social.hint")} dirty={socialKeys.some((k) => draft.isDirty(k))}>
          <div className="acc-form">
            {socialKeys.map((k, i) => (
              <Input key={k} label={t("details.socialN", { n: i + 1 })} hideLabel type="url" inputMode="url" maxLength={255} placeholder="https://" value={v[k]} onChange={(e) => draft.set(k, e.target.value)} />
            ))}
          </div>
        </SettingRow>
        <SettingRow label={t("details.timezone")} description={t("details.timezone.hint")} dirty={draft.isDirty("timezone")} was={draft.initial.timezone || t("details.hidden")}>
          <Select
            label={t("details.timezone")}
            hideLabel
            value={v.timezone || NONE}
            onValueChange={(x) => draft.set("timezone", x === NONE ? "" : x)}
            options={[{ value: NONE, label: t("details.hidden") }, ...zoneList.map((z) => {
              const off = gmtOffset(z);
              return { value: z, label: off ? `${z.replaceAll("_", " ")} (${off})` : z.replaceAll("_", " ") };
            })]}
          />
        </SettingRow>
        <SettingRow label={t("details.email")} description={t("details.email.hint")} dirty={draft.isDirty("publicEmail")} was={draft.initial.publicEmail || t("details.hidden")}>
          <Select
            label={t("details.email")}
            hideLabel
            value={v.publicEmail || NONE}
            onValueChange={(x) => draft.set("publicEmail", x === NONE ? "" : x)}
            options={[{ value: NONE, label: t("details.hidden") }, ...verified.map((a) => ({ value: a, label: a }))]}
          />
        </SettingRow>
        <SettingRow label={t("details.orgOrder")} description={t("details.orgOrder.hint")} dirty={draft.isDirty("orgs")}>
          <OrgOrder orgs={ordered(myOrgs, v.orgs)} onChange={(names) => draft.set("orgs", names.join("\n"))} />
        </SettingRow>
      </SettingsCard>
      <SaveBar
        draft={draft}
        labels={{
          bio: t("details.bio"), company: t("details.company"), location: t("details.location"), website: t("details.website"),
          timezone: t("details.timezone"), publicEmail: t("details.email"),
          s0: t("details.socialN", { n: 1 }), s1: t("details.socialN", { n: 2 }), s2: t("details.socialN", { n: 3 }), s3: t("details.socialN", { n: 4 }),
          orgs: t("details.orgOrder"),
        }}
        onSave={save}
        saving={update.isPending}
      />
    </SettingsSection>
  );
}
