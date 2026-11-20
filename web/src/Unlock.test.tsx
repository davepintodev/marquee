// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { Unlock, type UnlockPhase } from "./Unlock";

interface Opts {
  rejected?: boolean;
  attempt?: number;
  phase?: UnlockPhase;
  onSubmit?: (token: string) => void;
}

const view = (o: Opts = {}) => <Unlock rejected={o.rejected ?? false} attempt={o.attempt ?? 0} phase={o.phase ?? "ask"} onSubmit={o.onSubmit ?? (() => {})} />;
const show = (o: Opts = {}) => render(view(o));

const field = () => screen.getByLabelText(/api token/i) as HTMLInputElement;
const wall = () => document.querySelector<HTMLElement>(".uwall")!;
const tiles = () => [...document.querySelectorAll<HTMLElement>(".utile")];
const slot = () => document.querySelector<HTMLElement>(".uslot")!;
const nudgeLine = () => screen.queryByText("unlock to continue.");

const px = (el: HTMLElement, p: "left" | "top" | "width" | "height") => parseFloat(el.style[p]);
const box = (el: HTMLElement) => ({ x: px(el, "left"), y: px(el, "top"), w: px(el, "width"), h: px(el, "height") });

function setViewport(width: number, height: number) {
  Object.defineProperty(window, "innerWidth", { configurable: true, value: width });
  Object.defineProperty(window, "innerHeight", { configurable: true, value: height });
}

function reduceMotion(on: boolean) {
  vi.stubGlobal("matchMedia", (q: string) => ({ matches: on && q.includes("prefers-reduced-motion"), media: q, addEventListener() {}, removeEventListener() {} }));
}

