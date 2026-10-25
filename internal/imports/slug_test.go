package imports

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/modules/metadata"
	"github.com/Asion001/mangarr/internal/titlematch"
)

func TestSlugTitle(t *testing.T) {
	for link, want := range map[string]string{
		"/manga/chronicles-of-the-demon-faction":              "chronicles of the demon faction",
		"https://mangalib.me/ru/manga/7965--chainsaw-man?x=1": "chainsaw man",
		"/manga/the-beginning-after-the-end/":                 "the beginning after the end",
		"/manga/12345":                                        "",
		"/manga/solo":                                         "",
		"/manga/a-b":                                          "",
		"/title/Начало-после-конца":                           "",
	} {
		if got := SlugTitle(link); got != want {
			t.Errorf("%s: %q, want %q", link, got, want)
		}
	}
	// the slug's name is a confident match for the provider's title
	if sc := titlematch.Best([]string{"Chronicles of the Demon Faction", "Madojeonsaenggi"}, SlugTitle("/manga/chronicles-of-the-demon-faction")); sc < MetadataThreshold {
		t.Fatalf("score %v", sc)
	}
}

func TestPauseLeft(t *testing.T) {
	paused := fmt.Errorf("AniList: %w", &metadata.PausedError{Err: errors.New("down"), Until: time.Now().Add(time.Hour)})
	if got := pauseLeft([]error{errors.New("other"), paused}); got != maxPauseWait {
		t.Fatalf("capped wait: %v", got)
	}
	if got := pauseLeft([]error{errors.New("other")}); got != 0 {
		t.Fatalf("no pause: %v", got)
	}
}
