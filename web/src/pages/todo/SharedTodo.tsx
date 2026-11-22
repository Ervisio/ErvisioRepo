import { useId, useState, type FormEvent, type ReactNode } from "react";
import { ApiError } from "../../api/http.ts";
import { useSession } from "../../api/identity.ts";
import {
  todoAccessDenied, useTodo, type TodoBoard, type TodoCategory, type TodoItem, type TodoScope,
} from "../../api/todos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Dialog, DialogClose, Input, Menu, MenuItem, MenuSeparator, Select } from "../../ui/index.ts";
import "./todo.css";

type Edit = { kind: "item"; item: TodoItem } | { kind: "category"; category?: TodoCategory };
type Delete = { kind: "item" | "category"; id: string; name: string };
type Draft<T> = { value: T; revision: number };
type Save = (board: TodoBoard) => Promise<boolean>;
type Direction = -1 | 1;

const uncategorized = "none";
const maxItems = 500;
const maxCategories = 64;

function reorder<T extends { id: string }>(rows: T[], row: T, neighbor: T | undefined) {
  if (!neighbor) return rows;
  return rows.map((entry) => entry.id === row.id ? neighbor : entry.id === neighbor.id ? row : entry);
}

function newTodoId() {
  const parts = crypto.getRandomValues(new Uint8Array(16));
  const bytes = Array.from(parts, (value, index) => {
    const adjusted = index === 6 ? (value & 15) | 64 : index === 8 ? (value & 63) | 128 : value;
    return adjusted.toString(16).padStart(2, "0");
  });
  return [bytes.slice(0, 4), bytes.slice(4, 6), bytes.slice(6, 8), bytes.slice(8, 10), bytes.slice(10)]
    .map((part) => part.join("")).join("-");
}

function replace<T extends { id: string }>(rows: T[], record: T) {
  return rows.map((row) => row.id === record.id ? record : row);
}

function remove(board: TodoBoard, target: Delete): TodoBoard {
  if (target.kind === "item") return { ...board, items: board.items.filter((item) => item.id !== target.id) };
  return {
    ...board,
    categories: board.categories.filter((category) => category.id !== target.id),
    items: board.items.map((item) => item.categoryId === target.id ? { ...item, categoryId: undefined } : item),
  };
}

export function SharedTodo({ scope }: { scope: TodoScope }) {
  const session = useSession();
  const viewer = session.isSuccess ? session.data.username : undefined;
  const key = "org" in scope ? scope.org : `${scope.owner}/${scope.name}`;
  return viewer ? <TodoPanel key={`${viewer}/${key}`} scope={scope} viewer={viewer} /> : null;
}