beforeEach(() => {
  setViewport(1280, 800);
  reduceMotion(false);
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

test("the screen says it is locked and asks for the token in a labelled password field", () => {
  show();
  expect(screen.getByRole("heading", { name: "this marquee is locked" })).toBeTruthy();
  expect(field().type).toBe("password");
  expect(document.activeElement).toBe(field());
});

test("every control is reachable by keyboard: the field, then the unlock button, nothing hidden from Tab", () => {
  show();
  const controls = [...document.querySelectorAll<HTMLElement>("input, button, a, select, textarea, [tabindex]")];
  expect(controls.map((c) => c.tagName)).toEqual(["INPUT", "BUTTON"]);
  expect(controls.every((c) => c.tabIndex >= 0)).toBe(true);
  expect(screen.getByRole("button", { name: "unlock" })).toBe(controls[1]);
});

test("submitting hands over the trimmed token and empties the field", () => {
  const onSubmit = vi.fn();
  show({ onSubmit });
  fireEvent.change(field(), { target: { value: "  abc  " } });
  fireEvent.click(screen.getByRole("button", { name: "unlock" }));
  expect(onSubmit).toHaveBeenCalledExactlyOnceWith("abc");
  expect(field().value).toBe("");
});

test("an empty field does not submit", () => {
  const onSubmit = vi.fn();
  show({ onSubmit });
  fireEvent.change(field(), { target: { value: "   " } });
  fireEvent.click(screen.getByRole("button", { name: "unlock" }));
  expect(onSubmit).not.toHaveBeenCalled();
});

test("a refused token reads as an alert and the field has focus again", () => {
  const first = show({ attempt: 0 });
  expect(screen.queryByRole("alert")).toBeNull();
  (document.activeElement as HTMLElement).blur();

  first.rerender(view({ rejected: true, attempt: 1 }));

  expect(screen.getByRole("alert").textContent).toBe("that token was not accepted. check it and try again.");
  expect(document.activeElement).toBe(field());
});

test("a second refusal is announced and focused like the first", () => {
  const first = show({ rejected: true, attempt: 1 });
  const firstAlert = screen.getByRole("alert");
  (document.activeElement as HTMLElement).blur();

  first.rerender(view({ rejected: true, attempt: 2 }));

  expect(screen.getByRole("alert")).not.toBe(firstAlert);
  expect(document.activeElement).toBe(field());
});

test("while the token is being checked the field cannot be changed and a second submit is ignored", () => {
  const onSubmit = vi.fn();
  show({ phase: "checking", onSubmit });
  expect(field().readOnly).toBe(true);
  expect(screen.getByRole("button", { name: "unlocking…" }).getAttribute("aria-disabled")).toBe("true");
  fireEvent.submit(field().form!);
  expect(onSubmit).not.toHaveBeenCalled();
});

test("once accepted the button says so", () => {
  show({ phase: "open" });
  expect(screen.getByRole("button", { name: "unlocked" })).toBeTruthy();
  expect(document.querySelector(".screen")!.getAttribute("data-phase")).toBe("open");
});

test("a click on the wall focuses the field and says what to do, in a calm line", () => {
  show();
  field().blur();
  fireEvent.click(tiles()[0]);
  expect(document.activeElement).toBe(field());
  expect(screen.getByRole("status").textContent).toBe("unlock to continue.");
  expect(document.querySelector(".unlock")!.getAttribute("data-nudge")).not.toBeNull();
});

test("a second click shakes the field again", () => {
  show();
  fireEvent.click(tiles()[0]);
  const first = document.querySelector(".unlock")!.getAttribute("data-nudge");
  fireEvent.click(tiles()[1]);
  expect(document.querySelector(".unlock")!.getAttribute("data-nudge")).not.toBe(first);
});

test("a click inside the form does not nudge", () => {
  show();
  fireEvent.click(screen.getByRole("heading"));
  fireEvent.click(field());
  expect(nudgeLine()).toBeNull();
});

test("the line goes away after three seconds", () => {
  vi.useFakeTimers();
  show();
  fireEvent.click(tiles()[0]);
  expect(nudgeLine()).toBeTruthy();
  act(() => void vi.advanceTimersByTime(2900));
  expect(nudgeLine()).toBeTruthy();
  act(() => void vi.advanceTimersByTime(200));
  expect(nudgeLine()).toBeNull();
});

test("a second click restarts the three seconds", () => {
  vi.useFakeTimers();
  show();
  fireEvent.click(tiles()[0]);
  act(() => void vi.advanceTimersByTime(2000));
  fireEvent.click(tiles()[0]);
  act(() => void vi.advanceTimersByTime(2000));
  expect(nudgeLine()).toBeTruthy();
  act(() => void vi.advanceTimersByTime(1100));
  expect(nudgeLine()).toBeNull();
});

test("typing clears the line at once", () => {
  show();
  fireEvent.click(tiles()[0]);
  fireEvent.change(field(), { target: { value: "a" } });
  expect(nudgeLine()).toBeNull();
});

test("the line never hides a fresh refusal: sending a token puts it away", () => {
  const first = show();
  fireEvent.change(field(), { target: { value: "wrong" } });
  fireEvent.click(tiles()[0]);
  expect(nudgeLine()).toBeTruthy();
  fireEvent.submit(field().form!);
  first.rerender(view({ phase: "checking" }));
  first.rerender(view({ rejected: true, attempt: 1 }));
  expect(screen.getByRole("alert")).toBeTruthy();
  expect(nudgeLine()).toBeNull();
});

test("a letter pressed with nothing focused lands in the field", () => {
  show();
  field().blur();
  expect(document.activeElement).toBe(document.body);
  fireEvent.keyDown(window, { key: "x" });
  expect(document.activeElement).toBe(field());
  expect(nudgeLine()).toBeNull();
});

test("any other stray key points at the field with the line", () => {
  show();
  field().blur();
  fireEvent.keyDown(window, { key: "Enter" });
  expect(document.activeElement).toBe(field());
  expect(nudgeLine()).toBeTruthy();
});

test("a key meant for a control, or a shortcut, is left alone", () => {
  show();
  const button = screen.getByRole("button", { name: "unlock" });
  button.focus();
  fireEvent.keyDown(button, { key: "Enter" });
  expect(document.activeElement).toBe(button);
  expect(nudgeLine()).toBeNull();

  (document.activeElement as HTMLElement).blur();
  fireEvent.keyDown(window, { key: "r", ctrlKey: true });
  fireEvent.keyDown(window, { key: "Tab" });
  expect(document.activeElement).toBe(document.body);
  expect(nudgeLine()).toBeNull();
});

test("Esc does nothing here: still locked, what was typed stays", () => {
  show();
  fireEvent.change(field(), { target: { value: "half" } });
  fireEvent.keyDown(field(), { key: "Escape" });
  field().blur();
  fireEvent.keyDown(window, { key: "Escape" });
  expect(field().value).toBe("half");
  expect(nudgeLine()).toBeNull();
  expect(screen.queryByRole("alert")).toBeNull();
  expect(screen.getByRole("heading", { name: "this marquee is locked" })).toBeTruthy();
});

test("with reduced motion the click shows the line and the field does not shake", () => {
  reduceMotion(true);
  show();
  fireEvent.click(tiles()[0]);
  expect(nudgeLine()).toBeTruthy();
  expect(document.activeElement).toBe(field());
  expect(document.querySelector(".unlock")!.getAttribute("data-nudge")).toBeNull();
});

test("the wall asks the network for nothing: no images, no art files", () => {
  show();
  expect(document.querySelectorAll("img, video, picture, canvas")).toHaveLength(0);
  for (const t of tiles()) expect(t.getAttribute("style")).not.toMatch(/url\(/);
});

test.each([
  [1280, 800],
  [390, 844],
  [320, 568],
])("at %i×%i the tiles and the form's slot cover the wall area, and the form stays on screen", (width, height) => {
  setViewport(width, height);
  show();
  const hit = new Uint8Array(width * height);
  // The wall sits 5px in from every edge, like the front page's, so the gap at the edge matches the gap between tiles.
  const inset = 5;
  const origin = { x: px(wall(), "left"), y: px(wall(), "top") };
  const paint = (x0: number, y0: number, w: number, h: number) => {
    for (let y = Math.max(0, Math.round(y0)); y < Math.min(height, Math.round(y0 + h)); y++) {
      for (let x = Math.max(0, Math.round(x0)); x < Math.min(width, Math.round(x0 + w)); x++) hit[y * width + x] = 1;
    }
  };
  for (const t of tiles()) {
    const b = box(t);
    paint(origin.x + b.x, origin.y + b.y, b.w, b.h);
  }
  const s = box(slot());
  paint(s.x, s.y, s.w, s.h);
  expect(hit.reduce((a, b) => a + b, 0)).toBe((width - 2 * inset) * (height - 2 * inset));

  expect(s.x).toBeGreaterThanOrEqual(0);
  expect(s.y).toBeGreaterThanOrEqual(0);
  expect(s.x + s.w).toBeLessThanOrEqual(width);
  expect(s.y + s.h).toBeLessThanOrEqual(height);
});

test("the tiles keep an even 10px between them: each box carries 5px of padding on every side", () => {
  show();
  for (const t of tiles()) expect(t.style.padding).toBe("5px");
});

const hue = (el: HTMLElement) => ({ h: el.style.getPropertyValue("--th"), s: el.style.getPropertyValue("--ts") });

test("at rest the last word takes the most colourful tile beside the words", () => {
  show();
  const s = box(slot());
  const o = { x: px(wall(), "left"), y: px(wall(), "top") };
  const beside = tiles().filter((t) => {
    const b = box(t);
    const x = o.x + b.x;
    const y = o.y + b.y;
    return x < s.x + s.w + 12 && x + b.w > s.x - 12 && y < s.y + s.h + 12 && y + b.h > s.y - 12;
  });
  expect(beside.length).toBeGreaterThan(2);
  const most = Math.max(...beside.map((t) => parseInt(hue(t).s)));
  const got = hue(slot());
  expect(parseInt(got.s)).toBe(most);
  expect(beside.map((t) => hue(t).h)).toContain(got.h);
});

test("with a mouse the words follow the tile under the pointer, then go back to rest", () => {
  show();
  const rest = hue(slot());
  const s = box(slot());
  const o = { x: px(wall(), "left"), y: px(wall(), "top") };
  const apart = (t: HTMLElement) => {
    const b = box(t);
    return o.x + b.x + b.w <= s.x || s.x + s.w <= o.x + b.x || o.y + b.y + b.h <= s.y || s.y + s.h <= o.y + b.y;
  };
  const target = tiles().find((t) => apart(t) && hue(t).h !== rest.h)!;
  const b = box(target);

  fireEvent.pointerMove(screen.getByRole("heading").closest(".screen")!, { pointerType: "mouse", clientX: b.x + b.w / 2, clientY: b.y + b.h / 2 });
  expect(hue(slot()).h).toBe(hue(target).h);

  fireEvent.pointerLeave(document.querySelector(".screen")!, { pointerType: "mouse" });
  expect(hue(slot())).toEqual(rest);
});

test("a touch pointer and the pointer over the form itself leave the colour as it was", () => {
  show();
  const rest = hue(slot());
  const other = tiles().find((t) => hue(t).h !== rest.h)!;
  const b = box(other);
  fireEvent.pointerMove(document.querySelector(".screen")!, { pointerType: "touch", clientX: b.x + 5, clientY: b.y + 5 });
  fireEvent.pointerMove(screen.getByRole("heading"), { pointerType: "mouse", clientX: b.x + 5, clientY: b.y + 5 });
  expect(hue(slot())).toEqual(rest);
});
