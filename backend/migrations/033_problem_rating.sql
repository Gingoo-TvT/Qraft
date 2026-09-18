-- Rating evidence never overwrites problems.difficulty or edit-refresh state.
CREATE TABLE rating_subjects (
 problem_id UUID NOT NULL REFERENCES problems(id) ON DELETE CASCADE,
 subject_hash TEXT NOT NULL CHECK(length(subject_hash)=64),
 payload JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(problem_id,subject_hash)
);
CREATE TABLE rating_assessments (
 id UUID PRIMARY KEY, problem_id UUID NOT NULL, subject_hash TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('pending','running','completed','failed','cancelled')),
 payload JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(problem_id,subject_hash) REFERENCES rating_subjects(problem_id,subject_hash) ON DELETE CASCADE
);
CREATE UNIQUE INDEX rating_one_active_assessment ON rating_assessments(problem_id) WHERE status IN ('pending','running');
CREATE INDEX rating_assessment_problem ON rating_assessments(problem_id,created_at DESC);
CREATE TABLE rating_anchors (
 id UUID PRIMARY KEY, payload JSONB NOT NULL, reviewed_by TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE rating_reviewers (
 id UUID PRIMARY KEY, reviewer_key TEXT NOT NULL UNIQUE CHECK(length(reviewer_key) BETWEEN 1 AND 200)
);
CREATE TABLE rating_invitations (
 id UUID PRIMARY KEY, problem_id UUID NOT NULL, subject_hash TEXT NOT NULL,
 reviewer_id UUID NOT NULL REFERENCES rating_reviewers(id), token_hash TEXT NOT NULL UNIQUE CHECK(length(token_hash)=64),
 window_minutes INT NOT NULL CHECK(window_minutes BETWEEN 1 AND 1440),
 context TEXT NOT NULL CHECK(context IN ('practice','contest')),
 expires_at TIMESTAMPTZ NOT NULL, revoked_at TIMESTAMPTZ,
 created_by TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(problem_id,subject_hash) REFERENCES rating_subjects(problem_id,subject_hash) ON DELETE CASCADE
);
CREATE INDEX rating_invitation_problem ON rating_invitations(problem_id,created_at DESC);
CREATE TABLE rating_feedback (
 id UUID PRIMARY KEY, problem_id UUID NOT NULL, subject_hash TEXT NOT NULL,
 reviewer_id UUID NOT NULL REFERENCES rating_reviewers(id), revision INT NOT NULL CHECK(revision>0),
 payload JSONB NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(problem_id,subject_hash,reviewer_id),
 FOREIGN KEY(problem_id,subject_hash) REFERENCES rating_subjects(problem_id,subject_hash) ON DELETE CASCADE
);
CREATE TABLE rating_feedback_revisions (
 feedback_id UUID NOT NULL REFERENCES rating_feedback(id) ON DELETE CASCADE,
 revision INT NOT NULL, payload JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(feedback_id,revision)
);
CREATE TABLE rating_calibrations (
 id UUID PRIMARY KEY, problem_id UUID NOT NULL, subject_hash TEXT NOT NULL,
 feedback_hash TEXT NOT NULL, rule_version TEXT NOT NULL, payload JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(problem_id,subject_hash,feedback_hash,rule_version),
 FOREIGN KEY(problem_id,subject_hash) REFERENCES rating_subjects(problem_id,subject_hash) ON DELETE CASCADE
);
CREATE TABLE rating_decisions (
 id UUID PRIMARY KEY, problem_id UUID NOT NULL, subject_hash TEXT NOT NULL,
 payload JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(problem_id,subject_hash) REFERENCES rating_subjects(problem_id,subject_hash) ON DELETE CASCADE
);
CREATE TABLE rating_official (
 problem_id UUID PRIMARY KEY REFERENCES problems(id) ON DELETE CASCADE,
 subject_hash TEXT NOT NULL, rating INT NOT NULL CHECK(rating BETWEEN 800 AND 3500 AND rating%100=0),
 decision_id UUID NOT NULL REFERENCES rating_decisions(id),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
