package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

const mediaColumns = `id, path, root_id, arr_instance_id, arr_entity_id, size_bytes, mtime,
	nlink, fingerprint, probe_json, media_info_json, analyzed_at, plan_json, plan_kind,
	plan_reasons, container, video_codec, video_profile, video_level, video_bitrate,
	video_bitrate_src, is_hdr, fingerprint_algo, codarr_tagged, codarr_policy_hash,
	codarr_job_id, codarr_processed_at, codarr_output_fingerprint, codarr_output_size,
	codarr_output_mtime, codarr_output_full_hash, provenance, integrity_checked_at,
	status, ignored, last_error, created_at, updated_at`

// mediaSortColumns is a whitelist: the sort key reaches the query as an
// identifier, so it can never come from the request string.
//
//nolint:gochecknoglobals // lookup table, immutable
var mediaSortColumns = map[MediaSort]string{
	SortPath:       "path",
	SortSize:       "size_bytes",
	SortStatus:     "status",
	SortPlanKind:   "plan_kind",
	SortVideoCodec: "video_codec",
	SortBitrate:    "video_bitrate",
	SortUpdatedAt:  "updated_at",
}

// UpsertMediaFile inserts a file seen by a scan or webhook, or refreshes the
// identity fields of one already known. Provenance is re-derived here rather
// than taken from the argument: plan.md 12 makes it a function of the recorded
// output fingerprint and the current one, and nothing else.
func (s *store) UpsertMediaFile(ctx context.Context, m domain.MediaFile) (domain.MediaFile, error) {
	var out domain.MediaFile

	err := s.write(ctx, func(tx *sql.Tx) error {
		var recorded sql.NullString

		err := tx.QueryRowContext(ctx,
			`SELECT codarr_output_fingerprint FROM media_files WHERE path = ?`, m.Path,
		).Scan(&recorded)
		if err != nil && !notFound(err) {
			return fmt.Errorf("select media output fingerprint: %w", err)
		}

		m.Provenance = domain.DeriveProvenance(recorded.String, m.Fingerprint)

		const upsert = `
			INSERT INTO media_files (path, root_id, arr_instance_id, arr_entity_id, size_bytes,
				mtime, nlink, fingerprint, fingerprint_algo, provenance, status, ignored,
				created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (path) DO UPDATE SET
				root_id          = excluded.root_id,
				arr_instance_id  = excluded.arr_instance_id,
				arr_entity_id    = excluded.arr_entity_id,
				size_bytes       = excluded.size_bytes,
				mtime            = excluded.mtime,
				nlink            = excluded.nlink,
				fingerprint      = excluded.fingerprint,
				fingerprint_algo = excluded.fingerprint_algo,
				provenance       = excluded.provenance,
				status           = excluded.status,
				updated_at       = excluded.updated_at`

		_, err = tx.ExecContext(ctx, upsert,
			m.Path, ptrValue(m.RootID), ptrValue(m.ArrInstanceID), ptrValue(m.ArrEntityID),
			m.SizeBytes, m.MTime, nullInt64(int64(m.NLink)), nullString(m.Fingerprint),
			nullString(m.FingerprintAlgo), string(m.Provenance), string(m.Status), m.Ignored,
			formatTime(m.CreatedAt), formatTime(m.UpdatedAt))
		if err != nil {
			return fmt.Errorf("upsert media file: %w", err)
		}

		out, err = scanMediaFile(tx.QueryRowContext(ctx,
			`SELECT `+mediaColumns+` FROM media_files WHERE path = ?`, m.Path))
		if err != nil {
			return fmt.Errorf("reload media file: %w", err)
		}

		return nil
	})
	if err != nil {
		return domain.MediaFile{}, err
	}

	return out, nil
}

func (s *store) GetMediaFile(ctx context.Context, id int64) (domain.MediaFile, error) {
	const query = `SELECT ` + mediaColumns + ` FROM media_files WHERE id = ?`

	return s.mediaRow(s.db.read.QueryRowContext(ctx, query, id))
}

func (s *store) GetMediaFileByPath(ctx context.Context, path string) (domain.MediaFile, error) {
	const query = `SELECT ` + mediaColumns + ` FROM media_files WHERE path = ?`

	return s.mediaRow(s.db.read.QueryRowContext(ctx, query, path))
}

