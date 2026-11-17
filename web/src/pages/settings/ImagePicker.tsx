import { useRef, type ReactNode } from "react";
import { Button } from "../../ui/index.ts";

/** Preview with Upload and Remove buttons, for the image of an organization or a repository. */
export function ImagePicker({ preview, hasImage, uploading, busy, onPick, onRemove, labels }: {
  preview: ReactNode;
  hasImage: boolean;
  uploading: boolean;
  busy: boolean;
  onPick: (file: File) => void;
  onRemove: () => void;
  labels: { upload: string; uploading: string; remove: string };
}) {
  const picker = useRef<HTMLInputElement>(null);
  return (
    <div className="ui-inline">
      {preview}
      <input
        ref={picker}
        type="file"
        accept="image/png,image/jpeg,image/gif"
        hidden
        aria-label={labels.upload}
        onChange={(e) => {
          const file = e.target.files?.[0];
          e.target.value = "";
          if (file) onPick(file);
        }}
      />
      <Button size="sm" icon="upload" disabled={busy} onClick={() => picker.current?.click()}>
        {uploading ? labels.uploading : labels.upload}
      </Button>
      {hasImage && (
        <Button size="sm" icon="trash" disabled={busy} onClick={onRemove}>
          {labels.remove}
        </Button>
      )}
    </div>
  );
}
