DROP TABLE IF EXISTS audit_log, attendance_corrections, attendance_days, work_schedules, user_roles, users, roles, departments, sites CASCADE;
DROP FUNCTION IF EXISTS audit_log_immutable();
DROP TYPE IF EXISTS departure_status, arrival_status;