// ListMediaFiles is the library table of plan.md 18.2: filter, sort, paginate,
// and a total count for the pager taken under the same filter.
func (s *store) ListMediaFiles(ctx context.Context, f MediaFilter) ([]domain.MediaFile, int, error) {
	where, args := mediaWhere(f)

	var total int
	if err := s.db.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_files`+where, args...).
		Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count media files: %w", err)
	}

	column, ok := mediaSortColumns[f.Sort]
	if !ok {
		column = mediaSortColumns[SortPath]
	}

	direction := " ASC"
	if f.Descending {
		direction = " DESC"
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}

	//nolint:gosec // column comes from mediaSortColumns and every value is a placeholder
	query := `SELECT ` + mediaColumns + ` FROM media_files` + where +
		` ORDER BY ` + column + direction + `, id ASC LIMIT ? OFFSET ?`

	rows, err := s.db.read.QueryContext(ctx, query, append(args, limit, f.Offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("select media files: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []domain.MediaFile{}

	for rows.Next() {
		m, err := scanMediaFile(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan media file: %w", err)
		}

		out = append(out, m)
	}

	if err := closeRows(rows); err != nil {
		return nil, 0, err
	}

	return out, total, nil
}

func (s *store) ListMediaStatsByRoot(ctx context.Context, rootID int64) ([]MediaStat, error) {
	const query = `
		SELECT id, path, size_bytes, mtime, status, ignored FROM media_files
		WHERE root_id = ? ORDER BY path`

	rows, err := s.db.read.QueryContext(ctx, query, rootID)
	if err != nil {
		return nil, fmt.Errorf("select media stats: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []MediaStat{}

	for rows.Next() {
		var (
			m      MediaStat
			status string
		)

		if err := rows.Scan(&m.ID, &m.Path, &m.SizeBytes, &m.MTime, &status, &m.Ignored); err != nil {
			return nil, fmt.Errorf("scan media stat: %w", err)
		}

		m.Status = domain.MediaStatus(status)
		out = append(out, m)
	}

	if err := closeRows(rows); err != nil {
		return nil, err
	}

	return out, nil
}

// UpdateMediaAnalysis writes one analysis pass and re-derives provenance, which
// plan.md 12 requires on every analysis rather than only at promotion.
func (s *store) UpdateMediaAnalysis(ctx context.Context, u AnalysisUpdate) error {
	planJSON, err := marshalJSON(u.Plan)
	if err != nil {
		return err
	}

	reasons, err := marshalStrings(u.PlanReasons)
	if err != nil {
		return err
	}

	return s.write(ctx, func(tx *sql.Tx) error {
		var recorded sql.NullString

		err := tx.QueryRowContext(ctx,
			`SELECT codarr_output_fingerprint FROM media_files WHERE id = ?`, u.MediaFileID,
		).Scan(&recorded)
		if notFound(err) {
			return ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("select media output fingerprint: %w", err)
		}

		provenance := domain.DeriveProvenance(recorded.String, u.Fingerprint)

		const update = `
			UPDATE media_files SET
				size_bytes = ?, mtime = ?, nlink = ?, fingerprint = ?, fingerprint_algo = ?,
				probe_json = ?, media_info_json = ?, analyzed_at = ?, plan_json = ?,
				plan_kind = ?, plan_reasons = ?, container = ?, video_codec = ?,
				video_profile = ?, video_level = ?, video_bitrate = ?, video_bitrate_src = ?,
				is_hdr = ?, codarr_tagged = ?, codarr_policy_hash = ?, provenance = ?,
				status = ?, last_error = ?, updated_at = ?
			WHERE id = ?`

		res, err := tx.ExecContext(ctx, update,
			u.SizeBytes, u.MTime, nullInt64(int64(u.NLink)), nullString(u.Fingerprint),
			nullString(u.FingerprintAlgo), nullString(u.ProbeJSON), nullString(u.MediaInfoJSON),
			formatTime(u.AnalyzedAt), planJSON, nullString(string(u.PlanKind)), reasons,
			nullString(u.Container), nullString(u.VideoCodec), nullString(u.VideoProfile),
			nullString(u.VideoLevel), nullInt64(int64(u.VideoBitrate)),
			nullString(string(u.VideoBitrateSrc)), u.IsHDR, u.CodarrTagged,
			nullString(u.CodarrPolicyHash), string(provenance), string(u.Status),
			nullString(u.LastError), formatTime(u.AnalyzedAt), u.MediaFileID)
		if err != nil {
			return fmt.Errorf("update media analysis: %w", err)
		}

		return requireOne(res)
	})
}

func (s *store) SetMediaStatus(ctx context.Context, id int64, status domain.MediaStatus, lastError string) error {
	const query = `UPDATE media_files SET status = ?, last_error = ?, updated_at = ? WHERE id = ?`

	return s.execOne(ctx, query, string(status), nullString(lastError), formatTime(time.Now()), id)
}

func (s *store) SetMediaIgnored(ctx context.Context, id int64, ignored bool) error {
	const query = `UPDATE media_files SET ignored = ?, updated_at = ? WHERE id = ?`

	return s.execOne(ctx, query, ignored, formatTime(time.Now()), id)
}

// MarkMediaMissing is the prune half of the scheduled scan (plan.md 13.2). The
// row and its history are kept; only the status changes.
func (s *store) MarkMediaMissing(ctx context.Context, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	query := `UPDATE media_files SET status = ?, updated_at = ? WHERE id IN (` +
		placeholders(len(ids)) + `)`

	args := make([]any, 0, len(ids)+2)
	args = append(args, string(domain.MediaMissing), formatTime(time.Now()))

	for _, id := range ids {
		args = append(args, id)
	}

	res, err := s.exec(ctx, query, args...)
	if err != nil {
		return 0, err
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}

	return n, nil
}

// SetMediaIntegrity is POST /api/media/{id}/verify-integrity: a recomputed
// fingerprint re-derives provenance without touching the plan.
func (s *store) SetMediaIntegrity(ctx context.Context, id int64, fingerprint, fullHash string, at time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		var recorded sql.NullString

		err := tx.QueryRowContext(ctx,
			`SELECT codarr_output_fingerprint FROM media_files WHERE id = ?`, id).Scan(&recorded)
		if notFound(err) {
			return ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("select media output fingerprint: %w", err)
		}

		const update = `
			UPDATE media_files SET
				fingerprint = ?, provenance = ?, integrity_checked_at = ?, updated_at = ?
			WHERE id = ?`

		res, err := tx.ExecContext(ctx, update, nullString(fingerprint),
			string(domain.DeriveProvenance(recorded.String, fingerprint)),
			formatTime(at), formatTime(at), id)
		if err != nil {
			return fmt.Errorf("update media integrity: %w", err)
		}

		if err := requireOne(res); err != nil {
			return err
		}

		if fullHash == "" {
			return nil
		}

		const hash = `UPDATE media_files SET codarr_output_full_hash = ? WHERE id = ?`
		if _, err := tx.ExecContext(ctx, hash, fullHash, id); err != nil {
			return fmt.Errorf("update media full hash: %w", err)
		}

		return nil
	})
}

// RecordPromotion is step 9 of plan.md 15.2. The current size, mtime and
// fingerprint move to the output's own values in the same transaction as the
// codarr_output_* columns, so the next scan sees an unchanged file instead of
// re-probing Codarr's own work and looping.
func (s *store) RecordPromotion(ctx context.Context, u PromotionUpdate) error {
	transform, err := marshalJSON(u.Transform)
	if err != nil {
		return err
	}

	return s.write(ctx, func(tx *sql.Tx) error {
		const job = `
			UPDATE jobs SET
				state = ?, output_size = ?, output_fingerprint = ?, output_full_hash = ?,
				actual_seconds = ?, transform_json = ?, failure_code = NULL,
				failure_message = NULL, finished_at = ?
			WHERE id = ?`

		res, err := tx.ExecContext(ctx, job, string(domain.JobDone), u.OutputSize,
			nullString(u.OutputFingerprint), nullString(u.OutputFullHash),
			nullInt64(int64(u.ActualSeconds)), transform, formatTime(u.PromotedAt), u.JobID)
		if err != nil {
			return fmt.Errorf("update promoted job: %w", err)
		}

		if err := requireOne(res); err != nil {
			return err
		}

		const media = `
			UPDATE media_files SET
				size_bytes = ?, mtime = ?, fingerprint = ?,
				codarr_job_id = ?, codarr_processed_at = ?, codarr_output_fingerprint = ?,
				codarr_output_size = ?, codarr_output_mtime = ?, codarr_output_full_hash = ?,
				codarr_tagged = 1, codarr_policy_hash = ?, provenance = ?, status = ?,
				last_error = NULL, updated_at = ?
			WHERE id = ?`

		res, err = tx.ExecContext(ctx, media, u.OutputSize, u.OutputMTime,
			nullString(u.OutputFingerprint), u.JobID, formatTime(u.PromotedAt),
			nullString(u.OutputFingerprint), u.OutputSize, u.OutputMTime,
			nullString(u.OutputFullHash), nullString(u.PolicyHash),
			string(domain.ProvenanceCodarrOutput), string(domain.MediaDone),
			formatTime(u.PromotedAt), u.MediaFileID)
		if err != nil {
			return fmt.Errorf("update promoted media: %w", err)
		}

		return requireOne(res)
	})
}

func (s *store) CountMediaByStatus(ctx context.Context) (map[domain.MediaStatus]int, error) {
	counts, err := s.countBy(ctx, `SELECT status, COUNT(*) FROM media_files GROUP BY status`)
	if err != nil {
		return nil, err
	}

	out := make(map[domain.MediaStatus]int, len(counts))
	for k, v := range counts {
		out[domain.MediaStatus(k)] = v
	}

	return out, nil
}

func (s *store) CountMediaByPlanKind(ctx context.Context) (map[domain.Kind]int, error) {
	const query = `
		SELECT plan_kind, COUNT(*) FROM media_files
		WHERE plan_kind IS NOT NULL GROUP BY plan_kind`

	counts, err := s.countBy(ctx, query)
	if err != nil {
		return nil, err
	}

	out := make(map[domain.Kind]int, len(counts))
	for k, v := range counts {
		out[domain.Kind(k)] = v
	}

	return out, nil
}

func (s *store) countBy(ctx context.Context, query string) (map[string]int, error) {
	rows, err := s.db.read.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("count rows: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := map[string]int{}

	for rows.Next() {
		var (
			key string
			n   int
		)

		if err := rows.Scan(&key, &n); err != nil {
			return nil, fmt.Errorf("scan count: %w", err)
		}

		out[key] = n
	}

	if err := closeRows(rows); err != nil {
		return nil, err
	}

	return out, nil
}

func (s *store) mediaRow(row *sql.Row) (domain.MediaFile, error) {
	m, err := scanMediaFile(row)
	if notFound(err) {
		return domain.MediaFile{}, ErrNotFound
	}

	if err != nil {
		return domain.MediaFile{}, fmt.Errorf("scan media file: %w", err)
	}

	return m, nil
}

func mediaWhere(f MediaFilter) (string, []any) {
	var c conds

	if f.Query != "" {
		c.add(`path LIKE ? ESCAPE '\'`, "%"+escapeLike(f.Query)+"%")
	}

	c.in("status", strs(f.Status))
	c.in("plan_kind", strs(f.PlanKind))
	c.in("video_codec", f.VideoCodec)
	c.in("provenance", strs(f.Provenance))

	if f.ArrInstanceID != nil {
		c.add("arr_instance_id = ?", *f.ArrInstanceID)
	}

	if !f.IncludeIgnored {
		c.add("ignored = 0")
	}

	return c.where(), c.args
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

	return r.Replace(s)
}

