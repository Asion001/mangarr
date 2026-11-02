import type { LucideIcon } from "lucide-react";
import { AudioLines, Bell, Bird, Bomb, Building, Car, Clock, CloudLightning, CloudRain, DoorOpen, Droplets, Flame, Footprints, HeartPulse, Key, Mic, Snowflake, Sparkles, Swords, TrainFront, Trees, Users, WandSparkles, Waves, Wind, Wine, Zap } from "lucide-react";
import { basePath } from "../../api/client";
import { t } from "../i18n/core";

/** trackIcons are the icons a pack sound may have (the server's soundtrack.TrackIcons). */
export const trackIcons: Record<string, LucideIcon> = {
  "cloud-rain": CloudRain,
  "cloud-lightning": CloudLightning,
  wind: Wind,
  snowflake: Snowflake,
  waves: Waves,
  droplets: Droplets,
  trees: Trees,
  bird: Bird,
  flame: Flame,
  building: Building,
  "train-front": TrainFront,
  car: Car,
  users: Users,
  bell: Bell,
  "door-open": DoorOpen,
  clock: Clock,
  wine: Wine,
  key: Key,
  swords: Swords,
  bomb: Bomb,
  zap: Zap,
  "heart-pulse": HeartPulse,
  footprints: Footprints,
  sparkles: Sparkles,
  "wand-sparkles": WandSparkles,
  mic: Mic,
  "audio-lines": AudioLines,
};

/** categoryColors give each kind of sound its colour. */
export const categoryColors: Record<string, string> = {
  weather: "#d29922",
  water: "#58a6ff",
  nature: "#56d364",
  city: "#8b93ff",
  objects: "#3fb98a",
  combat: "#f85149",
  body: "#f778ba",
  magic: "#a371f7",
  voices: "#f0883e",
  other: "#9aa2b1",
};

export const categoryColor = (c: string) => categoryColors[c] ?? categoryColors.other;

/** categoryName is a category in the interface language. */
export function categoryName(c: string): string {
  const names: Record<string, () => string> = {
    weather: () => t("Weather"),
    water: () => t("Water"),
    nature: () => t("Nature"),
    city: () => t("City"),
    objects: () => t("Objects"),
    combat: () => t("Combat"),
    body: () => t("Body"),
    magic: () => t("Magic"),
    voices: () => t("Voices"),
  };
  return names[c]?.() ?? t("Other");
}

export function TrackIcon({ icon, className }: { icon: string; className?: string }) {
  const Icon = trackIcons[icon] ?? AudioLines;
  return <Icon className={className} aria-hidden="true" />;
}

/** trackUrl is where a pack sound streams from. */
export const trackUrl = (id: number) => `${basePath}/api/v1/audio/tracks/${id}/stream`;
