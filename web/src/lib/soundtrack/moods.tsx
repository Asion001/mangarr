import type { LucideIcon } from "lucide-react";
import { Castle, CloudRain, Coffee, Drama, Eye, Flame, Ghost, Heart, Leaf, Moon, Mountain, Music, Rocket, Skull, Smile, Snowflake, Sparkles, Sun, Sunrise, Swords, Trees, Waves, Wind, Zap } from "lucide-react";
import { t } from "../i18n/core";
import type { Mood } from "./core";

/** moodIcons are the icons a mood may have (the server's soundtrack.Icons). */
export const moodIcons: Record<string, LucideIcon> = {
  leaf: Leaf,
  "cloud-rain": CloudRain,
  zap: Zap,
  flame: Flame,
  ghost: Ghost,
  heart: Heart,
  smile: Smile,
  moon: Moon,
  music: Music,
  sun: Sun,
  snowflake: Snowflake,
  swords: Swords,
  waves: Waves,
  wind: Wind,
  coffee: Coffee,
  sparkles: Sparkles,
  skull: Skull,
  mountain: Mountain,
  castle: Castle,
  rocket: Rocket,
  trees: Trees,
  drama: Drama,
  eye: Eye,
  sunrise: Sunrise,
};

/** moodColors are colours offered for new moods (any #rrggbb works). */
export const moodColors = ["#3fb98a", "#58a6ff", "#d29922", "#f0883e", "#a371f7", "#f778ba", "#e3d34b", "#8b93ff", "#f85149", "#2bb5c9", "#9aa2b1"];

export function MoodIcon({ icon, className }: { icon: string; className?: string }) {
  const Icon = moodIcons[icon] ?? Music;
  return <Icon className={className} aria-hidden="true" />;
}

// the built-in moods' names, shown in the interface language until an
// admin renames them
const builtinNames: Record<string, [string, () => string]> = {
  calm: ["Calm", () => t("Calm")],
  melancholy: ["Melancholy", () => t("Melancholy")],
  tense: ["Tense", () => t("Tense")],
  action: ["Action", () => t("Action")],
  horror: ["Horror", () => t("Horror")],
  romance: ["Romance", () => t("Romance")],
  comedy: ["Comedy", () => t("Comedy")],
  mystery: ["Mystery", () => t("Mystery")],
};

/** moodName is a mood's name, translated while it's a built-in one's own. */
export function moodName(m: Pick<Mood, "slug" | "name">): string {
  const b = builtinNames[m.slug];
  return b && b[0] === m.name ? b[1]() : m.name;
}
