import { useState } from "react";
import { useT } from "../i18n/useT.ts";
import {
  Badge, Button, Card, Dialog, DialogClose, EmptyState, Input, LayoutPicker, Menu, MenuItem, MenuLabel,
  MenuSeparator, Page, Select, Table, TableBody, TableHead, Tabs, TabsContent, TabsList, TabsTrigger,
  Td, Th, Tr, useToast,
} from "../ui/index.ts";

export function Catalog() {
  const t = useT("ui");
  const toast = useToast();
  const [visibility, setVisibility] = useState("private");

  return (
    <Page section="pr" title={t("catalog.title")} subtitle={t("catalog.subtitle")} actions={<Button variant="primary" icon="plus">{t("button.primary")}</Button>}>
      <div className="catalog">
        <Card title={t("catalog.section.layout")}>
          <div className="catalog-pad">
            <LayoutPicker />
            <p className="ui-note">{t("catalog.layout.hint")}</p>
          </div>
        </Card>

        <Card title={t("catalog.section.buttons")}>
          <div className="catalog-pad catalog-wrap">
            <Button variant="primary" icon="plus">{t("button.primary")}</Button>
            <Button>{t("button.secondary")}</Button>
            <Button variant="ghost">{t("button.ghost")}</Button>
            <Button variant="danger" icon="x">{t("button.danger")}</Button>
            <Button size="sm">{t("button.small")}</Button>
            <Button disabled>{t("button.disabled")}</Button>
          </div>
        </Card>

        <Card title={t("catalog.section.fields")}>
          <div className="catalog-pad catalog-fields">
            <Input label={t("field.name")} placeholder={t("field.name.placeholder")} hint={t("field.name.hint")} mono />
            <Input label={t("field.token")} defaultValue="erp_9f3a1c" error={t("field.token.error")} mono />
            <Select
              label={t("field.visibility")}
              placeholder={t("field.visibility.placeholder")}
              value={visibility}
              onValueChange={setVisibility}
              options={[
                { value: "private", label: t("visibility.private") },
                { value: "internal", label: t("visibility.internal") },
                { value: "public", label: t("visibility.public") },
              ]}
            />
          </div>
        </Card>

        <Card title={t("catalog.section.badges")}>
          <div className="catalog-pad catalog-wrap">
            <Badge>{t("badge.neutral")}</Badge>
            <Badge tone="ok">{t("badge.ok")}</Badge>
            <Badge tone="warn">{t("badge.warn")}</Badge>
            <Badge tone="err">{t("badge.err")}</Badge>
            <Badge tone="info">{t("badge.info")}</Badge>
            <Badge tone="section">{t("badge.section")}</Badge>
          </div>
        </Card>

        <Card title={t("catalog.section.tabs")}>
          <div className="catalog-pad">
            <Tabs defaultValue="files">
              <TabsList>
                <TabsTrigger value="files">{t("tabs.files")}</TabsTrigger>
                <TabsTrigger value="history">{t("tabs.history")}</TabsTrigger>
                <TabsTrigger value="branches">{t("tabs.branches")}</TabsTrigger>
              </TabsList>
              <TabsContent value="files">{t("tabs.files.body")}</TabsContent>
              <TabsContent value="history">{t("tabs.history.body")}</TabsContent>
              <TabsContent value="branches">{t("tabs.branches.body")}</TabsContent>
            </Tabs>
          </div>
        </Card>

        <section aria-label={t("catalog.section.table")}>
          <Table>
            <TableHead>
              <Tr>
                <Th>{t("table.name")}</Th>
                <Th>{t("table.version")}</Th>
                <Th>{t("table.status")}</Th>
                <Th className="ui-num">{t("table.size")}</Th>
              </Tr>
            </TableHead>
            <TableBody>
              <Tr>
                <Td>{t("table.row1")}</Td>
                <Td><span className="ui-mono">v1.4.0</span></Td>
                <Td><Badge tone="ok">{t("badge.ok")}</Badge></Td>
                <Td className="ui-num">48 MB</Td>
              </Tr>
              <Tr>
                <Td>{t("table.row2")}</Td>
                <Td><span className="ui-mono">v1.4.0</span></Td>
                <Td><Badge tone="info">{t("badge.info")}</Badge></Td>
                <Td className="ui-num">212 MB</Td>
              </Tr>
              <Tr>
                <Td>{t("table.row3")}</Td>
                <Td><span className="ui-mono">v0.9.2</span></Td>
                <Td><Badge tone="err">{t("badge.err")}</Badge></Td>
                <Td className="ui-num">9 MB</Td>
              </Tr>
            </TableBody>
          </Table>
        </section>

        <Card title={t("catalog.section.overlays")}>
          <div className="catalog-pad catalog-wrap">
            <Menu trigger={<Button icon="menu">{t("menu.open")}</Button>}>
              <MenuLabel>{t("menu.label")}</MenuLabel>
              <MenuItem icon="copy">{t("menu.copy")}</MenuItem>
              <MenuItem icon="pkg">{t("menu.archive")}</MenuItem>
              <MenuSeparator />
              <MenuItem icon="x" danger>{t("menu.delete")}</MenuItem>
            </Menu>
            <Dialog
              trigger={<Button>{t("dialog.open")}</Button>}
              title={t("dialog.title")}
              description={t("dialog.description")}
              footer={
                <>
                  <DialogClose asChild><Button variant="ghost">{t("dialog.cancel")}</Button></DialogClose>
                  <DialogClose asChild><Button variant="danger">{t("dialog.confirm")}</Button></DialogClose>
                </>
              }
            />
            <Button onClick={() => toast({ title: t("toast.title"), description: t("toast.description"), tone: "ok" })}>
              {t("toast.show")}
            </Button>
          </div>
        </Card>

        <Card title={t("catalog.section.empty")}>
          <EmptyState icon="pr" title={t("empty.title")} description={t("empty.description")} action={<Button variant="primary">{t("empty.action")}</Button>} />
        </Card>

        <Card title={t("catalog.section.hues")}>
          <div className="catalog-pad catalog-wrap">
            {(["code", "pr", "issue", "act", "pkg"] as const).map((s) => (
              <span key={s} className={`sec-${s}`}>
                <Badge tone="section">{t(`hue.${s}`)}</Badge>
              </span>
            ))}
          </div>
        </Card>
      </div>
    </Page>
  );
}
