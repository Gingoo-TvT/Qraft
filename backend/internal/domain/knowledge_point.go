package domain

import (
	"time"

	"github.com/google/uuid"
)

const (
	QuizSubjectCLanguage              = "c_language"
	QuizSubjectDataStructureAlgorithm = "data_structure_algorithm"
)

type KnowledgePoint struct {
	ID        uuid.UUID  `json:"id" db:"id"`
	Subject   string     `json:"subject" db:"subject"`
	Code      string     `json:"code" db:"code"`
	Name      string     `json:"name" db:"name"`
	ParentID  *uuid.UUID `json:"parent_id,omitempty" db:"parent_id"`
	SortOrder int        `json:"sort_order" db:"sort_order"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
}
