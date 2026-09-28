package repository

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/rating"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RatingRepository struct {
	db               *pgxpool.Pool
	artifactReader   func(context.Context, string) ([]byte, error)
	artifactDigester func(context.Context, string) (string, error)
}

func NewRatingRepository(db *pgxpool.Pool) *RatingRepository { return &RatingRepository{db: db} }
func (r *RatingRepository) SetArtifactReader(read func(context.Context, string) ([]byte, error)) {
	r.artifactReader = read
}

// SetArtifactDigester avoids loading non-sample payloads on every snapshot.
// Production digests must revalidate the object's version on each call.
func (r *RatingRepository) SetArtifactDigester(digest func(context.Context, string) (string, error)) {
	r.artifactDigester = digest
}

type ratingDB interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func ratingHash(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func ratingJSON(v any) []byte       { data, _ := json.Marshal(v); return data }
func ratingError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return rating.ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && (pg.Code == "23505" || pg.Code == "40001") {
		return rating.ErrConflict
	}
	return err
}
func ratingInvalid(message string) error { return fmt.Errorf("%w: %s", rating.ErrInvalid, message) }
func (r *RatingRepository) lock(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", id.String())
	return err
}
func (r *RatingRepository) capture(ctx context.Context, db ratingDB, id uuid.UUID, locked bool) (rating.Subject, error) {
	s := rating.Subject{ProblemID: id, Tests: []rating.TestArtifact{}, CapturedAt: time.Now().UTC()}
	q := "SELECT title,statement,time_limit,memory_limit,difficulty,tags,COALESCE(detailed_solution,''),COALESCE(metadata_json,'{}'::jsonb) FROM problems WHERE id=$1"
	if locked {
		q += " FOR SHARE"
	}
	if err := db.QueryRow(ctx, q, id).Scan(&s.Title, &s.Statement, &s.TimeLimit, &s.MemoryLimit, &s.TargetDifficulty, &s.ExpectedTags, &s.OfficialSolution, &s.Metadata); err != nil {
		return s, ratingError(err)
	}
	rows, err := db.Query(ctx, "SELECT id::text,input_path,output_path,COALESCE(is_sample,false) FROM testcases WHERE problem_id=$1 ORDER BY test_index,id", id)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var t rating.TestArtifact
		if err = rows.Scan(&t.ID, &t.InputPath, &t.OutputPath, &t.IsSample); err != nil {
			rows.Close()
			return s, err
		}
		s.Tests = append(s.Tests, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return s, err
	}
	var checkerSource string
	err = db.QueryRow(ctx, "SELECT COALESCE(string_agg(language||':'||source_code,E'\\n' ORDER BY id),'') FROM solutions WHERE problem_id=$1 AND solution_type='checker'", id).Scan(&checkerSource)
	if err != nil {
		return s, err
	}
	s.JudgeMode = "exact_normalized"
	if checkerSource != "" {
		s.JudgeMode = "unsupported_checker"
	}
	for i := range s.Tests {
		t := &s.Tests[i]
		if !t.IsSample && r.artifactDigester != nil {
			input, e := r.artifactDigester(ctx, t.InputPath)
			if e != nil {
				return s, fmt.Errorf("digest rating test input: %w", e)
			}
			output, e := r.artifactDigester(ctx, t.OutputPath)
			if e != nil {
				return s, fmt.Errorf("digest rating test output: %w", e)
			}
			inputBytes, e := hex.DecodeString(input)
			if e != nil || len(inputBytes) != sha256.Size {
				return s, fmt.Errorf("invalid rating input digest")
			}
			outputBytes, e := hex.DecodeString(output)
			if e != nil || len(outputBytes) != sha256.Size {
				return s, fmt.Errorf("invalid rating output digest")
			}
			t.InputSHA256 = hex.EncodeToString(inputBytes)
			t.OutputSHA256 = hex.EncodeToString(outputBytes)
			continue
		}
		if r.artifactReader == nil {
			return s, fmt.Errorf("rating test content reader is not configured")
		}
		input, e := r.artifactReader(ctx, t.InputPath)
		if e != nil {
			return s, fmt.Errorf("read rating test input: %w", e)
		}
		output, e := r.artifactReader(ctx, t.OutputPath)
		if e != nil {
			return s, fmt.Errorf("read rating test output: %w", e)
		}
		t.InputSHA256 = ratingHash(input)
		t.OutputSHA256 = ratingHash(output)
		if t.IsSample {
			if len(input) > 1<<20 || len(output) > 1<<20 {
				return s, ratingInvalid("sample content exceeds 1 MiB")
			}
			t.Input = string(input)
			t.Output = string(output)
		}
	}
	// Exclude target labels/editorial/status/metadata: none are part of the task
	// seen by a contestant. Constraints embedded in statement and test content are.
	var metadata map[string]json.RawMessage
	if err = json.Unmarshal(s.Metadata, &metadata); err != nil {
		return s, err
	}
	constraints := map[string]json.RawMessage{}
	for _, key := range []string{"constraints", "semantic_spec", "input_format", "output_format", "subtasks", "testdata_config", "test_data_config"} {
		if v, ok := metadata[key]; ok {
			constraints[key] = v
		}
	}
	identity := struct {
		Title, Statement, JudgeMode, Checker string
		TimeLimit, MemoryLimit               int
		Tests                                []rating.TestArtifact
		Constraints                          map[string]json.RawMessage
	}{s.Title, s.Statement, s.JudgeMode, checkerSource, s.TimeLimit, s.MemoryLimit, s.Tests, constraints}
	s.SourceReference = rating.SourceFromMetadata(s.Metadata, s.Statement)
	s.Hash = ratingHash(ratingJSON(identity))
	_, err = db.Exec(ctx, "INSERT INTO rating_subjects(problem_id,subject_hash,payload) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", id, s.Hash, ratingJSON(s))
	return s, err
}
func (r *RatingRepository) CaptureSubject(ctx context.Context, id uuid.UUID) (rating.Subject, error) {
	return r.capture(ctx, r.db, id, false)
}

