import * as RSelect from "@radix-ui/react-select";
import { useId } from "react";
import { Icon } from "./Icon.tsx";
import { cspNonce } from "./nonce.ts";

export type SelectOption = { value: string; label: string };

export type SelectProps = {
  label: string;
  value: string;
  onValueChange: (value: string) => void;
  options: readonly SelectOption[];
  placeholder?: string;
  disabled?: boolean;
  className?: string;
  /** Keeps the label for assistive tech only, when a settings row already shows it. */
  hideLabel?: boolean;
};

export function Select({ label, value, onValueChange, options, placeholder, disabled, className, hideLabel }: SelectProps) {
  const id = useId();
  return (
    <div className={["ui-field", className].filter(Boolean).join(" ")}>
      <label id={id} className={hideLabel ? "sr-only" : undefined}>{label}</label>
      <RSelect.Root value={value} onValueChange={onValueChange} disabled={disabled}>
        <RSelect.Trigger className="ui-select" aria-labelledby={id}>
          <RSelect.Value placeholder={placeholder} />
          <RSelect.Icon asChild>
            <Icon name="down" />
          </RSelect.Icon>
        </RSelect.Trigger>
        <RSelect.Portal>
          <RSelect.Content className="ui-pop" position="popper" sideOffset={6}>
            <RSelect.Viewport className="ui-pop-viewport" nonce={cspNonce()}>
              {options.map((o) => (
                <RSelect.Item key={o.value} value={o.value} className="ui-item">
                  <RSelect.ItemText>{o.label}</RSelect.ItemText>
                  <RSelect.ItemIndicator className="ui-item-mark">
                    <Icon name="check" />
                  </RSelect.ItemIndicator>
                </RSelect.Item>
              ))}
            </RSelect.Viewport>
          </RSelect.Content>
        </RSelect.Portal>
      </RSelect.Root>
    </div>
  );
}
