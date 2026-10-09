#!/usr/bin/env bash
set -euo pipefail

REPO=${POSTGRESQL_REPO:-${KITSOKI_PJ_REPOS_DIR:-$HOME/code}/postgresql}
BASELINE_SHA=${POSTGRESQL_BASELINE_SHA:-62c09cdc16757da93c373a197ec51a52b14bc2b3}
FIX_SHA=${POSTGRESQL_FIX_SHA:-64797ad97d6e0a476f809979df99e0013c1933b1}
BASELINE_WORKTREE=${POSTGRESQL_BASELINE_WORKTREE:-/private/tmp/pg-oracle-baseline}
FIX_WORKTREE=${POSTGRESQL_FIX_WORKTREE:-/private/tmp/pg-oracle-fix}
BASELINE_INSTALL=${POSTGRESQL_BASELINE_INSTALL:-/private/tmp/pg-install-alter-baseline}
FIX_INSTALL=${POSTGRESQL_FIX_INSTALL:-/private/tmp/pg-install-alter-fix}

spec_path="src/test/isolation/specs/alter-domain-validate.spec"
expected_path="src/test/isolation/expected/alter-domain-validate.out"

if [[ ! -d "$REPO/.git" ]]; then
  echo "postgresql oracle: repo not found at $REPO" >&2
  echo "postgresql oracle: set POSTGRESQL_REPO=/path/to/postgresql, or KITSOKI_PJ_REPOS_DIR=/parent/dir containing a postgresql/ checkout (default parent: \$HOME/code)" >&2
  exit 1
fi

if [[ ! -x "$BASELINE_INSTALL/lib/postgresql/pgxs/src/test/isolation/pg_isolation_regress" ]]; then
  echo "postgresql oracle: missing baseline runner at $BASELINE_INSTALL" >&2
  exit 1
fi
if [[ ! -x "$FIX_INSTALL/lib/postgresql/pgxs/src/test/isolation/pg_isolation_regress" ]]; then
  echo "postgresql oracle: missing fix runner at $FIX_INSTALL" >&2
  exit 1
fi

prepare_worktree() {
  local worktree=$1
  local sha=$2
  if [[ ! -d "$worktree" ]]; then
    echo "postgresql oracle: missing prepared worktree at $worktree" >&2
    exit 1
  fi
  local head
  head=$(git -C "$worktree" rev-parse HEAD)
  if [[ "$head" != "$sha" ]]; then
    echo "postgresql oracle: worktree $worktree is at $head, want $sha" >&2
    exit 1
  fi
  mkdir -p "$worktree/src/test/isolation/specs" "$worktree/src/test/isolation/expected"
  cat > "$worktree/$spec_path" <<'EOF'
# Test ALTER DOMAIN VALIDATE CONSTRAINT waits for already-running DML.

setup
{
	CREATE DOMAIN alter_domain_validate_d AS int;
	CREATE TABLE alter_domain_validate_t (a alter_domain_validate_d);
}

teardown
{
	DROP TABLE alter_domain_validate_t;
	DROP DOMAIN alter_domain_validate_d;
}

session s1
step s1_lock		{ DO $$ BEGIN PERFORM pg_advisory_lock(8888); END $$; }
step s1_unlock		{ DO $$ BEGIN PERFORM pg_advisory_unlock(8888); END $$; }

session s2
# CoerceToDomain initializes the domain constraint list during executor
# startup, before this CTE waits on the advisory lock.
step s2_insert		{ WITH wait AS MATERIALIZED (SELECT pg_advisory_lock(8888)) INSERT INTO alter_domain_validate_t SELECT (-1)::alter_domain_validate_d FROM wait; }

session s3
step s3_add			{ ALTER DOMAIN alter_domain_validate_d ADD CONSTRAINT alter_domain_validate_d_pos CHECK (VALUE > 0) NOT VALID; }
step s3_validate	{ ALTER DOMAIN alter_domain_validate_d VALIDATE CONSTRAINT alter_domain_validate_d_pos; }
step s3_validated	{ SELECT convalidated FROM pg_constraint WHERE conname = 'alter_domain_validate_d_pos'; }
step s3_check		{ SELECT count(*) FROM alter_domain_validate_t; }

permutation s1_lock s2_insert s3_add s3_validate s1_unlock s3_validated s3_check
EOF
  cat > "$worktree/$expected_path" <<'EOF'
Parsed test spec with 3 sessions

starting permutation: s1_lock s2_insert s3_add s3_validate s1_unlock s3_validated s3_check
step s1_lock: DO $$ BEGIN PERFORM pg_advisory_lock(8888); END $$;
step s2_insert: WITH wait AS MATERIALIZED (SELECT pg_advisory_lock(8888)) INSERT INTO alter_domain_validate_t SELECT (-1)::alter_domain_validate_d FROM wait; <waiting ...>
step s3_add: ALTER DOMAIN alter_domain_validate_d ADD CONSTRAINT alter_domain_validate_d_pos CHECK (VALUE > 0) NOT VALID;
step s3_validate: ALTER DOMAIN alter_domain_validate_d VALIDATE CONSTRAINT alter_domain_validate_d_pos; <waiting ...>
step s1_unlock: DO $$ BEGIN PERFORM pg_advisory_unlock(8888); END $$;
step s2_insert: <... completed>
step s3_validate: <... completed>
ERROR:  column "a" of table "alter_domain_validate_t" contains values that violate the new constraint
step s3_validated: SELECT convalidated FROM pg_constraint WHERE conname = 'alter_domain_validate_d_pos';
convalidated
------------
f           
(1 row)

step s3_check: SELECT count(*) FROM alter_domain_validate_t;
count
-----
    1
(1 row)

EOF
}

run_case() {
  local label=$1
  local worktree=$2
  local install=$3
  local port=$4
  local temp_instance
  temp_instance=$(mktemp -d "/private/tmp/pg-temp-${label}.XXXX")
  trap 'rm -rf "$temp_instance"' RETURN
  local runner="$install/lib/postgresql/pgxs/src/test/isolation/pg_isolation_regress"
  if ! output=$(cd "$worktree/src/test/isolation" && "$runner" --bindir="$install/bin" --temp-instance="$temp_instance" --port="$port" alter-domain-validate 2>&1); then
    printf '%s\n' "$output"
    return 1
  fi
  printf '%s\n' "$output"
}

prepare_worktree "$BASELINE_WORKTREE" "$BASELINE_SHA"
prepare_worktree "$FIX_WORKTREE" "$FIX_SHA"

set +e
baseline_output=$(run_case baseline "$BASELINE_WORKTREE" "$BASELINE_INSTALL" 55440)
baseline_status=$?
set -e
if [[ $baseline_status -eq 0 ]]; then
  echo "postgresql oracle: baseline unexpectedly passed" >&2
  printf '%s\n' "$baseline_output"
  exit 1
fi
if ! grep -q '1 of 1 tests failed.' <<<"$baseline_output"; then
  echo "postgresql oracle: baseline output did not show the expected failure state" >&2
  printf '%s\n' "$baseline_output"
  exit 1
fi

set +e
fix_output=$(run_case fix "$FIX_WORKTREE" "$FIX_INSTALL" 55439)
fix_status=$?
set -e
if [[ $fix_status -ne 0 ]]; then
  echo "postgresql oracle: fix output did not report success" >&2
  printf '%s\n' "$fix_output"
  exit 1
fi
if ! grep -q 'All 1 tests passed.' <<<"$fix_output"; then
  echo "postgresql oracle: fix output did not report success" >&2
  printf '%s\n' "$fix_output"
  exit 1
fi

echo "postgresql oracle: baseline red / fix green"
