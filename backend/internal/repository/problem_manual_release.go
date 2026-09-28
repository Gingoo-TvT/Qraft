package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ManualReleaseOptions struct {
	ExpectedUpdatedAt time.Time
	Note              string
}

type ManualReleaseApproval struct {
	ApprovalID       uuid.UUID `json:"approval_id"`
	ApprovedBy       string    `json:"approved_by"`
	ApprovedAt       time.Time `json:"approved_at"`
	Note             string    `json:"note,omitempty"`
	OverriddenChecks []string  `json:"overridden_checks"`
}

// ApprovePublicReleaseWithQualityOverride is the explicit human-only path.
// Automatic publication calls ApprovePublicRelease and cannot opt into it.
func (r *ProblemRepository) ApprovePublicReleaseWithQualityOverride(ctx context.Context, id uuid.UUID, actor string, options ManualReleaseOptions) (PublicReleaseApprovalReport, error) {
	if options.ExpectedUpdatedAt.IsZero() || len([]rune(options.Note)) > 2000 {
		return PublicReleaseApprovalReport{}, fmt.Errorf("validation: a reviewed revision and a note of at most 2000 characters are required")
	}
	return r.approvePublicRelease(ctx, id, actor, &options)
}

func recordManualRelease(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor string, options ManualReleaseOptions, reasons []string) (*ManualReleaseApproval, error) {
	if reasons == nil {
		reasons = []string{}
	}
	raw, err := json.Marshal(reasons)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO problem_manual_release_approvals
        (approval_id, problem_id, subject_sha256, approved_by, note, overridden_checks)
        SELECT $1, p.id, problem_manual_release_subject(p), $3, $4, $5::jsonb
        FROM problems p WHERE p.id=$2
        ON CONFLICT (problem_id, subject_sha256) DO NOTHING`,
		uuid.New(), id, actor, strings.TrimSpace(options.Note), string(raw))
	if err != nil {
		return nil, fmt.Errorf("recording manual release approval: %w", err)
	}
	approval := &ManualReleaseApproval{}
	err = tx.QueryRow(ctx, `SELECT a.approval_id,a.approved_by,a.approved_at,a.note,a.overridden_checks
        FROM problem_manual_release_approvals a JOIN problems p ON p.id=a.problem_id
        WHERE p.id=$1 AND a.subject_sha256=problem_manual_release_subject(p)`, id).Scan(
		&approval.ApprovalID, &approval.ApprovedBy, &approval.ApprovedAt, &approval.Note, &raw)
	if err != nil {
		return nil, fmt.Errorf("reading manual release approval: %w", err)
	}
	if err = json.Unmarshal(raw, &approval.OverriddenChecks); err != nil {
		return nil, err
	}
	summary, err := json.Marshal(approval)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE problems SET metadata_json=COALESCE(metadata_json,'{}'::jsonb)
        || jsonb_build_object('manual_release_approval',$2::jsonb) WHERE id=$1`, id, string(summary))
	return approval, err
}
