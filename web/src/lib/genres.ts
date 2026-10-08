import { getLocale } from "./i18n/core";

// The server stores genres under their English names (internal/genres);
// these are the ones it knows, in the other interface languages.
const names: Record<string, { ru: string; uk: string }> = {
  Action: { ru: "Экшен", uk: "Екшн" },
  Adventure: { ru: "Приключения", uk: "Пригоди" },
  Comedy: { ru: "Комедия", uk: "Комедія" },
  Cyberpunk: { ru: "Киберпанк", uk: "Кіберпанк" },
  Detective: { ru: "Детектив", uk: "Детектив" },
  Drama: { ru: "Драма", uk: "Драма" },
  Ecchi: { ru: "Этти", uk: "Еччі" },
  Erotica: { ru: "Эротика", uk: "Еротика" },
  Fantasy: { ru: "Фэнтези", uk: "Фентезі" },
  "Gender Bender": { ru: "Гендерная интрига", uk: "Гендерна інтрига" },
  Harem: { ru: "Гарем", uk: "Гарем" },
  Hentai: { ru: "Хентай", uk: "Хентай" },
  Historical: { ru: "Исторический", uk: "Історичне" },
  Horror: { ru: "Ужасы", uk: "Жахи" },
  Isekai: { ru: "Исекай", uk: "Ісекай" },
  Josei: { ru: "Дзёсэй", uk: "Дзьосей" },
  Kids: { ru: "Детское", uk: "Дитяче" },
  "Mahou Shoujo": { ru: "Махо-сёдзё", uk: "Махо-сьодзьо" },
  "Martial Arts": { ru: "Боевые искусства", uk: "Бойові мистецтва" },
  Mecha: { ru: "Меха", uk: "Меха" },
  Military: { ru: "Военное", uk: "Військове" },
  Music: { ru: "Музыка", uk: "Музика" },
  Mystery: { ru: "Мистика", uk: "Містика" },
  Omegaverse: { ru: "Омегаверс", uk: "Омегаверс" },
  Parody: { ru: "Пародия", uk: "Пародія" },
  "Post-Apocalyptic": { ru: "Постапокалиптика", uk: "Постапокаліптика" },
  Psychological: { ru: "Психологическое", uk: "Психологічне" },
  Reincarnation: { ru: "Реинкарнация", uk: "Реінкарнація" },
  Romance: { ru: "Романтика", uk: "Романтика" },
  School: { ru: "Школа", uk: "Школа" },
  "Sci-Fi": { ru: "Фантастика", uk: "Фантастика" },
  Seinen: { ru: "Сэйнэн", uk: "Сейнен" },
  Shoujo: { ru: "Сёдзё", uk: "Сьодзьо" },
  "Shoujo Ai": { ru: "Сёдзё-ай", uk: "Сьодзьо-ай" },
  Shounen: { ru: "Сёнэн", uk: "Сьонен" },
  "Shounen Ai": { ru: "Сёнэн-ай", uk: "Сьонен-ай" },
  "Slice of Life": { ru: "Повседневность", uk: "Повсякденність" },
  Sports: { ru: "Спорт", uk: "Спорт" },
  Supernatural: { ru: "Сверхъестественное", uk: "Надприродне" },
  "Super Power": { ru: "Суперсила", uk: "Суперсила" },
  Survival: { ru: "Выживание", uk: "Виживання" },
  Thriller: { ru: "Триллер", uk: "Трилер" },
  "Time Travel": { ru: "Путешествие во времени", uk: "Подорож у часі" },
  Tragedy: { ru: "Трагедия", uk: "Трагедія" },
  Vampire: { ru: "Вампиры", uk: "Вампіри" },
  Yaoi: { ru: "Яой", uk: "Яой" },
  Yuri: { ru: "Юри", uk: "Юрі" },
};

/** genreName is a genre or tag in the interface language, when it is a known one. */
export function genreName(genre: string): string {
  const locale = getLocale();
  return locale === "en" ? genre : names[genre]?.[locale] ?? genre;
}
