-- +migrate Up
--
-- The probe cache in 10.1 is keyed on the ffmpeg version alone, so changing
-- qsv_device in the UI leaves a stale answer about a device that is no longer
-- being used. Record the device the probe actually ran against, so the cache can
-- be invalidated on either axis.
ALTER TABLE hw_capabilities ADD COLUMN device TEXT NOT NULL DEFAULT '';
