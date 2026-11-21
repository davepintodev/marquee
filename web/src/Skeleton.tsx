import type { CSSProperties } from "react";
import "./skeleton.css";

type Shape =
  | "poster"
  | "card"
  | "row"
  | "line"
  | "head"
  | "dot"
  | "h2"
  | "why"
  | "face"
  | "pill";

export function Sk({
  shape,
  width,
  style,
}: {
  shape: Shape;
  width?: number | string;
  style?: CSSProperties;
}) {
  return (
    <span
      className={`sk sk-${shape}`}
      aria-hidden="true"
      style={width === undefined ? style : { width, ...style }}
    />
  );
}

export function Say({ children }: { children: string }) {
  return (
    <span className="sr" role="status">
      {children}
    </span>
  );
}

export function Busy({ say, width = 56 }: { say: string; width?: number }) {
  return (
    <>
      <Sk shape="line" width={width} />
      <Say>{say}</Say>
    </>
  );
}

const range = (n: number) => Array.from({ length: n }, (_, i) => i);


export function ActivitySkeleton() {
  return (
    <div aria-busy="true">
      <Say>Loading activity</Say>
      <Sk shape="line" width={210} style={{ marginTop: "var(--space-2)" }} />
      <div
        className="board-cols"
        style={{ "--n": 2 } as CSSProperties}
        aria-hidden="true"
      >
        <div className="board-main">
          <section className="board-section">
            <Sk shape="h2" width={132} />
            <Sk
              shape="line"
              width={170}
              style={{ margin: "var(--space-2) 0 var(--space-3)" }}
            />
            <div className="sk-pair">
              {range(2).map((i) => (
                <div key={i} className="sk-show">
                  <Sk shape="card" />
                  <Sk shape="line" width="62%" />
                  <Sk shape="line" width="38%" />
                </div>
              ))}
            </div>
          </section>
          <section className="board-section">
            <Sk shape="h2" width={92} />
            <Sk
              shape="line"
              width={230}
              style={{ margin: "var(--space-2) 0 var(--space-3)" }}
            />
            <div className="sk-strip">
              {range(4).map((i) => (
                <Sk key={i} shape="poster" />
              ))}
            </div>
          </section>
        </div>
        <section className="board-section">
          <Sk shape="h2" width={88} />
          <Sk
            shape="line"
            width={150}
            style={{ margin: "var(--space-2) 0 var(--space-3)" }}
          />
          <Sk
            shape="line"
            width={64}
            style={{ marginBottom: "var(--space-3)" }}
          />
          <div className="sk-log">
            {range(7).map((i) => (
              <div key={i}>
                <Sk shape="line" width={`${58 - (i % 3) * 8}%`} />
                <Sk shape="line" width={`${86 - (i % 2) * 22}%`} />
              </div>
            ))}
          </div>
        </section>
      </div>
    </div>
  );
}

export function HealthSkeleton() {
  return (
    <div className="bd-body" aria-hidden="true">
      <header className="sk-verdict">
        <Sk shape="head" width="min(560px, 86%)" />
        <Sk shape="why" width="min(400px, 70%)" />
        <div className="sk-bar">
          <Sk shape="line" width={170} />
          <Sk shape="line" width={84} />
        </div>
      </header>
      <section className="bd-sec">
        <Sk shape="line" width="min(430px, 80%)" />
        <div className="sk-stops">
          {range(7).map((i) => (
            <div key={i}>
              <Sk shape="dot" />
              <Sk shape="line" width="70%" />
            </div>
          ))}
        </div>
      </section>
      <section className="bd-sec sk-stack">
        <Sk shape="h2" width={84} />
        <Sk shape="line" width="min(260px, 60%)" />
      </section>
      <section className="bd-sec sk-stack">
        <Sk shape="h2" width={104} />
        <Sk shape="line" width="min(340px, 80%)" />
      </section>
    </div>
  );
}

export function TitleSkeleton() {
  return (
    <div aria-hidden="true">
      <section className="ts-sec">
        <div className="ts-sec-head">
          <Sk shape="h2" width={110} />
        </div>
        <div className="sk-stack">
          {range(3).map((i) => (
            <Sk key={i} shape="row" />
          ))}
        </div>
      </section>
      <section className="ts-sec">
        <div className="ts-sec-head">
          <Sk shape="h2" width={64} />
        </div>
        <ul className="ts-cast">
          {range(4).map((i) => (
            <li key={i}>
              <Sk shape="face" />
              <Sk
                shape="line"
                width="64%"
                style={{ alignSelf: "end", marginBottom: 5 }}
              />
              <Sk
                shape="line"
                width="40%"
                style={{ alignSelf: "start", marginTop: 5 }}
              />
            </li>
          ))}
        </ul>
      </section>
      <section className="ts-sec">
        <div className="ts-sec-head">
          <Sk shape="h2" width={140} />
        </div>
        <ul className="ts-sim">
          {range(6).map((i) => (
            <li key={i} className="sk sk-poster" />
          ))}
        </ul>
      </section>
    </div>
  );
}

export function ReleasesSkeleton({ say }: { say: string }) {
  return (
    <div className="rl rl-t" aria-busy="true">
      <Say>{say}</Say>
      <div aria-hidden="true">
        <div className="rl-head">
          <Sk shape="line" width={120} />
          <div className="rl-filters">
            <Sk shape="line" width={28} />
            <Sk shape="line" width={40} />
            <Sk shape="line" width={40} />
          </div>
        </div>
        <div className="rl-main">
          <div className="rt-head">
            <Sk shape="line" width={48} />
            <Sk shape="line" width={56} />
            <Sk shape="line" width={44} />
            <Sk shape="line" width={40} />
            <Sk shape="line" width={32} style={{ justifySelf: "end" }} />
            <span />
          </div>
          <ul className="rt-list">
            {range(5).map((i) => (
              <li key={i} className="rt-row" data-top={i === 0 || undefined}>
                <span className="rt-q">
                  <Sk shape="line" width={36} />
                  <Sk shape="line" width={26} style={{ marginTop: 6 }} />
                </span>
                <span className="rt-file">
                  <Sk shape="line" width={`${92 - (i % 3) * 14}%`} />
                  <Sk
                    shape="line"
                    width={`${48 - (i % 2) * 14}%`}
                    style={{ marginTop: 6 }}
                  />
                </span>
                <span className="rt-src">
                  <Sk shape="line" width={52} />
                </span>
                <span className="rt-c">
                  <Sk shape="line" width={44} />
                </span>
                <span className="rt-size">
                  <Sk shape="line" width={52} />
                </span>
                <span className="rt-act">
                  <Sk
                    shape={i === 0 ? "pill" : "line"}
                    width={i === 0 ? 76 : 48}
                  />
                </span>
              </li>
            ))}
          </ul>
        </div>
      </div>
    </div>
  );
}
