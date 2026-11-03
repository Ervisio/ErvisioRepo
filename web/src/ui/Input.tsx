import { useId, type InputHTMLAttributes } from "react";

export type InputProps = Omit<InputHTMLAttributes<HTMLInputElement>, "id"> & {
  label: string;
  hint?: string;
  error?: string;
  mono?: boolean;
};

export function Input({ label, hint, error, mono, className, ...rest }: InputProps) {
  const id = useId();
  const noteId = `${id}-note`;
  const note = error ?? hint;
  return (
    <div className={["ui-field", error ? "ui-field-err" : "", className].filter(Boolean).join(" ")}>
      <label htmlFor={id}>{label}</label>
      <input
        id={id}
        className={mono ? "ui-input ui-mono" : "ui-input"}
        aria-invalid={error ? true : undefined}
        aria-describedby={note ? noteId : undefined}
        {...rest}
      />
      {note && (
        <p id={noteId} className="ui-note" role={error ? "alert" : undefined}>
          {note}
        </p>
      )}
    </div>
  );
}