//nolint:funlen,cyclop // 38 columns; the length is the schema's, not a branchy function's
func scanMediaFile(row rowScanner) (domain.MediaFile, error) {
	var (
		m                domain.MediaFile
		rootID           sql.NullInt64
		arrInstanceID    sql.NullInt64
		arrEntityID      sql.NullInt64
		nlink            sql.NullInt64
		fingerprint      sql.NullString
		probeJSON        sql.NullString
		mediaInfoJSON    sql.NullString
		analyzedAt       sql.NullString
		planJSON         sql.NullString
		planKind         sql.NullString
		planReasons      sql.NullString
		container        sql.NullString
		videoCodec       sql.NullString
		videoProfile     sql.NullString
		videoLevel       sql.NullString
		videoBitrate     sql.NullInt64
		videoBitrateSrc  sql.NullString
		fingerprintAlgo  sql.NullString
		policyHash       sql.NullString
		codarrJobID      sql.NullInt64
		codarrProcessed  sql.NullString
		outFingerprint   sql.NullString
		outSize          sql.NullInt64
		outMTime         sql.NullInt64
		outFullHash      sql.NullString
		provenance       string
		integrityChecked sql.NullString
		status           string
		lastError        sql.NullString
		createdAt        sql.NullString
		updatedAt        sql.NullString
	)

	err := row.Scan(&m.ID, &m.Path, &rootID, &arrInstanceID, &arrEntityID, &m.SizeBytes,
		&m.MTime, &nlink, &fingerprint, &probeJSON, &mediaInfoJSON, &analyzedAt, &planJSON,
		&planKind, &planReasons, &container, &videoCodec, &videoProfile, &videoLevel,
		&videoBitrate, &videoBitrateSrc, &m.IsHDR, &fingerprintAlgo, &m.CodarrTagged,
		&policyHash, &codarrJobID, &codarrProcessed, &outFingerprint, &outSize, &outMTime,
		&outFullHash, &provenance, &integrityChecked, &status, &m.Ignored, &lastError,
		&createdAt, &updatedAt)
	if err != nil {
		return domain.MediaFile{}, err //nolint:wrapcheck // sql.ErrNoRows must stay comparable for the caller
	}

	m.RootID = int64Ptr(rootID)
	m.ArrInstanceID = int64Ptr(arrInstanceID)
	m.ArrEntityID = int64Ptr(arrEntityID)
	m.NLink = int(nlink.Int64)
	m.Fingerprint = fingerprint.String
	m.FingerprintAlgo = fingerprintAlgo.String
	m.ProbeJSON = probeJSON.String
	m.MediaInfoJSON = mediaInfoJSON.String
	m.PlanKind = domain.Kind(planKind.String)
	m.Container = container.String
	m.VideoCodec = videoCodec.String
	m.VideoProfile = videoProfile.String
	m.VideoLevel = videoLevel.String
	m.VideoBitrate = int(videoBitrate.Int64)
	m.VideoBitrateSrc = domain.BitrateSource(videoBitrateSrc.String)
	m.CodarrPolicyHash = policyHash.String
	m.CodarrJobID = int64Ptr(codarrJobID)
	m.CodarrOutputFingerprint = outFingerprint.String
	m.CodarrOutputSize = outSize.Int64
	m.CodarrOutputMTime = outMTime.Int64
	m.CodarrOutputFullHash = outFullHash.String
	m.Provenance = domain.Provenance(provenance)
	m.Status = domain.MediaStatus(status)
	m.LastError = lastError.String

	if planJSON.Valid && planJSON.String != "" {
		var p domain.Plan
		if err := unmarshalJSON(planJSON, &p); err != nil {
			return domain.MediaFile{}, err
		}

		m.Plan = &p
	}

	if err := unmarshalJSON(planReasons, &m.PlanReasons); err != nil {
		return domain.MediaFile{}, err
	}

	if m.AnalyzedAt, err = scanTimePtr(analyzedAt); err != nil {
		return domain.MediaFile{}, err
	}

	if m.CodarrProcessedAt, err = scanTimePtr(codarrProcessed); err != nil {
		return domain.MediaFile{}, err
	}

	if m.IntegrityCheckedAt, err = scanTimePtr(integrityChecked); err != nil {
		return domain.MediaFile{}, err
	}

	if m.CreatedAt, err = scanTime(createdAt); err != nil {
		return domain.MediaFile{}, err
	}

	if m.UpdatedAt, err = scanTime(updatedAt); err != nil {
		return domain.MediaFile{}, err
	}

	return m, nil
}
