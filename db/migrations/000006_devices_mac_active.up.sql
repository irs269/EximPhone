-- A MAC address only has to be unique among ACTIVE devices, so a replaced or re-provisioned phone can be created again.
ALTER TABLE devices DROP CONSTRAINT IF EXISTS devices_mac_key;
CREATE UNIQUE INDEX devices_mac_active ON devices (mac) WHERE active;
