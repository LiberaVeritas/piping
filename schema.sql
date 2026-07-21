CREATE TYPE job_state AS ENUM (
	'quota_insufficient',
	'quota_deducted',
	'print_sent',
	'print_succeeded',
	'print_failed',
	'refunded'
);

CREATE TABLE app_user (
	id text PRIMARY KEY, -- short username as canonical user id
);

CREATE TABLE semester (
	id integer PRIMARY KEY, -- e.g. 202601
	name text NOT NULL,
	default_quota integer NOT NULL
);

CREATE TABLE semester_grant (
	id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	user_id text NOT NULL REFERENCES app_user (id) ON DELETE RESTRICT ON UPDATE CASCADE,
	semester_id integer NOT NULL REFERENCES semester (id) ON DELETE RESTRICT,
	amount integer NOT NULL,
	granted_at timestamptz NOT NULL DEFAULT now (),
	CONSTRAINT amount_positive CHECK (amount >= 0),
	CONSTRAINT one_grant_per_semester UNIQUE (user_id, semester_id)
);

CREATE TABLE queue (
	id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	name text NOT NULL,
	enabled boolean NOT NULL DEFAULT true,
	policy text NOT NULL DEFAULT 'uniform'
);

CREATE TABLE destination (
	id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	queue_id bigint NOT NULL REFERENCES queue (id) ON DELETE RESTRICT,
	address text NOT NULL,
	name text NOT NULL,
	enabled boolean NOT NULL DEFAULT true
);

CREATE INDEX idx_destination_queue ON destination (queue_id);

CREATE TABLE job (
	id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	user_id text NOT NULL REFERENCES app_user (id) ON DELETE RESTRICT ON UPDATE CASCADE,
	queue_id bigint NOT NULL REFERENCES queue (id) ON DELETE RESTRICT,
	destination_id bigint REFERENCES destination (id) ON DELETE RESTRICT,
	state job_state NOT NULL,
	num_pages integer NOT NULL,
	num_color_pages integer NOT NULL DEFAULT 0,
	copies integer NOT NULL,
	cost integer NOT NULL,
	color boolean NOT NULL,
	duplex boolean NOT NULL,
	document_name text NOT NULL,
	submitted_at timestamptz NOT NULL DEFAULT now (),
	completed_at timestamptz,
	refunded_at timestamptz,
	CONSTRAINT num_pages_positive CHECK (num_pages > 0),
	CONSTRAINT color_pages_nonneg CHECK (num_color_pages >= 0),
	CONSTRAINT color_pages_within_total CHECK (num_color_pages <= num_pages),
	CONSTRAINT copies_positive CHECK (copies > 0),
	CONSTRAINT cost_positive CHECK (cost > 0)
);

CREATE INDEX idx_job_user_state ON job (user_id, state);

CREATE INDEX idx_job_state_submitted ON job (state, submitted_at);

CREATE INDEX idx_job_user_submitted ON job (user_id, submitted_at DESC);
