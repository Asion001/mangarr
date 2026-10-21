import { expect, test, type Page } from "@playwright/test";

type LiveEvents = { emit: (type: string, payload: unknown) => void; count: () => number };

async function mockEventSource(page: Page) {
  await page.addInitScript(() => {
    class MockEventSource extends EventTarget {
      static sources: MockEventSource[] = [];
      onopen: ((event: Event) => void) | null = null;
      onerror: ((event: Event) => void) | null = null;
      readyState = 0;
      closed = false;

      constructor(_url: string | URL) {
        super();
        MockEventSource.sources.push(this);
        setTimeout(() => {
          if (!this.closed) {
            this.readyState = 1;
            this.onopen?.(new Event("open"));
          }
        }, 0);
      }

      close() {
        this.closed = true;
        this.readyState = 2;
      }
    }

    Object.defineProperty(window, "EventSource", { value: MockEventSource });
    Object.defineProperty(window, "__toastEvents", {
      value: {
        emit(type: string, payload: unknown) {
          for (const source of MockEventSource.sources.filter((item) => !item.closed)) {
            source.dispatchEvent(new MessageEvent(type, { data: JSON.stringify({ payload }) }));
          }
        },
        count() {
          return MockEventSource.sources.filter((item) => !item.closed).length;
        },
      },
    });
  });
}

async function mockAPI(page: Page) {
  const svg = '<svg xmlns="http://www.w3.org/2000/svg" width="600" height="900"><rect width="100%" height="100%" fill="#555"/></svg>';
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (/\/read\/chapters\/1\/pages\/\d+$/.test(path)) return route.fulfill({ contentType: "image/svg+xml", body: svg });
    if (path.endsWith("/auth/status")) {
      return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: [] } } });
    }
    if (path.endsWith("/updates")) return route.fulfill({ json: { items: [], total: 0, page: 1, pageSize: 50 } });
    if (path.endsWith("/read/settings")) return route.fulfill({ json: { defaults: { direction: "ltr", mode: "paged", spread: "single" }, series: null } });
    if (route.request().method() === "GET" && path.endsWith("/read/chapters/1")) {
      return route.fulfill({ json: {
        id: 1,
        seriesId: 1,
        seriesTitle: "Reader fixture",
        number: "1",
        readingDirection: "ltr",
        downloaded: true,
        canDownload: false,
        pages: [{ number: 1, mediaType: "image/svg+xml" }],
        progress: { page: 0, completed: false },
      } });
    }
    return route.fulfill({ json: {} });
  });
}

async function emit(page: Page, type: string, payload: unknown) {
  await page.evaluate(([eventType, eventPayload]) => {
    (window as unknown as { __toastEvents: LiveEvents }).__toastEvents.emit(eventType, eventPayload);
  }, [type, payload] as const);
}

test.beforeEach(async ({ page }) => {
  await mockEventSource(page);
  await mockAPI(page);
});

test("imported chapters make no toast, other notices still do", async ({ page }) => {
  await page.goto("/updates");
  await expect.poll(() => page.evaluate(() => (window as unknown as { __toastEvents: LiveEvents }).__toastEvents.count())).toBe(1);

  await emit(page, "chapter.imported", { seriesTitle: "One Piece", chapter: "10" });
  await emit(page, "chapter.imported", { seriesTitle: "One Piece", chapter: "11" });
  await emit(page, "chapter.imported", { seriesTitle: "One Piece", chapter: "12" });
  await emit(page, "chapter.imported", { seriesTitle: "Berserk", chapter: "1" });
  await emit(page, "series.added", { title: "Series added", message: "Dorohedoro" });

  await expect(page.getByText("Series added", { exact: true })).toBeVisible();
  await expect(page.getByText("Dorohedoro", { exact: true })).toBeVisible();
  await page.waitForTimeout(2_300);
  await expect(page.getByText(/One Piece|Berserk/)).toHaveCount(0);
});

test("server events never create toasts over the reader", async ({ page }) => {
  await page.goto("/read/1");
  await expect(page.getByRole("slider", { name: "Page" })).toBeVisible();
  await expect.poll(() => page.evaluate(() => (window as unknown as { __toastEvents: LiveEvents }).__toastEvents.count())).toBe(1);

  await emit(page, "chapter.imported", { seriesTitle: "Hidden import", chapter: "2" });
  await emit(page, "series.added", { title: "Hidden series", message: "Hidden title" });
  await emit(page, "download.failed", { title: "Hidden failure", message: "Network" });
  await emit(page, "health.issue", { title: "Hidden health", message: "Disk" });
  await emit(page, "cleanup.done", { title: "Hidden cleanup", message: "Files" });
  await page.waitForTimeout(2_300);

  for (const text of ["Hidden import", "Hidden series", "Hidden title", "Hidden failure", "Hidden health", "Hidden cleanup"]) {
    await expect(page.getByText(text, { exact: false })).toHaveCount(0);
  }
});