function TodoPanel({ scope, viewer }: { scope: TodoScope; viewer: string }) {
  const t = useT("todo");
  const { query, save, preference } = useTodo(scope, viewer);
  const contentId = useId();
  const titleId = useId();
  const [edit, setEdit] = useState<Draft<Edit>>();
  const [deletion, setDeletion] = useState<Draft<Delete>>();

  if (todoAccessDenied(query.error) || todoAccessDenied(save.error) || todoAccessDenied(preference.error)) return null;
  if (!query.data) return null;

  const { board, collapsed } = query.data;
  const Panel = "org" in scope ? "section" : "aside";
  const completed = board.items.filter((item) => item.completed).length;
  const busy = save.isPending || preference.isPending;
  const error = save.error ?? preference.error ?? query.error;
  const message = error instanceof ApiError && error.status === 409 ? t("conflict") : t("failed");
  const startEdit = (value: Edit) => {
    save.reset();
    setEdit({ value, revision: board.revision });
  };
  const startDelete = (value: Delete) => {
    save.reset();
    setDeletion({ value, revision: board.revision });
  };
  const staleDeletion = deletion !== undefined && deletion.revision !== board.revision;
  const saveBoard: Save = async (next) => {
    preference.reset();
    try {
      await save.mutateAsync(next);
      return true;
    } catch {
      return false;
    }
  };
  const categorized = new Map<string, TodoItem[]>();
  for (const item of board.items) {
    const category = item.categoryId ?? uncategorized;
    const items = categorized.get(category);
    if (items) items.push(item);
    else categorized.set(category, [item]);
  }

  return (
    <Panel className="ui-box todo-panel" aria-labelledby={titleId}>
      <header className="todo-head">
        <h2 id={titleId}>{t("title")}</h2>
        <span className="todo-members">{t("members")}</span>
        <span className="todo-count">{t("progress", { completed, total: board.items.length })}</span>
        <Button
          className="todo-icon todo-fold"
          icon={collapsed ? "right" : "down"}
          aria-label={t(collapsed ? "expand" : "collapse")}
          aria-expanded={!collapsed}
          aria-controls={contentId}
          disabled={busy}
          onClick={() => { save.reset(); preference.mutate(!collapsed); }}
        />
      </header>
      {error && <p className="todo-error" role="alert">{message}</p>}
      <div id={contentId} hidden={collapsed} aria-busy={busy}>
        {!board.items.length && !board.categories.length && <p className="todo-empty">{t("empty")}</p>}
        {board.categories.map((category, index) => (
          <TodoGroup
            key={category.id}
            title={category.name}
            items={categorized.get(category.id) ?? []}
            disabled={busy}
            board={board}
            save={saveBoard}
            onEdit={startEdit}
            onDelete={startDelete}
            headingAction={
              <TodoMenu
                name={category.name}
                disabled={busy}
                canMoveUp={index > 0}
                canMoveDown={index < board.categories.length - 1}
                onEdit={() => startEdit({ kind: "category", category })}
                onDelete={() => startDelete({ kind: "category", id: category.id, name: category.name })}
                onMove={(direction) => void saveBoard({
                  ...board,
                  categories: reorder(board.categories, category, board.categories[index + direction]),
                })}
              />
            }
          />
        ))}
        {categorized.has(uncategorized) && (
          <TodoGroup
            title={t("uncategorized")}
            items={categorized.get(uncategorized) ?? []}
            disabled={busy}
            board={board}
            save={saveBoard}
            onEdit={startEdit}
            onDelete={startDelete}
          />
        )}
        <QuickAdd board={board} disabled={busy} save={saveBoard} onCategory={() => startEdit({ kind: "category" })} />
      </div>
      {edit && (
        <TodoEdit
          edit={edit.value}
          board={board}
          busy={busy}
          stale={edit.revision !== board.revision}
          message={error ? message : undefined}
          close={() => { if (!busy) { save.reset(); setEdit(undefined); } }}
          save={async (next) => { if (await saveBoard(next)) setEdit(undefined); }}
        />
      )}
      {deletion && (
        <Dialog
          open
          onOpenChange={(open) => { if (!open && !busy) { save.reset(); setDeletion(undefined); } }}
          title={t(deletion.value.kind === "item" ? "deleteItem" : "deleteCategory")}
          description={t(deletion.value.kind === "item" ? "deleteItemHint" : "deleteCategoryHint", { name: deletion.value.name })}
          footer={<>
            <DialogClose asChild><Button disabled={busy}>{t("cancel")}</Button></DialogClose>
            <Button variant="danger" disabled={busy || staleDeletion} onClick={async () => {
              if (await saveBoard(remove(board, deletion.value))) setDeletion(undefined);
            }}>{t("delete")}</Button>
          </>}
        >
          {(staleDeletion || error) && <p className="todo-error" role="alert">{staleDeletion ? t("staleDraft") : message}</p>}
        </Dialog>
      )}
    </Panel>
  );
}

function categoriesFor(board: TodoBoard, label: string) {
  return [{ value: uncategorized, label }, ...board.categories.map(({ id, name }) => ({ value: id, label: name }))];
}

function QuickAdd({ board, disabled, save, onCategory }: {
  board: TodoBoard; disabled: boolean; save: Save; onCategory: () => void;
}) {
  const t = useT("todo");
  const [title, setTitle] = useState("");
  const [category, setCategory] = useState(uncategorized);
  const options = categoriesFor(board, t("uncategorized"));
  const selected = options.some(({ value }) => value === category) ? category : uncategorized;
  async function add(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (disabled || !title.trim() || board.items.length >= maxItems) return;
    const item: TodoItem = {
      id: newTodoId(), title: title.trim(), completed: false,
      categoryId: selected === uncategorized ? undefined : selected,
    };
    if (await save({ ...board, items: [...board.items, item] })) setTitle("");
  }
  return (
    <>
      <form className="todo-add" onSubmit={add}>
        <Input label={t("newItem")} hideLabel placeholder={t("newItem")} value={title} maxLength={240} disabled={disabled || board.items.length >= maxItems} onChange={(event) => setTitle(event.target.value)} />
        {board.categories.length > 0 && <Select label={t("category")} hideLabel value={selected} options={options} disabled={disabled || board.items.length >= maxItems} onValueChange={setCategory} />}
        <Button variant="primary" type="submit" disabled={disabled || !title.trim() || board.items.length >= maxItems}>{t("add")}</Button>
        <Button icon="plus" aria-label={t("newCategory")} onClick={onCategory} disabled={disabled || board.categories.length >= maxCategories}>{t("category")}</Button>
      </form>
      {board.items.length >= maxItems && <p className="todo-empty">{t("itemLimit", { max: maxItems })}</p>}
      {board.categories.length >= maxCategories && <p className="todo-empty">{t("categoryLimit", { max: maxCategories })}</p>}
    </>
  );
}

