import { describe, expect, it, vi } from "vitest";
import { createUpdateNotice } from "./updateNotice";

describe("update notice", () => {
  it("shows once when the server comes back as a new build", () => {
    const show = vi.fn();
    const notice = createUpdateNotice({ show });
    notice.seen("1.0+a");
    notice.seen("1.0+a");
    expect(show).not.toHaveBeenCalled();
    notice.seen("1.1+b");
    notice.seen("1.1+b");
    expect(show).toHaveBeenCalledTimes(1);
    expect(show).toHaveBeenCalledWith("1.1+b");
    notice.seen("1.2+c");
    expect(show).toHaveBeenCalledTimes(2);
  });

  it("ignores an empty build", () => {
    const show = vi.fn();
    const notice = createUpdateNotice({ show });
    notice.seen("");
    notice.seen(undefined);
    notice.seen("1.0+a");
    notice.seen("");
    expect(show).not.toHaveBeenCalled();
  });

  it("waits while suppressed and shows on flush", () => {
    const show = vi.fn();
    let reading = true;
    const notice = createUpdateNotice({ show, suppressed: () => reading });
    notice.seen("1.0+a");
    notice.seen("1.1+b");
    expect(show).not.toHaveBeenCalled();
    reading = false;
    notice.flush();
    notice.flush();
    expect(show).toHaveBeenCalledTimes(1);
  });

  it("drops a waiting notice when the server goes back to the tab's build", () => {
    const show = vi.fn();
    let reading = true;
    const notice = createUpdateNotice({ show, suppressed: () => reading });
    notice.seen("1.0+a");
    notice.seen("1.1+b");
    notice.seen("1.0+a");
    reading = false;
    notice.flush();
    expect(show).not.toHaveBeenCalled();
  });
});
