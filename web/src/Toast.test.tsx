// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { Toaster, type ToastMsg } from "./Toast";

const msg = (over: Partial<ToastMsg> = {}): ToastMsg => ({ n: 1, text: "Added “Dune”.", kind: "ok", ...over });

beforeEach(() => vi.useFakeTimers());
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

// The exit animation starts its own timer after a render, so time is advanced in steps with a flush between them.
const settle = async (ms: number) => {
  for (let left = ms; left > 0; left -= 100) await act(() => vi.advanceTimersByTimeAsync(Math.min(100, left)));
};

test("a confirmation is a status that dismisses itself", async () => {
  const onDismiss = vi.fn();
  render(<Toaster toast={msg()} onDismiss={onDismiss} />);

  expect(screen.getByRole("status").textContent).toContain("Added “Dune”.");
  await settle(4000);
  expect(onDismiss).not.toHaveBeenCalled();
  await settle(2000);
  expect(onDismiss).toHaveBeenCalledTimes(1);
});

test("an error is an alert and stays longer than a confirmation", async () => {
  const onDismiss = vi.fn();
  render(<Toaster toast={msg({ kind: "error", text: "Could not add it." })} onDismiss={onDismiss} />);

  expect(screen.getByRole("alert").textContent).toContain("Could not add it.");
  await settle(6000);
  expect(onDismiss).not.toHaveBeenCalled();
  await settle(2500);
  expect(onDismiss).toHaveBeenCalledTimes(1);
});

test("the X dismisses it", async () => {
  const onDismiss = vi.fn();
  render(<Toaster toast={msg()} onDismiss={onDismiss} />);

  fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
  await settle(300);

  expect(onDismiss).toHaveBeenCalledTimes(1);
});

test("Esc on the toast dismisses it and goes no further", async () => {
  const onDismiss = vi.fn();
  const outside = vi.fn();
  window.addEventListener("keydown", outside);
  render(<Toaster toast={msg()} onDismiss={onDismiss} />);

  fireEvent.keyDown(screen.getByRole("button", { name: "Dismiss" }), { key: "Escape" });
  await settle(300);
  window.removeEventListener("keydown", outside);

  expect(onDismiss).toHaveBeenCalledTimes(1);
  expect(outside).not.toHaveBeenCalled();
});

test("an action runs and closes the toast", async () => {
  const onDismiss = vi.fn();
  const onClick = vi.fn();
  render(<Toaster toast={msg({ kind: "error", text: "Could not retry.", action: { label: "Try again", onClick } })} onDismiss={onDismiss} />);

  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  await settle(300);

  expect(onClick).toHaveBeenCalledTimes(1);
  expect(onDismiss).toHaveBeenCalledTimes(1);
});

test("hovering holds the timer; leaving lets it run", async () => {
  const onDismiss = vi.fn();
  render(<Toaster toast={msg()} onDismiss={onDismiss} />);

  fireEvent.pointerEnter(screen.getByRole("status"));
  await settle(20000);
  expect(onDismiss).not.toHaveBeenCalled();

  fireEvent.pointerLeave(screen.getByRole("status"));
  await settle(6000);
  expect(onDismiss).toHaveBeenCalledTimes(1);
});

test("leaving with the pointer keeps the toast while keyboard focus is still inside it", async () => {
  const onDismiss = vi.fn();
  render(<Toaster toast={msg({ kind: "error", text: "Could not add it.", action: { label: "Try again", onClick: () => {} } })} onDismiss={onDismiss} />);
  const toast = screen.getByRole("alert");

  fireEvent.pointerEnter(toast);
  act(() => screen.getByRole("button", { name: "Try again" }).focus());
  fireEvent.pointerLeave(toast);
  await settle(20000);
  expect(onDismiss).not.toHaveBeenCalled();

  act(() => screen.getByRole("button", { name: "Try again" }).blur());
  await settle(9000);
  expect(onDismiss).toHaveBeenCalledTimes(1);
});

test("a newer toast replaces the old one with its own timer", async () => {
  const onDismiss = vi.fn();
  const { rerender } = render(<Toaster toast={msg()} onDismiss={onDismiss} />);
  await settle(4000);

  rerender(<Toaster toast={msg({ n: 2, text: "Queued." })} onDismiss={onDismiss} />);
  await settle(4000);

  expect(screen.getByRole("status").textContent).toContain("Queued.");
  expect(onDismiss).not.toHaveBeenCalled();
});

test("nothing renders without a toast", () => {
  render(<Toaster toast={null} onDismiss={() => {}} />);
  expect(screen.queryByRole("status")).toBeNull();
});
