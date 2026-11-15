import type { StorageCheckResult } from "../../api/useStorage.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Table, TableBody, TableHead, Td, Th, Tr } from "../../ui/index.ts";

/** Outcome of a storage connection test, step by step. */
export function TestResult({ result, name }: { result: StorageCheckResult; name?: string }) {
  const t = useT("admin");
  return (
    <div role="status" aria-label={t("storage.test.result", { target: name ?? result.target })}>
      <p className="ui-inline">
        <Badge tone={result.ok ? "ok" : "err"} icon={result.ok ? "check" : "alert"}>{result.ok ? t("storage.test.ok") : t("storage.test.failed")}</Badge>
        <span>{t("storage.test.duration", { ms: result.durationMs })}</span>
      </p>
      <Table>
        <TableHead>
          <Tr>
            <Th>{t("storage.test.step")}</Th>
            <Th>{t("storage.test.outcome")}</Th>
            <Th className="ui-num">{t("storage.test.time")}</Th>
          </Tr>
        </TableHead>
        <TableBody>
          {result.steps.map((s) => (
            <Tr key={s.name}>
              <Td className="ui-mono">{t(`storage.step.${s.name}`)}</Td>
              <Td>
                <Badge tone={s.ok ? "ok" : "err"} icon={s.ok ? "check" : "alert"}>{s.ok ? t("storage.test.ok") : t("storage.test.failed")}</Badge>
                {s.error && <span className="ui-mono"> {s.error}</span>}
              </Td>
              <Td className="ui-num">{t("storage.test.duration", { ms: s.durationMs })}</Td>
            </Tr>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
