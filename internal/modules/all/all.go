// Package all registers every built-in module implementation.
package all

import (
	_ "github.com/Asion001/mangarr/internal/modules/library/kavita"
	_ "github.com/Asion001/mangarr/internal/modules/library/komga"
	_ "github.com/Asion001/mangarr/internal/modules/mediaserver/jellyfin"
	_ "github.com/Asion001/mangarr/internal/modules/mediaserver/silo"
	_ "github.com/Asion001/mangarr/internal/modules/metadata/anilist"
	_ "github.com/Asion001/mangarr/internal/modules/metadata/kitsu"
	_ "github.com/Asion001/mangarr/internal/modules/metadata/mangadex"
	_ "github.com/Asion001/mangarr/internal/modules/metadata/mangaupdates"
	_ "github.com/Asion001/mangarr/internal/modules/metadata/shikimori"
	_ "github.com/Asion001/mangarr/internal/modules/notify/apprise"
	_ "github.com/Asion001/mangarr/internal/modules/notify/discord"
	_ "github.com/Asion001/mangarr/internal/modules/notify/gotify"
	_ "github.com/Asion001/mangarr/internal/modules/notify/ntfy"
	_ "github.com/Asion001/mangarr/internal/modules/notify/telegram"
	_ "github.com/Asion001/mangarr/internal/modules/notify/webhook"
	_ "github.com/Asion001/mangarr/internal/modules/source/native"
	_ "github.com/Asion001/mangarr/internal/modules/source/suwayomi"
	_ "github.com/Asion001/mangarr/internal/modules/upscale/local"
	_ "github.com/Asion001/mangarr/internal/modules/upscale/workers"
)
