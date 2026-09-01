-- +migrate Up
--
-- ffmpeg's own final out_time (14.3), the 15.3 fallback for VOB and AVI headers that
-- lie about duration. A column and not memory because 19.2 resumes across a restart.
ALTER TABLE jobs ADD COLUMN final_out_time_us INTEGER;
