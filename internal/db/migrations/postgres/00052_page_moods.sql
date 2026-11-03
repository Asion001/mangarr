-- +goose Up
-- Moods found in pages: each mood's prompts describe how its pages look,
-- page_moods keeps a chapter file's scores (by the file's hash, the model
-- and the prompts they were made with), and a worker task may belong to no
-- download job (scoring a chapter's pages isn't part of one).
ALTER TABLE audio_moods ADD COLUMN prompts jsonb NOT NULL DEFAULT '[]'::jsonb;
UPDATE audio_moods SET prompts = '["a peaceful manga page of a quiet everyday moment", "a calm manga panel of people relaxing at home", "a gentle slice of life manga scene in the sunshine"]'::jsonb WHERE slug = 'calm';
UPDATE audio_moods SET prompts = '["a sad manga page with someone crying", "a lonely melancholic manga panel in the rain", "a manga page of a tearful farewell"]'::jsonb WHERE slug = 'melancholy';
UPDATE audio_moods SET prompts = '["a tense manga page of a dangerous standoff", "a suspenseful manga panel with a worried face and sweat", "a manga page of someone being chased"]'::jsonb WHERE slug = 'tense';
UPDATE audio_moods SET prompts = '["a manga page of an intense fight with speed lines", "an action manga panel with an explosion", "a manga page of a sword battle"]'::jsonb WHERE slug = 'action';
UPDATE audio_moods SET prompts = '["a scary horror manga page with a monster in the dark", "a creepy manga panel with a terrified face", "a gory horror manga scene"]'::jsonb WHERE slug = 'horror';
UPDATE audio_moods SET prompts = '["a romantic manga page of a blushing couple", "a manga panel of a love confession with sparkles", "a manga page of two people about to kiss"]'::jsonb WHERE slug = 'romance';
UPDATE audio_moods SET prompts = '["a funny manga page with exaggerated comic expressions", "a chibi comedy manga panel", "a silly gag manga scene with people laughing"]'::jsonb WHERE slug = 'comedy';
UPDATE audio_moods SET prompts = '["a mysterious manga page with dark shadows", "a manga panel of a detective looking for clues", "an eerie mysterious manga scene at night"]'::jsonb WHERE slug = 'mystery';

CREATE TABLE page_moods (
    file_key text NOT NULL,
    model_version text NOT NULL,
    prompts_key text NOT NULL,
    pages integer NOT NULL DEFAULT 0,
    scores jsonb NOT NULL DEFAULT '[]'::jsonb,
    scanned_at timestamp with time zone NOT NULL,
    PRIMARY KEY (file_key, model_version)
);

ALTER TABLE worker_tasks ALTER COLUMN job_id DROP NOT NULL;

-- +goose Down
DELETE FROM worker_tasks WHERE job_id IS NULL;
ALTER TABLE worker_tasks ALTER COLUMN job_id SET NOT NULL;
DROP TABLE page_moods;
ALTER TABLE audio_moods DROP COLUMN prompts;
