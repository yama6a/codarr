-- +migrate Up
--
-- The probe cache of 10.1 is keyed on the ffmpeg version alone, so a qsv_device change
-- leaves a stale answer. Record the device probed so the cache invalidates on either axis.
ALTER TABLE hw_capabilities ADD COLUMN device TEXT NOT NULL DEFAULT '';
