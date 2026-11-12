DROP INDEX IF EXISTS devices_mac_active;
ALTER TABLE devices ADD CONSTRAINT devices_mac_key UNIQUE (mac);
