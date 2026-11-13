ALTER TABLE attachments DROP COLUMN IF EXISTS encrypted;
DROP TABLE IF EXISTS call_recordings, recording_rules;
ALTER TABLE calls DROP COLUMN IF EXISTS recorded;
