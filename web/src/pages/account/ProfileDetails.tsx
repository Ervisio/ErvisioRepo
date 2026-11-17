import { useEmails } from "../../api/identity.ts";
import { useProfileDetails, useUpdateProfileDetails, type ProfileDetails } from "../../api/profile.ts";
import { useT } from "../../i18n/useT.ts";
import { Input, SaveBar, Select, SettingRow, SettingsCard, SettingsSection, useDraft } from "../../ui/index.ts";
import { useNotify } from "./common.tsx";

const SOCIAL = 4;
const NONE = "-";

type Fields = { bio: string; company: string; location: string; website: string; timezone: string; publicEmail: string; s0: string; s1: string; s2: string; s3: string };

const toFields = (d: ProfileDetails): Fields => ({
  bio: d.bio, company: d.company, location: d.location, website: d.website, timezone: d.timezone, publicEmail: d.publicEmail,
  s0: d.socialLinks[0] ?? "", s1: d.socialLinks[1] ?? "", s2: d.socialLinks[2] ?? "", s3: d.socialLinks[3] ?? "",
});

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
  const draft = useDraft(toFields(initial));
  const v = draft.value;
  const verified = (emails.data ?? []).filter((e) => e.verified).map((e) => e.address);
  const zoneList = zones();
  if (v.timezone && !zoneList.includes(v.timezone)) zoneList.unshift(v.timezone);

  function save() {
    const next: ProfileDetails = {
      bio: v.bio.trim(), company: v.company.trim(), location: v.location.trim(), website: v.website.trim(), timezone: v.timezone, publicEmail: v.publicEmail,
      socialLinks: [v.s0, v.s1, v.s2, v.s3].map((s) => s.trim()).filter(Boolean),
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
            options={[{ value: NONE, label: t("details.hidden") }, ...zoneList.map((z) => ({ value: z, label: z.replaceAll("_", " ") }))]}
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
      </SettingsCard>
      <SaveBar
        draft={draft}
        labels={{
          bio: t("details.bio"), company: t("details.company"), location: t("details.location"), website: t("details.website"),
          timezone: t("details.timezone"), publicEmail: t("details.email"),
          s0: t("details.socialN", { n: 1 }), s1: t("details.socialN", { n: 2 }), s2: t("details.socialN", { n: 3 }), s3: t("details.socialN", { n: 4 }),
        }}
        onSave={save}
        saving={update.isPending}
      />
    </SettingsSection>
  );
}