func (r *RatingRepository) CreateAssessment(ctx context.Context, id uuid.UUID, actor string) (rating.Assessment, error) {
	var a rating.Assessment
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return a, err
	}
	defer tx.Rollback(ctx)
	if err = r.lock(ctx, tx, id); err != nil {
		return a, err
	}
	s, err := r.capture(ctx, tx, id, true)
	if err != nil {
		return a, err
	}
	a = rating.Assessment{ID: uuid.New(), ProblemID: id, Subject: s, Status: "pending", Phase: "queued", RuleVersion: rating.RuleVersion, CreatedBy: actor, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	a.WorkflowID = "rating-" + a.ID.String()
	_, err = tx.Exec(ctx, "INSERT INTO rating_assessments(id,problem_id,subject_hash,status,payload) VALUES($1,$2,$3,$4,$5)", a.ID, id, s.Hash, a.Status, ratingJSON(a))
	if err != nil {
		return a, ratingError(err)
	}
	return a, tx.Commit(ctx)
}
func (r *RatingRepository) GetAssessment(ctx context.Context, id uuid.UUID) (rating.Assessment, error) {
	var a rating.Assessment
	var raw []byte
	err := r.db.QueryRow(ctx, "SELECT payload FROM rating_assessments WHERE id=$1", id).Scan(&raw)
	if err != nil {
		return a, ratingError(err)
	}
	err = json.Unmarshal(raw, &a)
	return a, err
}
func (r *RatingRepository) UpdateAssessment(ctx context.Context, id uuid.UUID, status, phase string, report *rating.Report, errorText string) error {
	switch status {
	case "running", "completed", "failed", "cancelled":
	default:
		return ratingInvalid("invalid assessment status")
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var raw []byte
	if err = tx.QueryRow(ctx, "SELECT payload FROM rating_assessments WHERE id=$1 FOR UPDATE", id).Scan(&raw); err != nil {
		return ratingError(err)
	}
	var a rating.Assessment
	if err = json.Unmarshal(raw, &a); err != nil {
		return err
	}
	if a.Status == "completed" || a.Status == "failed" || a.Status == "cancelled" {
		if a.Status == status {
			return nil
		}
		return rating.ErrConflict
	}
	if status == "completed" && (report == nil || report.SnapshotHash != a.Subject.Hash) {
		return ratingInvalid("completed assessment requires matching report snapshot")
	}
	a.Status = status
	a.Phase = phase
	a.Report = report
	a.Error = errorText
	a.UpdatedAt = time.Now().UTC()
	_, err = tx.Exec(ctx, "UPDATE rating_assessments SET status=$2,payload=$3,updated_at=now() WHERE id=$1", id, status, ratingJSON(a))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func ratingRows[T any](ctx context.Context, db ratingDB, q string, args ...any) ([]T, error) {
	result := []T{}
	rows, err := db.Query(ctx, q, args...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var item T
		if err = rows.Scan(&raw); err != nil {
			return result, err
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return result, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
func (r *RatingRepository) ListAnchors(ctx context.Context) ([]rating.Anchor, error) {
	return ratingRows[rating.Anchor](ctx, r.db, "SELECT payload FROM rating_anchors ORDER BY created_at,id")
}
func (r *RatingRepository) CreateAnchor(ctx context.Context, a rating.Anchor, actor string) (rating.Anchor, error) {
	if !a.SourceConfirmed || a.Title == "" || a.StatementSummary == "" || a.SolutionSummary == "" || a.RatingSource == "" || a.Population == "" || a.Family == "" || a.RetrievedAt.IsZero() || !validRating(a.Rating) || actor == "" {
		return a, ratingInvalid("reviewed anchor needs confirmed source, source date, rating, population, family, statement and solution summaries")
	}
	if !ratingHTTPURL(a.SourceURL) {
		return a, ratingInvalid("anchor source must be an http(s) URL")
	}
	if a.RetrievedAt.After(time.Now().Add(24 * time.Hour)) {
		return a, ratingInvalid("anchor retrieval date is in the future")
	}
	if len(ratingJSON(a)) > 128<<10 {
		return a, ratingInvalid("anchor is too large")
	}
	a.Basis = "manual"
	a.SourceReference = nil
	a.DecisionID = uuid.Nil
	a.SubjectHash = ""
	a.ID = uuid.New()
	a.ReviewedBy = actor
	a.ReviewedAt = time.Now().UTC()
	_, err := r.db.Exec(ctx, "INSERT INTO rating_anchors(id,payload,reviewed_by) VALUES($1,$2,$3)", a.ID, ratingJSON(a), actor)
	return a, err
}
func validRating(v int) bool { return v >= 800 && v <= 3500 && v%100 == 0 }
func ratingHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil
}

func (r *RatingRepository) Workspace(ctx context.Context, id uuid.UUID) (rating.Workspace, error) {
	w := rating.Workspace{}
	var err error
	w.Subject, err = r.CaptureSubject(ctx, id)
	if err != nil {
		return w, err
	}
	w.Assessments, err = ratingRows[rating.Assessment](ctx, r.db, "SELECT payload FROM rating_assessments WHERE problem_id=$1 ORDER BY created_at DESC,id DESC LIMIT 100", id)
	if err != nil {
		return w, err
	}
	for i := range w.Assessments {
		w.Assessments[i].Stale = w.Assessments[i].Subject.Hash != w.Subject.Hash
	}
	var o rating.OfficialRating
	err = r.db.QueryRow(ctx, "SELECT rating,subject_hash,decision_id,updated_at FROM rating_official WHERE problem_id=$1", id).Scan(&o.Rating, &o.SubjectHash, &o.DecisionID, &o.UpdatedAt)
	if err == nil {
		o.Stale = o.SubjectHash != w.Subject.Hash
		w.Official = &o
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return w, err
	}
	w.Feedback, err = r.ListFeedback(ctx, id, w.Subject.Hash)
	if err != nil {
		return w, err
	}
	w.FeedbackHash = rating.FeedbackSnapshotHash(w.Feedback)
	w.Human = rating.SummarizeFeedback(w.Feedback)
	w.Calibrations, err = ratingRows[rating.Calibration](ctx, r.db, "SELECT payload FROM rating_calibrations WHERE problem_id=$1 ORDER BY created_at DESC,id DESC LIMIT 100", id)
	if err != nil {
		return w, err
	}
	w.Decisions, err = ratingRows[rating.Decision](ctx, r.db, "SELECT payload FROM rating_decisions WHERE problem_id=$1 ORDER BY created_at DESC,id DESC LIMIT 100", id)
	return w, err
}
func (r *RatingRepository) IssueInvitation(ctx context.Context, id uuid.UUID, q rating.InvitationRequest, actor string) (rating.IssuedInvitation, error) {
	var result rating.IssuedInvitation
	q.ReviewerKey = strings.TrimSpace(q.ReviewerKey)
	if q.ReviewerKey == "" || len(q.ReviewerKey) > 200 || q.WindowMinutes < 1 || q.WindowMinutes > 1440 || q.ExpiresInDays < 1 || q.ExpiresInDays > 90 || (q.Context != "practice" && q.Context != "contest") {
		return result, ratingInvalid("reviewer_key, window 1–1440, practice/contest context and expiry 1–90 days required")
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	if err = r.lock(ctx, tx, id); err != nil {
		return result, err
	}
	s, err := r.capture(ctx, tx, id, true)
	if err != nil {
		return result, err
	}
	reviewer := uuid.New()
	err = tx.QueryRow(ctx, "INSERT INTO rating_reviewers(id,reviewer_key) VALUES($1,$2) ON CONFLICT(reviewer_key) DO UPDATE SET reviewer_key=EXCLUDED.reviewer_key RETURNING id", reviewer, q.ReviewerKey).Scan(&reviewer)
	if err != nil {
		return result, err
	}
	// One identity has one fixed condition per version, even across token reissues.
	var oldWindow int
	var oldContext string
	err = tx.QueryRow(ctx, "SELECT window_minutes,context FROM rating_invitations WHERE problem_id=$1 AND subject_hash=$2 AND reviewer_id=$3 ORDER BY created_at LIMIT 1", id, s.Hash, reviewer).Scan(&oldWindow, &oldContext)
	if err == nil && (oldWindow != q.WindowMinutes || oldContext != q.Context) {
		return result, ratingInvalid("reviewer already has a different observation window or context for this version")
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return result, err
	}
	result.Token = base64.RawURLEncoding.EncodeToString(secret)
	now := time.Now().UTC()
	result.Invitation = rating.Invitation{ID: uuid.New(), ProblemID: id, SubjectHash: s.Hash, ReviewerID: reviewer, ReviewerKey: q.ReviewerKey, WindowMinutes: q.WindowMinutes, Context: q.Context, ExpiresAt: now.Add(time.Duration(q.ExpiresInDays) * 24 * time.Hour), CreatedAt: now}
	i := result.Invitation
	_, err = tx.Exec(ctx, "INSERT INTO rating_invitations(id,problem_id,subject_hash,reviewer_id,token_hash,window_minutes,context,expires_at,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)", i.ID, id, s.Hash, reviewer, ratingHash([]byte(result.Token)), i.WindowMinutes, i.Context, i.ExpiresAt, actor)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
func (r *RatingRepository) ListInvitations(ctx context.Context, id uuid.UUID) ([]rating.Invitation, error) {
	rows, err := r.db.Query(ctx, "SELECT i.id,i.problem_id,i.subject_hash,i.reviewer_id,r.reviewer_key,i.window_minutes,i.context,i.expires_at,i.revoked_at,i.created_at FROM rating_invitations i JOIN rating_reviewers r ON r.id=i.reviewer_id WHERE problem_id=$1 ORDER BY i.created_at DESC", id)
	result := []rating.Invitation{}
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var i rating.Invitation
		if err = rows.Scan(&i.ID, &i.ProblemID, &i.SubjectHash, &i.ReviewerID, &i.ReviewerKey, &i.WindowMinutes, &i.Context, &i.ExpiresAt, &i.RevokedAt, &i.CreatedAt); err != nil {
			return result, err
		}
		result = append(result, i)
	}
	return result, rows.Err()
}
func (r *RatingRepository) RevokeInvitation(ctx context.Context, problem, id uuid.UUID) error {
	tag, err := r.db.Exec(ctx, "UPDATE rating_invitations SET revoked_at=COALESCE(revoked_at,now()) WHERE id=$1 AND problem_id=$2", id, problem)
	if err == nil && tag.RowsAffected() == 0 {
		return rating.ErrNotFound
	}
	return err
}
func ratingInvitation(ctx context.Context, db ratingDB, token string, locked bool) (rating.Invitation, error) {
	var i rating.Invitation
	if len(token) != 43 {
		return i, rating.ErrUnauthorized
	}
	q := "SELECT id,problem_id,subject_hash,reviewer_id,window_minutes,context,expires_at,revoked_at FROM rating_invitations WHERE token_hash=$1"
	if locked {
		q += " FOR UPDATE"
	}
	err := db.QueryRow(ctx, q, ratingHash([]byte(token))).Scan(&i.ID, &i.ProblemID, &i.SubjectHash, &i.ReviewerID, &i.WindowMinutes, &i.Context, &i.ExpiresAt, &i.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return i, rating.ErrUnauthorized
	}
	if err != nil {
		return i, err
	}
	if i.RevokedAt != nil || !i.ExpiresAt.After(time.Now()) {
		return i, rating.ErrUnauthorized
	}
	return i, nil
}
func (r *RatingRepository) ReviewTask(ctx context.Context, token string) (rating.ReviewTask, error) {
	var task rating.ReviewTask
	i, err := ratingInvitation(ctx, r.db, token, false)
	if err != nil {
		return task, err
	}
	var raw []byte
	var s rating.Subject
	err = r.db.QueryRow(ctx, "SELECT payload FROM rating_subjects WHERE problem_id=$1 AND subject_hash=$2", i.ProblemID, i.SubjectHash).Scan(&raw)
	if err != nil {
		return task, ratingError(err)
	}
	if err = json.Unmarshal(raw, &s); err != nil {
		return task, err
	}
	task = rating.ReviewTask{Title: s.Title, Statement: s.Statement, TimeLimit: s.TimeLimit, MemoryLimit: s.MemoryLimit, Samples: []rating.ReviewSample{}, SubjectHash: s.Hash, WindowMinutes: i.WindowMinutes, Context: i.Context, ExpiresAt: i.ExpiresAt}
	for _, t := range s.Tests {
		if t.IsSample {
			task.Samples = append(task.Samples, rating.ReviewSample{Input: t.Input, Output: t.Output})
		}
	}
	err = r.db.QueryRow(ctx, "SELECT payload FROM rating_feedback WHERE problem_id=$1 AND subject_hash=$2 AND reviewer_id=$3", i.ProblemID, i.SubjectHash, i.ReviewerID).Scan(&raw)
	if err == nil {
		var f rating.Feedback
		if err = json.Unmarshal(raw, &f); err != nil {
			return task, err
		}
		task.Feedback = &f.FeedbackInput
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return task, err
	}
	return task, nil
}
func validateRatingFeedback(f rating.FeedbackInput, window int) error {
	switch f.Outcome {
	case "solved", "unsolved", "in_progress", "not_attempted", "stopped":
	default:
		return ratingInvalid("invalid outcome")
	}
	if f.IndependentMinutes < 0 || f.ElapsedMinutes < 0 || f.ElapsedMinutes > 10080 || f.IndependentMinutes > f.ElapsedMinutes {
		return ratingInvalid("invalid effective/independent minutes")
	}
	if f.ObservedFullWindow && f.ElapsedMinutes < window {
		return ratingInvalid("full observation requires elapsed time at least the assigned window")
	}
	if f.Outcome == "not_attempted" && (f.ElapsedMinutes > 0 || f.IndependentMinutes > 0 || f.ObservedFullWindow) {
		return ratingInvalid("not_attempted conflicts with attempt time")
	}
	if f.AssistanceAfterMinutes != nil && (*f.AssistanceAfterMinutes < 0 || *f.AssistanceAfterMinutes > f.ElapsedMinutes) {
		return ratingInvalid("invalid assistance timing")
	}
	seen := map[string]bool{}
	for _, a := range f.Assistance {
		switch a {
		case "hint", "editorial", "tags", "ai", "discussion":
		default:
			return ratingInvalid("invalid assistance type")
		}
		if seen[a] {
			return ratingInvalid("duplicate assistance type")
		}
		seen[a] = true
	}
	if len(f.Assistance) == 0 && f.AssistanceAfterMinutes != nil {
		return ratingInvalid("assistance timing requires an assistance type")
	}
	if len(f.Assistance) > 0 && f.AssistanceAfterMinutes != nil && f.IndependentMinutes > *f.AssistanceAfterMinutes {
		return ratingInvalid("independent time cannot continue past first assistance")
	}
	if f.SubjectiveRating != nil && !validRating(*f.SubjectiveRating) {
		return ratingInvalid("subjective rating must be 800–3500 in steps of 100")
	}
	if f.CFRating != nil && (*f.CFRating < 0 || *f.CFRating > 5000 || f.CFRatingAt == nil) {
		return ratingInvalid("self-reported CF ability needs a dated value")
	}
	if f.CFRatingAt != nil && (f.CFRating == nil || f.CFRatingAt.After(time.Now().Add(24*time.Hour))) {
		return ratingInvalid("invalid CF ability date")
	}
	if f.ResultSource != "self_report" && f.ResultSource != "external_link" {
		return ratingInvalid("result source is self_report or external_link")
	}
	if f.ResultSource == "external_link" && !ratingHTTPURL(f.ResultURL) {
		return ratingInvalid("external result needs an http(s) link")
	}
	if len(f.FirstRoute) > 8000 || len(f.FinalRoute) > 8000 || len(f.Blockers) > 8000 || len(f.Notes) > 8000 || len(f.Code) > 128<<10 {
		return ratingInvalid("feedback text is too large")
	}
	return nil
}
func (r *RatingRepository) SubmitFeedback(ctx context.Context, token string, input rating.FeedbackInput) (rating.Feedback, error) {
	var f rating.Feedback
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return f, err
	}
	defer tx.Rollback(ctx)
	// Discover scope without locking, then use consistent problem -> invitation order.
	inv, err := ratingInvitation(ctx, tx, token, false)
	if err != nil {
		return f, err
	}
	if err = r.lock(ctx, tx, inv.ProblemID); err != nil {
		return f, err
	}
	inv, err = ratingInvitation(ctx, tx, token, true)
	if err != nil {
		return f, err
	}
	if err = validateRatingFeedback(input, inv.WindowMinutes); err != nil {
		return f, err
	}
	f = rating.Feedback{ID: uuid.New(), ProblemID: inv.ProblemID, SubjectHash: inv.SubjectHash, ReviewerID: inv.ReviewerID, Revision: 1, WindowMinutes: inv.WindowMinutes, Context: inv.Context, FeedbackInput: input, UpdatedAt: time.Now().UTC()}
	var oldID uuid.UUID
	var oldPayload []byte
	var rev int
	err = tx.QueryRow(ctx, "SELECT id,revision,payload FROM rating_feedback WHERE problem_id=$1 AND subject_hash=$2 AND reviewer_id=$3 FOR UPDATE", inv.ProblemID, inv.SubjectHash, inv.ReviewerID).Scan(&oldID, &rev, &oldPayload)
	if err == nil {
		var previous rating.Feedback
		if err = json.Unmarshal(oldPayload, &previous); err != nil {
			return f, err
		}
		if bytes.Equal(ratingJSON(previous.FeedbackInput), ratingJSON(input)) {
			return previous, nil
		}
		f.ID = oldID
		f.Revision = rev + 1
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return f, err
	}
	raw := ratingJSON(f)
	_, err = tx.Exec(ctx, "INSERT INTO rating_feedback(id,problem_id,subject_hash,reviewer_id,revision,payload) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(problem_id,subject_hash,reviewer_id) DO UPDATE SET revision=EXCLUDED.revision,payload=EXCLUDED.payload,updated_at=now()", f.ID, f.ProblemID, f.SubjectHash, f.ReviewerID, f.Revision, raw)
	if err != nil {
		return f, err
	}
	_, err = tx.Exec(ctx, "INSERT INTO rating_feedback_revisions(feedback_id,revision,payload) VALUES($1,$2,$3)", f.ID, f.Revision, raw)
	if err != nil {
		return f, err
	}
	return f, tx.Commit(ctx)
}
func (r *RatingRepository) ListFeedback(ctx context.Context, id uuid.UUID, hash string) ([]rating.Feedback, error) {
	return ratingRows[rating.Feedback](ctx, r.db, "SELECT payload FROM rating_feedback WHERE problem_id=$1 AND subject_hash=$2 ORDER BY reviewer_id", id, hash)
}
func (r *RatingRepository) SaveCalibration(ctx context.Context, c rating.Calibration) (rating.Calibration, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return c, err
	}
	defer tx.Rollback(ctx)
	if err = r.lock(ctx, tx, c.ProblemID); err != nil {
		return c, err
	}
	s, err := r.capture(ctx, tx, c.ProblemID, true)
	if err != nil {
		return c, err
	}
	fs, err := ratingRows[rating.Feedback](ctx, tx, "SELECT payload FROM rating_feedback WHERE problem_id=$1 AND subject_hash=$2 ORDER BY reviewer_id", c.ProblemID, s.Hash)
	if err != nil {
		return c, err
	}
	if c.SubjectHash != s.Hash || c.FeedbackHash != rating.FeedbackSnapshotHash(fs) || c.RuleVersion != rating.RuleVersion {
		return c, rating.ErrConflict
	}
	// Recompute on the server; callers cannot inject a score or inflated sample count.
	c = rating.BuildCalibration(c.ProblemID, s.Hash, fs)
	c.ID = uuid.New()
	c.CreatedAt = time.Now().UTC()
	var raw []byte
	err = tx.QueryRow(ctx, "INSERT INTO rating_calibrations(id,problem_id,subject_hash,feedback_hash,rule_version,payload) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(problem_id,subject_hash,feedback_hash,rule_version) DO UPDATE SET feedback_hash=EXCLUDED.feedback_hash RETURNING payload", c.ID, c.ProblemID, c.SubjectHash, c.FeedbackHash, c.RuleVersion, ratingJSON(c)).Scan(&raw)
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	return c, tx.Commit(ctx)
}
func (r *RatingRepository) Decide(ctx context.Context, id uuid.UUID, input rating.DecisionInput, actor string) (rating.Decision, error) {
	var d rating.Decision
	if actor == "" || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 8000 {
		return d, ratingInvalid("administrator identity and reason are required")
	}
	switch input.Action {
	case "accept", "modify", "reject", "defer":
	default:
		return d, ratingInvalid("invalid decision action")
	}
	if (input.AssessmentID == nil) == (input.CalibrationID == nil) {
		return d, ratingInvalid("select exactly one assessment or calibration")
	}
	if input.Rating != nil && !validRating(*input.Rating) {
		return d, ratingInvalid("rating must be 800–3500 in steps of 100")
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return d, err
	}
	defer tx.Rollback(ctx)
	if err = r.lock(ctx, tx, id); err != nil {
		return d, err
	}
	s, err := r.capture(ctx, tx, id, true)
	if err != nil {
		return d, err
	}
	fs, err := ratingRows[rating.Feedback](ctx, tx, "SELECT payload FROM rating_feedback WHERE problem_id=$1 AND subject_hash=$2 ORDER BY reviewer_id", id, s.Hash)
	if err != nil {
		return d, err
	}
	if s.Hash != input.SubjectHash || input.FeedbackHash != rating.FeedbackSnapshotHash(fs) {
		return d, rating.ErrConflict
	}
	var proposed *int
	var raw []byte
	if input.AssessmentID != nil {
		var a rating.Assessment
		err = tx.QueryRow(ctx, "SELECT payload FROM rating_assessments WHERE id=$1 AND problem_id=$2", *input.AssessmentID, id).Scan(&raw)
		if err != nil {
			return d, ratingError(err)
		}
		if err = json.Unmarshal(raw, &a); err != nil {
			return d, err
		}
		if a.Subject.Hash != s.Hash || a.Status != "completed" || a.Report == nil {
			return d, rating.ErrConflict
		}
		if input.Action == "accept" || input.Action == "modify" {
			if a.Report.Validity == "invalid" || a.Report.Validity == "blocked" {
				return d, ratingInvalid("invalid problem requires repair before a formal rating")
			}
		}
		proposed = a.Report.Estimate.Representative
	} else {
		var c rating.Calibration
		err = tx.QueryRow(ctx, "SELECT payload FROM rating_calibrations WHERE id=$1 AND problem_id=$2", *input.CalibrationID, id).Scan(&raw)
		if err != nil {
			return d, ratingError(err)
		}
		if err = json.Unmarshal(raw, &c); err != nil {
			return d, err
		}
		if c.SubjectHash != s.Hash || c.FeedbackHash != input.FeedbackHash {
			return d, rating.ErrConflict
		}
		proposed = c.SuggestedRating
	}
	switch input.Action {
	case "accept":
		if input.AssessmentID != nil {
			var assessment rating.Assessment
			if err = json.Unmarshal(raw, &assessment); err != nil {
				return d, err
			}
			if len(assessment.Report.Disagreements) > 0 || assessment.Report.Validity != "tested_candidates" {
				return d, ratingInvalid("unresolved assessment needs explicit manual review")
			}
		}
		if proposed == nil {
			return d, ratingInvalid("no suggested rating; use explicit manual modification with a reason")
		}
		if input.Rating != nil && *input.Rating != *proposed {
			return d, ratingInvalid("accept must match the evidence proposal")
		}
		input.Rating = proposed
	case "modify":
		if input.Rating == nil {
			return d, ratingInvalid("modified rating is required")
		}
	default:
		input.Rating = nil
	}
	d = rating.Decision{ID: uuid.New(), ProblemID: id, DecisionInput: input, Actor: actor, CreatedAt: time.Now().UTC()}
	var previous int
	var previousDecision uuid.UUID
	err = tx.QueryRow(ctx, "SELECT rating,decision_id FROM rating_official WHERE problem_id=$1", id).Scan(&previous, &previousDecision)
	if err == nil {
		if input.ExpectedDecisionID == nil || *input.ExpectedDecisionID != previousDecision {
			return d, rating.ErrConflict
		}
		d.PreviousRating = &previous
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return d, err
	}
	if d.PreviousRating == nil && input.ExpectedDecisionID != nil {
		return d, rating.ErrConflict
	}
	_, err = tx.Exec(ctx, "INSERT INTO rating_decisions(id,problem_id,subject_hash,payload) VALUES($1,$2,$3,$4)", d.ID, id, s.Hash, ratingJSON(d))
	if err != nil {
		return d, err
	}
	if input.Rating != nil {
		_, err = tx.Exec(ctx, "INSERT INTO rating_official(problem_id,subject_hash,rating,decision_id) VALUES($1,$2,$3,$4) ON CONFLICT(problem_id) DO UPDATE SET subject_hash=EXCLUDED.subject_hash,rating=EXCLUDED.rating,decision_id=EXCLUDED.decision_id,updated_at=now()", id, s.Hash, *input.Rating, d.ID)
		if err != nil {
			return d, err
		}
	}
	return d, tx.Commit(ctx)
}

var _ rating.Store = (*RatingRepository)(nil)

// CurrentOfficial returns only a rating bound to the current task and actual
// test bytes. A nil value means absent or stale, never a fallback target score.
func (r *RatingRepository) CurrentOfficial(ctx context.Context, id uuid.UUID) (*rating.OfficialRating, error) {
	var o rating.OfficialRating
	err := r.db.QueryRow(ctx, "SELECT rating,subject_hash,decision_id,updated_at FROM rating_official WHERE problem_id=$1", id).Scan(&o.Rating, &o.SubjectHash, &o.DecisionID, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s, err := r.CaptureSubject(ctx, id)
	if err != nil {
		return nil, err
	}
	if s.Hash != o.SubjectHash {
		return nil, nil
	}
	return &o, nil
}

// VerifyOfficialTx binds an assembly confirmation to exactly the reviewed
// decision and subject. Call this in the same transaction as the set insert.
// Lock problem ids in sorted order for multi-item assemblies.
func (r *RatingRepository) VerifyOfficialTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, subjectHash string, decisionID uuid.UUID) (*rating.OfficialRating, error) {
	if err := r.lock(ctx, tx, id); err != nil {
		return nil, err
	}
	s, err := r.capture(ctx, tx, id, true)
	if err != nil {
		return nil, err
	}
	var o rating.OfficialRating
	err = tx.QueryRow(ctx, "SELECT rating,subject_hash,decision_id,updated_at FROM rating_official WHERE problem_id=$1 FOR SHARE", id).Scan(&o.Rating, &o.SubjectHash, &o.DecisionID, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rating.ErrConflict
	}
	if err != nil {
		return nil, err
	}
	if s.Hash != subjectHash || o.SubjectHash != subjectHash || o.DecisionID != decisionID {
		return nil, rating.ErrConflict
	}
	return &o, nil
}
