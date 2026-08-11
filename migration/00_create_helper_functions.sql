CREATE OR REPLACE FUNCTION generate_term_range(
    start_year integer,
    start_term integer,
    end_year integer,
    end_term integer
)
RETURNS SETOF integer AS $$
BEGIN
    RETURN QUERY
    SELECT (y.year * 100 + t.term)
    FROM generate_series(start_year, end_year) AS y(year)
    CROSS JOIN (VALUES (1), (5), (9)) AS t(term)
    WHERE (y.year * 100 + t.term) >= (start_year * 100 + start_term)
      AND (y.year * 100 + t.term) <= (end_year * 100 + end_term)
    ORDER BY y.year, t.term;
END;
$$ LANGUAGE plpgsql IMMUTABLE;

CREATE OR REPLACE FUNCTION term_to_string(term_code integer)
RETURNS text AS $$
DECLARE
    year integer;
    term integer;
    term_name text;
BEGIN
    year := term_code / 100;
    term := term_code % 100;

    term_name := CASE term
        WHEN 1 THEN 'Winter'
        WHEN 5 THEN 'Summer'
        WHEN 9 THEN 'Fall'
        ELSE 'Unknown'
    END;
    RETURN term_name || ' ' || year;
END;
$$ LANGUAGE plpgsql IMMUTABLE;

CREATE OR REPLACE FUNCTION quota_for(term_code integer)
RETURNS integer AS $$
BEGIN
	RETURN CASE
		WHEN term_code % 100 = 5 THEN 0
	    WHEN term_code = 201609 THEN 500
	    WHEN term_code BETWEEN 201701 AND 201905 THEN 1000
	    ELSE 250
	END;
END;
$$ LANGUAGE plpgsql IMMUTABLE;

CREATE OR REPLACE FUNCTION term_from_season_year(season integer, year integer)
RETURNS integer AS $$
DECLARE
	term integer;
BEGIN
	term := season * 4 + 1; -- 0 -> 01, 1 -> 05, 2 -> 09
	RETURN year * 100 + term;
END;
$$ LANGUAGE plpgsql IMMUTABLE;

CREATE OR REPLACE FUNCTION to_state(refunded boolean, error text)
RETURNS text AS $$
BEGIN
	RETURN CASE
		WHEN refunded THEN 'refunded'
		WHEN error IS NULL THEN 'print_succeeded'
		WHEN error ~* 'insufficient quota' THEN 'quota_insufficient'
		ELSE 'print_failed'
	END;
END;
$$ LANGUAGE plpgsql IMMUTABLE;

CREATE OR REPLACE FUNCTION to_completed_at(failed bigint, printed bigint)
RETURNS timestamptz AS $$
BEGIN
	RETURN CASE
		WHEN failed != -1 THEN to_timestamp(failed / 1000)
		ELSE to_timestamp(printed / 1000)
	END;
END;
$$ LANGUAGE plpgsql IMMUTABLE;

CREATE OR REPLACE FUNCTION to_refunded_at(refunded boolean, completed timestamptz)
RETURNS timestamptz AS $$
BEGIN
	RETURN CASE
		WHEN refunded THEN completed
		ELSE NULL
	END;
END;
$$ LANGUAGE plpgsql IMMUTABLE;

