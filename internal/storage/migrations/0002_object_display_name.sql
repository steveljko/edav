-- FN for vCards, SUMMARY for calendar components: the human-readable label,
-- indexed on write so the admin UI and property filters need not parse payloads.
ALTER TABLE objects ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
