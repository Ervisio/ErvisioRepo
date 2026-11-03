import type { ComponentProps } from "react";

const cx = (...c: (string | undefined)[]) => c.filter(Boolean).join(" ");

/** Scrolls horizontally inside its own box on narrow screens. */
export function Table({ className, ...rest }: ComponentProps<"table">) {
  return (
    <div className="ui-table-wrap">
      <table className={cx("ui-table", className)} {...rest} />
    </div>
  );
}
export const TableHead = (p: ComponentProps<"thead">) => <thead {...p} />;
export const TableBody = (p: ComponentProps<"tbody">) => <tbody {...p} />;
export const Tr = (p: ComponentProps<"tr">) => <tr {...p} />;
export const Th = ({ scope = "col", ...p }: ComponentProps<"th">) => <th scope={scope} {...p} />;
export const Td = ({ className, ...p }: ComponentProps<"td"> & { className?: string }) => <td className={className} {...p} />;
