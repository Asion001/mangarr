// Package genres gives genres and tags one name across metadata providers
// and sources. Providers and sites name the same genre differently ("Sci-Fi",
// "Science Fiction", "Фантастика", "Научная фантастика"); a series keeps the
// English name, and the web UI translates the known ones for display.
package genres

import "strings"

// known maps each English genre name to the other names it goes by.
var known = map[string][]string{
	"Action":           {"Экшен", "Экшн", "Боевик", "Бойовик", "Екшн", "Екшен"},
	"Adventure":        {"Приключения", "Пригоди"},
	"Comedy":           {"Комедия", "Комедія", "Comedies"},
	"Cyberpunk":        {"Киберпанк", "Кіберпанк"},
	"Detective":        {"Детектив"},
	"Drama":            {"Драма"},
	"Ecchi":            {"Этти", "Еччі", "Етті"},
	"Erotica":          {"Эротика", "Еротика"},
	"Fantasy":          {"Фэнтези", "Фентези", "Фентезі"},
	"Gender Bender":    {"Гендерная интрига", "Гендерна інтрига", "Genderswap"},
	"Harem":            {"Гарем"},
	"Hentai":           {"Хентай"},
	"Historical":       {"History", "Исторический", "Историческое", "История", "Історичне", "Історія"},
	"Horror":           {"Ужасы", "Хоррор", "Жахи", "Горор"},
	"Isekai":           {"Исекай", "Исэкай", "Ісекай"},
	"Josei":            {"Дзёсэй", "Дзёсей", "Дзесей", "Дзьосей"},
	"Kids":             {"Kodomo", "Кодомо", "Детское", "Дитяче"},
	"Mahou Shoujo":     {"Magical Girl", "Махо-сёдзё", "Махо-седзе", "Махо-сьодзьо"},
	"Martial Arts":     {"Боевые искусства", "Бойові мистецтва"},
	"Mecha":            {"Меха"},
	"Military":         {"Военное", "Военный", "Військове"},
	"Music":            {"Музыка", "Музика"},
	"Mystery":          {"Мистика", "Тайна", "Містика", "Таємниця"},
	"Omegaverse":       {"Омегаверс"},
	"Parody":           {"Пародия", "Пародія"},
	"Post-Apocalyptic": {"Post Apocalyptic", "Постапокалиптика", "Постапокаліптика"},
	"Psychological":    {"Психологическое", "Психологический", "Психология", "Психологічне", "Психологія"},
	"Reincarnation":    {"Реинкарнация", "Реінкарнація"},
	"Romance":          {"Романтика", "Романс"},
	"School":           {"School Life", "Школа"},
	"Sci-Fi":           {"Science Fiction", "Фантастика", "Научная фантастика", "Наукова фантастика"},
	"Seinen":           {"Сэйнэн", "Сейнен", "Сейнэн", "Сэйнен"},
	"Shoujo":           {"Shojo", "Сёдзё", "Седзе", "Сьодзьо"},
	"Shoujo Ai":        {"Shojo Ai", "Girls Love", "Сёдзё-ай", "Седзе-ай", "Сьодзьо-ай"},
	"Shounen":          {"Shonen", "Сёнэн", "Сёнен", "Сенэн", "Сенен", "Сьонен"},
	"Shounen Ai":       {"Shonen Ai", "Boys Love", "Сёнэн-ай", "Сёнен-ай", "Сенен-ай", "Сьонен-ай"},
	"Slice of Life":    {"Повседневность", "Повсякденність", "Буденність"},
	"Sports":           {"Sport", "Спорт"},
	"Supernatural":     {"Сверхъестественное", "Надприродне"},
	"Super Power":      {"Superpower", "Супер сила", "Суперсила"},
	"Survival":         {"Выживание", "Виживання"},
	"Thriller":         {"Suspense", "Триллер", "Трилер"},
	"Time Travel":      {"Путешествие во времени", "Подорож у часі"},
	"Tragedy":          {"Трагедия", "Трагедія"},
	"Vampire":          {"Vampires", "Вампиры", "Вампіри"},
	"Yaoi":             {"Яой"},
	"Yuri":             {"Юри"},
}

var byKey = func() map[string]string {
	m := map[string]string{}
	for name, others := range known {
		m[key(name)] = name
		for _, o := range others {
			m[key(o)] = name
		}
	}
	return m
}()

// key folds the differences that don't make a different genre: case, ё, and
// hyphens or underscores against spaces.
func key(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "ё", "е")
	s = strings.NewReplacer("-", " ", "_", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// Name is a genre's English name, or the name as given (trimmed) when it
// isn't a known genre.
func Name(s string) string {
	if name, ok := byKey[key(s)]; ok {
		return name
	}
	return strings.TrimSpace(s)
}

// Normalize renames the known genres in list to their English names and
// drops empties and repeats, keeping the first one's place.
func Normalize(list []string) []string {
	if len(list) == 0 {
		return list
	}
	out := make([]string, 0, len(list))
	seen := map[string]bool{}
	for _, s := range list {
		name := Name(s)
		if name == "" || seen[key(name)] {
			continue
		}
		seen[key(name)] = true
		out = append(out, name)
	}
	return out
}