function TodoGroup({ title, items, disabled, board, save, onEdit, onDelete, headingAction }: {
  title: string; items: TodoItem[]; disabled: boolean; board: TodoBoard; save: Save;
  onEdit: (editor: Edit) => void; onDelete: (target: Delete) => void; headingAction?: ReactNode;
}) {
  const t = useT("todo");
  return (
    <div className="todo-group">
      <div className="todo-group-head">
        <h3>{title}</h3>
        <span className="todo-count">{items.length}</span>
        {headingAction}
      </div>
      {!items.length ? <p className="todo-empty">{t("emptyCategory")}</p> : (
        <ul className="ui-in todo-rows" aria-label={title}>
          {items.map((item, index) => (
            <li key={item.id} className="todo-row">
              <label>
                <input type="checkbox" checked={item.completed} disabled={disabled} onChange={(event) => {
                  void save({ ...board, items: replace(board.items, { ...item, completed: event.target.checked }) });
                }} />
                <span>{item.title}</span>
              </label>
              <TodoMenu
                name={item.title}
                disabled={disabled}
                canMoveUp={index > 0}
                canMoveDown={index < items.length - 1}
                onEdit={() => onEdit({ kind: "item", item })}
                onDelete={() => onDelete({ kind: "item", id: item.id, name: item.title })}
                onMove={(direction) => void save({ ...board, items: reorder(board.items, item, items[index + direction]) })}
              />
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function TodoMenu({ name, disabled, canMoveUp, canMoveDown, onEdit, onDelete, onMove }: {
  name: string; disabled: boolean; canMoveUp: boolean; canMoveDown: boolean;
  onEdit: () => void; onDelete: () => void; onMove: (direction: Direction) => void;
}) {
  const t = useT("todo");
  const label = t("options", { name });
  return (
    <Menu align="end" label={label} trigger={<Button className="todo-icon" icon="dots" aria-label={label} disabled={disabled} />}>
      <MenuItem icon="edit" onSelect={onEdit}>{t("edit")}</MenuItem>
      {canMoveUp && <MenuItem onSelect={() => onMove(-1)}>{t("moveUp")}</MenuItem>}
      {canMoveDown && <MenuItem onSelect={() => onMove(1)}>{t("moveDown")}</MenuItem>}
      <MenuSeparator />
      <MenuItem icon="trash" danger onSelect={onDelete}>{t("delete")}</MenuItem>
    </Menu>
  );
}

function TodoEdit({ edit, board, busy, stale, message, close, save }: {
  edit: Edit; board: TodoBoard; busy: boolean; stale: boolean; message?: string; close: () => void; save: (next: TodoBoard) => Promise<void>;
}) {
  const t = useT("todo");
  const formId = useId();
  const [name, setName] = useState(edit.kind === "item" ? edit.item.title : edit.category?.name ?? "");
  const [category, setCategory] = useState(edit.kind === "item" ? edit.item.categoryId ?? uncategorized : uncategorized);
  const options = categoriesFor(board, t("uncategorized"));
  const selected = options.some(({ value }) => value === category) ? category : uncategorized;
  const record = edit.kind === "item" ? board.items.find(({ id }) => id === edit.item.id) : undefined;
  const missing = edit.kind === "item" ? !record : !!edit.category && !board.categories.some(({ id }) => id === edit.category?.id);
  const full = edit.kind === "category" && !edit.category && board.categories.length >= maxCategories;
  const canSave = !missing && !stale && !full && !!name.trim() && !busy;
  const notice = missing ? t("removed") : stale ? t("staleDraft") : message;
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!canSave) return;
    if (edit.kind === "item" && record) {
      await save({ ...board, items: replace(board.items, {
        ...record, title: name.trim(), categoryId: selected === uncategorized ? undefined : selected,
      }) });
    } else if (edit.kind === "category") {
      const row = { id: edit.category?.id ?? newTodoId(), name: name.trim() };
      await save({ ...board, categories: edit.category ? replace(board.categories, row) : [...board.categories, row] });
    }
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => { if (!open) close(); }}
      title={t(edit.kind === "item" ? "editItem" : edit.category ? "editCategory" : "newCategory")}
      footer={<>
        <DialogClose asChild><Button disabled={busy}>{t("cancel")}</Button></DialogClose>
        <Button type="submit" form={formId} variant="primary" disabled={!canSave}>{t("save")}</Button>
      </>}
    >
      <form className="todo-edit" id={formId} onSubmit={submit}>
        <Input label={t(edit.kind === "item" ? "itemTitle" : "categoryName")} value={name} maxLength={edit.kind === "item" ? 240 : 80} disabled={busy || missing} onChange={(event) => setName(event.target.value)} />
        {edit.kind === "item" && <Select label={t("category")} value={selected} options={options} disabled={busy || missing} onValueChange={setCategory} />}
        {notice && <p className="todo-error" role="alert">{notice}</p>}
      </form>
    </Dialog>
  );
}
