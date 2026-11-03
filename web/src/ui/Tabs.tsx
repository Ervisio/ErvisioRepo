import * as RTabs from "@radix-ui/react-tabs";
import type { ComponentProps } from "react";

export function Tabs({ className, ...rest }: ComponentProps<typeof RTabs.Root>) {
  return <RTabs.Root className={["ui-tabs", className].filter(Boolean).join(" ")} {...rest} />;
}
export function TabsList({ className, ...rest }: ComponentProps<typeof RTabs.List>) {
  return <RTabs.List className={["ui-tablist", className].filter(Boolean).join(" ")} {...rest} />;
}
export function TabsTrigger({ className, ...rest }: ComponentProps<typeof RTabs.Trigger>) {
  return <RTabs.Trigger className={["ui-tab", className].filter(Boolean).join(" ")} {...rest} />;
}
export function TabsContent({ className, ...rest }: ComponentProps<typeof RTabs.Content>) {
  return <RTabs.Content className={["ui-tabpanel", className].filter(Boolean).join(" ")} {...rest} />;
}
