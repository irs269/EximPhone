DROP TABLE IF EXISTS announcement_acks, announcements;
ALTER TABLE attendance_days DROP COLUMN IF EXISTS early_validation, DROP COLUMN IF EXISTS validated_by,
  DROP COLUMN IF EXISTS validated_at, DROP COLUMN IF EXISTS validation_comment;
DELETE FROM roles WHERE name = 'direction';
