BEGIN;

TRUNCATE job,
semester,
semester_grant,
app_user,
queue,
destination CASCADE;


INSERT INTO
	app_user (id)
SELECT
	_id
FROM
	fulluser;

INSERT INTO
	app_user (id)
SELECT DISTINCT
	useridentification
FROM
	printjob
WHERE
	useridentification IS NOT NULL
ON CONFLICT (id) DO NOTHING;


ALTER TABLE queue
ADD COLUMN legacy_id text NOT NULL;

ALTER TABLE queue
ADD CONSTRAINT queue_legacy UNiQUE (legacy_id);

-- Table "public.queue"
-- Column    |  Type   | Collation | Nullable |           Default
-- -----------+---------+-----------+----------+------------------------------
-- id        | bigint  |           | not null | generated always as identity
-- name      | text    |           | not null |
-- enabled   | boolean |           | not null | true
-- policy    | text    |           | not null | 'uniform'::text
-- legacy_id | text    |           |          |

INSERT INTO
	queue (legacy_id, name)
SELECT DISTINCT
	pqd.printqueue__id,
	pq.name
FROM
	fulldestination d
	JOIN printqueue_destinations pqd ON d._id = pqd.destinations
	JOIN printqueue pq ON pqd.printqueue__id = pq._id
GROUP BY
	pq._id,
	pqd.printqueue__id;

INSERT INTO
	queue (legacy_id, name, enabled)
SELECT DISTINCT
	pj.queueid,
	COALESCE (pq.name, pj.queueid),
	false
FROM
	printjob pj
	FULL JOIN fulldestination d ON pj.destination = d._id
	FULL JOIN printqueue pq ON pj.queueid = pq._id
WHERE
	pj.failed = -1 AND pj.pages > 0 AND pj.queueid NOT IN (SELECT _id FROM printqueue);


ALTER TABLE destination
ADD COLUMN legacy_id text NOT NULL;

ALTER TABLE destination
ADD CONSTRAINT destination_legacy UNiQUE (legacy_id);

-- Table "public.destination"
-- Column    |  Type   | Collation | Nullable |           Default
-- -----------+---------+-----------+----------+------------------------------
-- id        | bigint  |           | not null | generated always as identity
-- queue_id  | bigint  |           | not null |
-- address   | text    |           | not null |
-- name      | text    |           | not null |
-- enabled   | boolean |           | not null | true
-- legacy_id | text    |           |          |
INSERT INTO
	destination (legacy_id, name, address, queue_id)
SELECT DISTINCT
	d._id,
	d.name,
	d.path,
	q.id
FROM
	fulldestination d
	JOIN printqueue_destinations pqd ON pqd.destinations = d._id
	JOIN queue q ON pqd.printqueue__id = q.legacy_id;

INSERT INTO
	destination (legacy_id, queue_id, address, name, enabled)
SELECT DISTINCT
	destination,
	queue.id,
	path,
	fulldestination.name,
	false
FROM
	printjob
	JOIN fulldestination ON destination = fulldestination._id
	JOIN queue ON queueid = queue.legacy_id
WHERE destination NOT IN (SELECT legacy_id FROM destination)
ON CONFLICT (legacy_id) DO NOTHING;


INSERT INTO
	semester (id, name, default_quota)
SELECT
	code,
	term_to_string (code),
	quota_for (code)
FROM
	generate_term_range (2016, 09, 2026, 05) AS sem (code);


INSERT INTO
	semester_grant (user_id, semester_id, amount)
SELECT DISTINCT
	fs.fulluser__id,
	term_from_season_year (fs.season, fs.year),
	s.default_quota
FROM
	fulluser_semesters as fs
	JOIN semester s ON s.id = term_from_season_year (fs.season, fs.year);


ALTER TABLE job
ADD COLUMN legacy_id text NOT NULL;

ALTER TABLE job
ADD CONSTRAINT job_legacy UNiQUE (legacy_id);

