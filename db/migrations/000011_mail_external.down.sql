DROP TABLE IF EXISTS mail_outbox, mail_external_recipients;
DELETE FROM mail_messages WHERE sender_id IS NULL;
ALTER TABLE mail_attachments ALTER COLUMN uploader_id SET NOT NULL;
ALTER TABLE mail_messages DROP CONSTRAINT IF EXISTS mail_sender_or_external;
DROP INDEX IF EXISTS mail_messages_header_id_idx;
ALTER TABLE mail_messages DROP COLUMN IF EXISTS header_id, DROP COLUMN IF EXISTS external_address, DROP COLUMN IF EXISTS external_name, DROP COLUMN IF EXISTS source;
ALTER TABLE mail_messages ALTER COLUMN sender_id SET NOT NULL;
