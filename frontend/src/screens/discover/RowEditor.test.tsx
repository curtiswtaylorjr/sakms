// RowEditor tests — up/down reorder buttons call onMove with the right
// key/direction (edge buttons disabled), and enable-toggle/Delete only
// render for removable rows, mirroring SliderRow's established row-editor
// UI conventions. RowEditor itself is presentational — the caller (Mainstream/
// Adult) owns actually persisting the new order via saveRowOrder, so this
// tests the interaction contract that drives that persistence, not a live
// fetch.

import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, within } from "@solidjs/testing-library";
import { RowEditor, type RowDescriptor } from "./RowEditor";

const rows: RowDescriptor[] = [
  { key: "trending-movies", label: "Trending Movies", removable: false },
  { key: "slider:4", label: "Heist Movies", removable: true, enabled: true },
  { key: "rssfeed:2", label: "NZBGeek Saved Search", removable: true, enabled: false },
];

describe("RowEditor — reorder", () => {
  it("moving a middle row up calls onMove with its key and -1", () => {
    const onMove = vi.fn();
    render(() => (
      <RowEditor
        rows={rows}
        onMove={onMove}
        onToggleEnabled={vi.fn()}
        onDelete={vi.fn()}
      />
    ));

    const row = screen.getByText("Heist Movies").closest("li") as HTMLElement;
    fireEvent.click(within(row).getByLabelText("Move Heist Movies up"));
    expect(onMove).toHaveBeenCalledWith("slider:4", -1);
  });

  it("moving a middle row down calls onMove with its key and 1", () => {
    const onMove = vi.fn();
    render(() => (
      <RowEditor
        rows={rows}
        onMove={onMove}
        onToggleEnabled={vi.fn()}
        onDelete={vi.fn()}
      />
    ));

    const row = screen.getByText("Heist Movies").closest("li") as HTMLElement;
    fireEvent.click(within(row).getByLabelText("Move Heist Movies down"));
    expect(onMove).toHaveBeenCalledWith("slider:4", 1);
  });

  it("disables Up on the first row and Down on the last row", () => {
    render(() => (
      <RowEditor
        rows={rows}
        onMove={vi.fn()}
        onToggleEnabled={vi.fn()}
        onDelete={vi.fn()}
      />
    ));

    const first = screen.getByText("Trending Movies").closest("li") as HTMLElement;
    expect(within(first).getByLabelText("Move Trending Movies up")).toBeDisabled();

    const last = screen
      .getByText("NZBGeek Saved Search")
      .closest("li") as HTMLElement;
    expect(
      within(last).getByLabelText("Move NZBGeek Saved Search down"),
    ).toBeDisabled();
  });
});

describe("RowEditor — removable-only controls", () => {
  it("shows the enabled toggle and Delete only for removable rows", () => {
    render(() => (
      <RowEditor
        rows={rows}
        onMove={vi.fn()}
        onToggleEnabled={vi.fn()}
        onDelete={vi.fn()}
      />
    ));

    const builtin = screen.getByText("Trending Movies").closest("li") as HTMLElement;
    expect(within(builtin).queryByText("Delete")).not.toBeInTheDocument();
    expect(
      within(builtin).queryByLabelText("Trending Movies enabled"),
    ).not.toBeInTheDocument();

    const dynamic = screen.getByText("Heist Movies").closest("li") as HTMLElement;
    expect(within(dynamic).getByText("Delete")).toBeInTheDocument();
    expect(
      within(dynamic).getByLabelText("Heist Movies enabled"),
    ).toBeInTheDocument();
  });

  it("reflects a disabled dynamic row's checkbox state and fires onToggleEnabled/onDelete", () => {
    const onToggleEnabled = vi.fn();
    const onDelete = vi.fn();
    render(() => (
      <RowEditor
        rows={rows}
        onMove={vi.fn()}
        onToggleEnabled={onToggleEnabled}
        onDelete={onDelete}
      />
    ));

    const feedRow = screen
      .getByText("NZBGeek Saved Search")
      .closest("li") as HTMLElement;
    const checkbox = within(feedRow).getByLabelText(
      "NZBGeek Saved Search enabled",
    ) as HTMLInputElement;
    expect(checkbox.checked).toBe(false);

    fireEvent.click(checkbox);
    expect(onToggleEnabled).toHaveBeenCalledWith(rows[2]);

    fireEvent.click(within(feedRow).getByText("Delete"));
    expect(onDelete).toHaveBeenCalledWith(rows[2]);
  });
});