-- Table "public.printjob"
-- Column       |          Type          | Collation | Nullable | Default
-- --------------------+------------------------+-----------+----------+---------
-- _id                | character varying(36)  |           | not null |
-- _rev               | character varying(255) |           |          |
-- schema             | character varying(255) |           |          |
-- type               | character varying(255) |           |          |
-- colorpages         | integer                |           | not null |
-- deletedataon       | bigint                 |           | not null |
-- destination        | character varying(255) |           |          |
-- error              | character varying(255) |           |          |
-- eta                | bigint                 |           | not null |
-- failed             | bigint                 |           | not null |
-- file               | character varying(255) |           |          |
-- isrefunded         | boolean                |           | not null |
-- name               | character varying(255) |           |          |
-- originalhost       | character varying(255) |           |          |
-- pages              | integer                |           | not null |
-- printed            | bigint                 |           | not null |
-- processed          | bigint                 |           | not null |
-- queueid            | character varying(255) |           |          |
-- received           | bigint                 |           | not null |
-- started            | bigint                 |           | not null |
-- useridentification | character varying(255) |           |          |
--
-- Table "public.job"
-- Column      |           Type           | Collation | Nullable |           Default
-- -----------------+--------------------------+-----------+----------+------------------------------
-- id              | bigint                   |           | not null | generated always as identity
-- user_id         | text                     |           | not null |
-- queue_id        | bigint                   |           | not null |
-- destination_id  | bigint                   |           |          |
-- state           | job_state                |           | not null |
-- num_pages       | integer                  |           | not null |
-- num_color_pages | integer                  |           | not null | 0
-- copies          | integer                  |           | not null |
-- cost            | integer                  |           | not null |
-- color           | boolean                  |           | not null |
-- duplex          | boolean                  |           | not null |
-- document_name   | text                     |           | not null |
-- submitted_at    | timestamp with time zone |           | not null | now()
-- completed_at    | timestamp with time zone |           |          |
-- refunded_at     | timestamp with time zone |           |          |
-- legacy_id       | text                     |           |          |
INSERT INTO
	job (
		user_id,
		queue_id,
		destination_id,
		state,
		num_pages,
		num_color_pages,
		copies,
		cost,
		color,
		duplex,
		document_name,
		submitted_at,
		completed_at,
		refunded_at,
		legacy_id
	)
SELECT
	pj.useridentification,
	queue.id AS queue_id,
	d.id AS destination_id,
	to_state (pj.isrefunded, pj.error)::job_state,
	pj.pages,
	pj.colorpages,
	1,
	(pj.pages + 2 * pj.colorpages),
	(pj.colorpages != 0),
	true,
	pj.name,
	to_timestamp(pj.processed / 1000),
	to_completed_at(pj.failed, pj.printed),
	to_refunded_at(pj.isrefunded, to_completed_at(pj.failed, pj.printed)),
	pj._id
FROM
	printjob pj
	JOIN queue ON pj.queueid = queue.legacy_id
	JOIN destination d ON pj.destination = d.legacy_id
WHERE
	pj.processed != -1
	AND pj.pages > 0
	AND COALESCE(pj.error, '') !~* 'too many pages'
	AND COALESCE(pj.error, '') !~* 'color disabled'
	AND COALESCE(pj.error, '') !~* 'invalid destination'
	AND COALESCE(pj.error, '') !~* 'failed to process'
	AND COALESCE(pj.error, '') !~* 'exception during processing';


ALTER TABLE queue DROP COLUMN legacy_id;

ALTER TABLE destination DROP COLUMN legacy_id;

ALTER TABLE job DROP COLUMN legacy_id;


WITH tepid AS
	(SELECT
		useridentification, SUM(pages + 2 * colorpages) AS tepid_sum
	FROM
		printjob
	WHERE
		NOT isrefunded AND failed = -1
		AND processed != -1 AND pages > 0
	GROUP BY
		useridentification),
piping AS
	(SELECT
		user_id, SUM(cost) AS piping_sum
	FROM
		job
	WHERE
		state = 'print_succeeded'
	GROUP BY user_id)
SELECT
	user_id,
	tepid_sum,
	piping_sum
FROM
	tepid FULL JOIN piping ON useridentification = user_id
WHERE
	tepid_sum IS DISTINCT FROM piping_sum;


SELECT
    (submitted_at)
FROM
    job
WHERE
    submitted_at > now();


DROP TABLE printjob;
DROP TABLE fulluser_adgroup;
DROP TABLE fulluser_course;
DROP TABLE fulluser_groups;
DROP TABLE fulluser_semesters;
DROP TABLE fullsession;
DROP TABLE fulldestination;
DROP TABLE destinationticket;
DROP TABLE printqueue_destinations;
DROP TABLE printqueue;
DROP TABLE course;
DROP TABLE adgroup;
DROP TABLE fulluser;
DROP TABLE marqueedata_entry;
DROP TABLE marqueedata;
DROP FUNCTION generate_term_range;
DROP FUNCTION term_to_string;
DROP FUNCTION term_from_season_year;
DROP FUNCTION to_state;
DROP FUNCTION to_completed_at;
DROP FUNCTION to_refunded_at;


COMMIT;

