import { t } from "../../lib/i18n/core";
import { useMemo } from "react";
import type { ReaderSettings } from "./settings";
import { tapAction, type TapAction } from "./zones";

// every zone edge (thirds, fifths) falls on this grid
const N = 15;

const colour: Record<TapAction, string> = { prev: "fill-err/45", next: "fill-ok/45", menu: "fill-info/35" };

/** TapZones shows where taps turn pages and open the menu, like Mihon's overlay. */
export function TapZones({ s }: { s: ReaderSettings }) {
  const { cells, labels } = useMemo(() => {
    const cells: TapAction[] = [];
    const sum: Record<TapAction, { x: number; y: number; n: number }> = { prev: { x: 0, y: 0, n: 0 }, next: { x: 0, y: 0, n: 0 }, menu: { x: 0, y: 0, n: 0 } };
    for (let row = 0; row < N; row++) {
      for (let col = 0; col < N; col++) {
        const x = (col + 0.5) / N;
        const y = (row + 0.5) / N;
        const a = tapAction(x, y, s);
        cells.push(a);
        sum[a].x += x;
        sum[a].y += y;
        sum[a].n++;
      }
    }
    const labels = (Object.keys(sum) as TapAction[]).filter((a) => sum[a].n > 0).map((a) => ({ a, x: sum[a].x / sum[a].n, y: sum[a].y / sum[a].n }));
    return { cells, labels };
  }, [s]);
  const name: Record<TapAction, string> = { prev: t("Previous"), next: t("Next"), menu: t("Menu") };
  return (
    // taps go through to the reader underneath
    <div data-testid="tap-zones" className="pointer-events-none absolute inset-0 z-10" aria-hidden="true">
      <svg className="size-full" viewBox={`0 0 ${N} ${N}`} preserveAspectRatio="none" shapeRendering="crispEdges">
        {cells.map((a, i) => (
          <rect key={i} x={i % N} y={Math.floor(i / N)} width={1} height={1} className={colour[a]} />
        ))}
      </svg>
      {labels.map(({ a, x, y }) => (
        <div
          key={a}
          className="absolute -translate-x-1/2 -translate-y-1/2 rounded bg-black/60 px-2 py-1 text-sm font-medium text-fg"
          style={{ left: `${x * 100}%`, top: `${y * 100}%` }}
        >
          {name[a]}
        </div>
      ))}
    </div>
  );
}
