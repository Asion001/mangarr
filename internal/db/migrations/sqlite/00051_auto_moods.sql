-- +goose Up
-- Automatic moods: cues found by mangarr (source auto, with a confidence
-- and the detector's version) next to the ones people place, and words in
-- chapter titles that suggest each mood.
ALTER TABLE soundtrack_cues ADD COLUMN source TEXT NOT NULL DEFAULT 'manual';
ALTER TABLE soundtrack_cues ADD COLUMN confidence REAL;
ALTER TABLE soundtrack_cues ADD COLUMN model_version TEXT NOT NULL DEFAULT '';
ALTER TABLE audio_moods ADD COLUMN keywords TEXT NOT NULL DEFAULT '[]';
UPDATE audio_moods SET keywords = '["morning", "home", "tea", "garden", "rest", "peace", "peaceful", "everyday", "daily", "quiet", "picnic", "holiday", "sunny", "spring", "breakfast", "cooking"]' WHERE slug = 'calm';
UPDATE audio_moods SET keywords = '["farewell", "goodbye", "funeral", "memory", "memories", "loss", "lost", "rain", "tears", "alone", "lonely", "grave", "regret", "parting", "sorrow", "grief"]' WHERE slug = 'melancholy';
UPDATE audio_moods SET keywords = '["chase", "trap", "escape", "hunt", "hunted", "threat", "ambush", "deadline", "hostage", "siege", "pursuit", "danger", "crisis", "standoff", "countdown", "betrayal"]' WHERE slug = 'tense';
UPDATE audio_moods SET keywords = '["battle", "fight", "war", "duel", "clash", "attack", "raid", "tournament", "match", "versus", "vs", "combat", "showdown", "assault", "boss", "strike"]' WHERE slug = 'action';
UPDATE audio_moods SET keywords = '["blood", "death", "dead", "ghost", "curse", "cursed", "monster", "nightmare", "haunted", "corpse", "demon", "zombie", "massacre", "horror", "scream", "grave"]' WHERE slug = 'horror';
UPDATE audio_moods SET keywords = '["date", "confession", "kiss", "love", "wedding", "heart", "sweetheart", "crush", "proposal", "valentine", "bride", "lovers", "romance", "promise", "together", "dance"]' WHERE slug = 'romance';
UPDATE audio_moods SET keywords = '["festival", "party", "prank", "beach", "vacation", "trip", "pool", "karaoke", "costume", "mischief", "shopping", "game", "club", "dorm", "sports", "cultural"]' WHERE slug = 'comedy';
UPDATE audio_moods SET keywords = '["secret", "secrets", "mystery", "clue", "truth", "riddle", "investigation", "unknown", "shadow", "shadows", "disappearance", "missing", "detective", "hidden", "puzzle", "enigma"]' WHERE slug = 'mystery';

-- +goose Down
ALTER TABLE audio_moods DROP COLUMN keywords;
ALTER TABLE soundtrack_cues DROP COLUMN model_version;
ALTER TABLE soundtrack_cues DROP COLUMN confidence;
ALTER TABLE soundtrack_cues DROP COLUMN source;
