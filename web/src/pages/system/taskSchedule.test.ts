import { describe, expect, it } from "vitest";
import { sameSchedule, scheduleProblem, scheduleText, splitInterval, triggerText } from "./taskSchedule";

describe("task schedules", () => {
  it("reads schedules as text", () => {
    expect(scheduleText({ kind: "interval", intervalMinutes: 30 })).toBe("Every 30 minutes");
    expect(scheduleText({ kind: "interval", intervalMinutes: 60 })).toBe("Every hour");
    expect(scheduleText({ kind: "interval", intervalMinutes: 1440 })).toBe("Every 24 hours");
    expect(scheduleText({ kind: "interval", intervalMinutes: 4320 })).toBe("Every 3 days");
    expect(scheduleText({ kind: "daily", timesOfDay: ["03:30"], weekdays: [] })).toBe("Daily at 03:30");
    expect(scheduleText({ kind: "daily", timesOfDay: ["05:00"], weekdays: ["thu", "mon"] })).toBe("Mon and Thu at 05:00");
  });

  it("splits intervals into the largest even unit", () => {
    expect(splitInterval(90)).toEqual({ value: 90, unit: "minute" });
    expect(splitInterval(1440)).toEqual({ value: 24, unit: "hour" });
    expect(splitInterval(2880)).toEqual({ value: 2, unit: "day" });
  });

  it("validates like the server", () => {
    expect(scheduleProblem({ kind: "interval", intervalMinutes: 5 }, 15)).not.toBe("");
    expect(scheduleProblem({ kind: "interval", intervalMinutes: 15 }, 15)).toBe("");
    expect(scheduleProblem({ kind: "daily", timesOfDay: [] }, 15)).not.toBe("");
    expect(scheduleProblem({ kind: "daily", timesOfDay: ["03:30", "03:30"] }, 15)).not.toBe("");
    expect(scheduleProblem({ kind: "daily", timesOfDay: ["03:30"] }, 15)).toBe("");
  });

  it("treats all seven days as every day", () => {
    expect(sameSchedule({ kind: "daily", timesOfDay: ["03:30"], weekdays: ["mon", "tue", "wed", "thu", "fri", "sat", "sun"] }, { kind: "daily", timesOfDay: ["03:30"] })).toBe(true);
    expect(sameSchedule({ kind: "interval", intervalMinutes: 60 }, { kind: "interval", intervalMinutes: 30 })).toBe(false);
  });

  it("names who started a run", () => {
    expect(triggerText("scheduled")).toBe("Scheduled");
    expect(triggerText("manual · roma")).toBe("Manual · roma");
  });
});
