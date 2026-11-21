// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { Notice } from "./Notice";
import { EmptyState, ErrorState } from "./State";

afterEach(cleanup);

test("an error state is announced, says its message and offers one action", () => {
  const onClick = vi.fn();
  render(<ErrorState action={{ label: "Try again", onClick }}>marquee can’t reach its service.</ErrorState>);

  expect(screen.getByRole("alert").textContent).toContain("marquee can’t reach its service.");
  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  expect(onClick).toHaveBeenCalledTimes(1);
});

test("an empty state is quiet: not an alert, and no action unless one is given", () => {
  render(<EmptyState>Nothing to show yet.</EmptyState>);

  expect(screen.getByText("Nothing to show yet.")).toBeTruthy();
  expect(screen.queryByRole("alert")).toBeNull();
  expect(screen.queryByRole("button")).toBeNull();
});

test("two torn strips on screen never share clip ids", () => {
  const { container } = render(
    <>
      <ErrorState>One.</ErrorState>
      <ErrorState>Two.</ErrorState>
    </>,
  );

  const ids = [...container.querySelectorAll("clipPath")].map((c) => c.id);
  expect(ids).toHaveLength(4);
  expect(new Set(ids).size).toBe(4);
});

test("a notice announces only an error", () => {
  const { rerender } = render(<Notice tone="error">Broke.</Notice>);
  expect(screen.getByRole("alert").textContent).toContain("Broke.");

  rerender(<Notice tone="quiet">Nothing is downloading.</Notice>);
  expect(screen.queryByRole("alert")).toBeNull();
});
