import { useSession } from "../../api/identity.ts";
import { useSetPin, useUserPins, PIN_LIMIT } from "../../api/personal.ts";
import { ApiError } from "../../api/http.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, useToast } from "../../ui/index.ts";

/** Pin/unpin toggle for the signed-in user; a 409 means the pin limit is reached. */
export function PinButton({ owner, name, compact }: { owner: string; name: string; compact?: boolean }) {
  const t = useT("repos");
  const toast = useToast();
  const signedIn = !!useSession().data;
  const pins = useUserPins(signedIn);
  const setPin = useSetPin();
  if (!signedIn || !pins.data) return null;
  const pinned = pins.data.some((r) => r.owner === owner && r.name === name);
  const label = pinned ? t("pin.remove") : t("pin.add");
  const toggle = () =>
    setPin.mutate(
      { owner, name, pinned: !pinned },
      {
        onError: (e) =>
          toast(e instanceof ApiError && e.status === 409
            ? { title: t("pin.limit", { count: PIN_LIMIT }), tone: "warn" }
            : { title: t("pin.error"), description: e.message, tone: "err" }),
      },
    );
  return (
    <Button size={compact ? "sm" : "md"} icon="star" aria-pressed={pinned} aria-label={compact ? label : undefined} title={label} disabled={setPin.isPending} onClick={toggle}>
      {compact ? null : label}
    </Button>
  );
}
