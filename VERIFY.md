# Verification against live systems

`plan.md` section 27 lists claims that were not confirmed against a running
system when it was written. Each one is recorded here with the date, the command
that produced the answer, and the answer.

Anything still `TODO` is an assumption the code currently rests on.

| # | Claim | Status |
|---|---|---|
| 1 | HDR10 metadata survives a `hevc_qsv` round trip | TODO |
| 2 | Plex `analyze` verb and path on the running PMS | TODO |
| 3 | ffprobe JSON path for the Dolby Vision profile, and DOVI record survival on copy | TODO |
| 4 | Chrome client profile produces Direct Stream for 8-bit and 10-bit HEVC | TODO |
| 5 | *arr renaming is off on all four instances | TODO |
| 6 | Current jellyfin-ffmpeg Debian package name and repo path | TODO |
| 7 | `vainfo` inside the container, and which driver loaded | TODO |
| 8 | 7.1 to 5.1 downmix produces a sensible channel layout | TODO |
| 9 | ASS to SRT conversion is readable on a real sample | TODO |
| 10 | `rename()` atomicity and same device number on the NFS mount | TODO |
| 11 | Whether `chown` succeeds on the NFS mount | TODO |
| 12 | CODARR tag survives a round trip into MP4 | TODO |
| 13 | VMAF spot-check, tuning the 1.35 hardware correction | TODO |
| 14 | VP9 hardware decode on this Gen 9.5 driver stack | TODO |
| 15 | A level-rewritten file plays on the pickiest client | TODO |
| 16 | Whether Bazarr preserves the CODARR global tag | TODO |

## How these get answered

Most need a real jellyfin-ffmpeg on the Intel iGPU. The method is a short-lived
pod in the `media` namespace on `tc-w1`, requesting the free
`gpu.intel.com/i915` slot, running as uid 568 with `media-library` mounted
read-only and an `emptyDir` for scratch. Sleep entrypoint, `kubectl exec` per
check, deleted afterwards. Nothing existing is touched.

Items 4, 13, 15 and 16 need human judgement or a television and stay open until
someone runs them by hand.
