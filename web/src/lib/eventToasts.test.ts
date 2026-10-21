import { describe, expect, it, vi } from "vitest";
import { createEventToastController } from "./eventToasts";

function sink() {
  return {
    success: vi.fn(),
    error: vi.fn(),
    warning: vi.fn(),
    info: vi.fn(),
  };
}

describe("server event toasts", () => {
  it("shows nothing for imported chapters", () => {
    const toast = sink();
    const controller = createEventToastController(toast);
    controller.handle("chapter.imported", { seriesTitle: "One Piece", chapter: "1" });
    controller.handle("chapter.imported", { seriesTitle: "Berserk", chapter: "1" });

    expect(toast.success).not.toHaveBeenCalled();
    expect(toast.info).not.toHaveBeenCalled();
  });

  it("drops notices while suppressed", () => {
    const toast = sink();
    const controller = createEventToastController(toast, { suppressed: () => true });
    controller.handle("download.failed", { title: "Hidden failure" });

    expect(toast.error).not.toHaveBeenCalled();
  });

  it("shows a new series and existing operational notices immediately", () => {
    const toast = sink();
    const controller = createEventToastController(toast);
    controller.handle("series.added", { title: "Series added", message: "Dorohedoro" });
    controller.handle("download.failed", { title: "Download failed", message: "Network" });
    controller.handle("health.issue", { title: "Health issue", message: "Disk" });
    controller.handle("cleanup.done", { title: "Cleanup done", message: "2 files" });

    expect(toast.success).toHaveBeenCalledWith("Series added", "Dorohedoro");
    expect(toast.error).toHaveBeenCalledWith("Download failed", "Network");
    expect(toast.warning).toHaveBeenCalledWith("Health issue", "Disk");
    expect(toast.info).toHaveBeenCalledWith("Cleanup done", "2 files");
  });
});
