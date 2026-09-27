package service

import (
	"context"
	"errors"
	"github.com/Gingoo-TvT/Qraft/backend/internal/access"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"testing"
)

type selectionStore struct {
	problemSetStore
	set    *domain.ProblemSet
	writes int
}

func (r *selectionStore) Create(_ context.Context, s *domain.ProblemSet) error {
	r.set = s
	r.writes++
	return nil
}
func (r *selectionStore) GetByID(context.Context, uuid.UUID) (*domain.ProblemSet, error) {
	return r.set, nil
}
func (r *selectionStore) AddItems(_ context.Context, _ uuid.UUID, items []domain.ProblemSetItem) error {
	r.writes++
	r.set.Items = append(r.set.Items, items...)
	return nil
}

type selectionReader struct{ denied uuid.UUID }

func (r *selectionReader) GetProblem(_ context.Context, id uuid.UUID) (*domain.Problem, error) {
	if id == r.denied {
		return nil, ErrNotFound
	}
	return &domain.Problem{ID: id, Title: "Synthetic draft", Statement: "fixture", Status: domain.ProblemStatus("draft")}, nil
}

func TestManualSelectionCreatesOrderedPrivateSetFromDrafts(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	repo := &selectionStore{}
	svc := NewProblemSetServiceWithDeps(repo, &selectionReader{}, nil, nil, nil)
	svc.SetWorkflowAccess(&WorkflowAccess{})
	ctx := access.WithPrincipal(context.Background(), access.Principal{UserID: "member-one", Role: "member"})
	got, err := svc.Create(ctx, ProblemSetCreateRequest{Title: "Synthetic curriculum", Visibility: domain.ProblemSetVisibilityPublic, Items: []ProblemSetAddItemRequest{{ProblemID: &b, Score: 20, Notes: "  pointer practice  "}, {ProblemID: &a, Score: 10}, {ProblemID: &b, Score: 20}}})
	if err != nil {
		t.Fatal(err)
	}
	if repo.writes != 1 || len(got.Items) != 2 || *got.Items[0].ProblemID != b || *got.Items[1].ProblemID != a || got.Items[0].Notes != "pointer practice" || got.TotalScore != 30 || got.DesiredItemCount != 2 {
		t.Fatalf("unexpected selection: %+v", got)
	}
	if got.Visibility != domain.ProblemSetVisibilityPrivate || got.OwnerUserID != "member-one" || got.Status != domain.ProblemSetStatusDraft {
		t.Fatalf("ownership/status changed: %+v", got)
	}
}

func TestManualSelectionValidatesEverySourceBeforeWriting(t *testing.T) {
	allowed, denied := uuid.New(), uuid.New()
	repo := &selectionStore{}
	svc := NewProblemSetServiceWithDeps(repo, &selectionReader{denied: denied}, nil, nil, nil)
	_, err := svc.Create(context.Background(), ProblemSetCreateRequest{Title: "Synthetic", Items: []ProblemSetAddItemRequest{{ProblemID: &allowed}, {ProblemID: &denied}}})
	if !errors.Is(err, ErrNotFound) || repo.writes != 0 {
		t.Fatalf("inaccessible source produced write: %v, %d", err, repo.writes)
	}
	for _, items := range [][]ProblemSetAddItemRequest{nil, {{ProblemID: &allowed, QuizID: &denied}}, {{ProblemID: &allowed, Score: -1}}, make([]ProblemSetAddItemRequest, 1001)} {
		if _, err := svc.prepareSelectedItems(context.Background(), items); err == nil {
			t.Fatalf("invalid batch accepted: %d", len(items))
		}
	}
}

func TestManualSelectionCannotEditOtherUsersOrSharedSet(t *testing.T) {
	id := uuid.New()
	repo := &selectionStore{set: &domain.ProblemSet{ID: uuid.New(), OwnerUserID: "owner", Visibility: domain.ProblemSetVisibilityPrivate}}
	svc := NewProblemSetServiceWithDeps(repo, &selectionReader{}, nil, nil, nil)
	svc.SetWorkflowAccess(&WorkflowAccess{})
	for _, p := range []access.Principal{{UserID: "other", Role: "member"}, {}} {
		ctx := access.WithPrincipal(context.Background(), p)
		if _, err := svc.AddItems(ctx, repo.set.ID, []ProblemSetAddItemRequest{{ProblemID: &id}}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unauthorized edit allowed: %v", err)
		}
	}
	repo.set.Visibility = domain.ProblemSetVisibilityPublic
	ctx := access.WithPrincipal(context.Background(), access.Principal{UserID: "owner", Role: "member"})
	if _, err := svc.AddItems(ctx, repo.set.ID, []ProblemSetAddItemRequest{{ProblemID: &id}}); !errors.Is(err, ErrNotFound) || repo.writes != 0 {
		t.Fatalf("shared set edit allowed: %v", err)
	}
}

func TestLibrarySearchAndSortReachRepository(t *testing.T) {
	got := problemRepositoryListFilter(ListProblemsFilter{Search: "B2009", SortBy: "serial_number", SortOrder: "asc"}, 10, 20)
	if got.Search != "B2009" || got.OrderBy != "serial_number" || got.OrderDirection != "asc" || got.Limit != 10 || got.Offset != 20 || !got.ExcludeQuarantined || !got.ExcludeRejected {
		t.Fatalf("query fields lost: %+v", got)
	}
}
