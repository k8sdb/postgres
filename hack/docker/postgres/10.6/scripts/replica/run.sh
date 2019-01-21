#!/usr/bin/env bash

set e

echo "Running as Replica"

# set password ENV
export PGPASSWORD=${POSTGRES_PASSWORD:-postgres}

export ARCHIVE=${ARCHIVE:-}

gracefully_shutdown_host() {
  echo "Gracefully shutting down database"

  # start postgres server in background
  postgres >/dev/null &

  # Waiting for running Postgres
  while true; do
    echo "Attempting pg_isready on localhost"
    pg_isready --timeout=2 &>/dev/null && break
    sleep 2
  done

  # stop postgres server
  pg_ctl -D "$PGDATA" stop >/dev/null 2>&1
}

take_pg_basebackup() {
  mkdir -p "$PGDATA"
  rm -rf "$PGDATA"/*
  chmod 0700 "$PGDATA"

  echo "Taking base backup."
  pg_basebackup -X fetch --no-password --pgdata "$PGDATA" --username=postgres --host="$PRIMARY_HOST"
}

setup_postgresql_config() {
  # setup recovery.conf
  cp /scripts/replica/recovery.conf /tmp
  echo "recovery_target_timeline = 'latest'" >>/tmp/recovery.conf
  echo "archive_cleanup_command = 'pg_archivecleanup $PGWAL %r'" >>/tmp/recovery.conf

  # primary_conninfo is used for streaming replication
  echo "primary_conninfo = 'application_name=$HOSTNAME host=$PRIMARY_HOST'" >>/tmp/recovery.conf
  mv /tmp/recovery.conf "$PGDATA/recovery.conf"

  # setup postgresql.conf
  cp /scripts/primary/postgresql.conf /tmp
  echo "wal_level = replica" >>/tmp/postgresql.conf
  echo "max_wal_senders = 99" >>/tmp/postgresql.conf
  echo "wal_keep_segments = 32" >>/tmp/postgresql.conf
  echo "wal_log_hints = on" >>/tmp/postgresql.conf

  if [ "$STANDBY" == "hot" ]; then
    echo "hot_standby = on" >>/tmp/postgresql.conf
  fi

  if [ "$STREAMING" == "synchronous" ]; then
    # setup synchronous streaming replication
    echo "synchronous_commit = remote_write" >>/tmp/postgresql.conf
    echo "synchronous_standby_names = '*'" >>/tmp/postgresql.conf
  fi

  mv /tmp/postgresql.conf "$PGDATA/postgresql.conf"

  # push base-backup
  if [ "$ARCHIVE" == "wal-g" ]; then
    # set walg ENV
    CRED_PATH="/srv/wal-g/archive/secrets"
    export WALE_S3_PREFIX=$(echo "$ARCHIVE_S3_PREFIX")
    export AWS_ACCESS_KEY_ID=$(cat "$CRED_PATH/AWS_ACCESS_KEY_ID")
    export AWS_SECRET_ACCESS_KEY=$(cat "$CRED_PATH/AWS_SECRET_ACCESS_KEY")

    # setup postgresql.conf
    echo "archive_command = 'wal-g wal-push %p'" >>"$PGDATA/postgresql.conf"
    echo "archive_timeout = 60" >>"$PGDATA/postgresql.conf"
    echo "archive_mode = always" >>"$PGDATA/postgresql.conf"
  fi
}

# Waiting for running Postgres
TIMER=0
while [[ ${TIMER} -le 30 ]]; do
  pg_isready --host="$PRIMARY_HOST" --timeout=2 &>/dev/null && break
  echo "Attempting pg_isready on primary"
  TIMER=$((TIMER + 1))
  sleep 2
done

pg_isready --host="$PRIMARY_HOST" --timeout=2

while true; do
  psql -h "$PRIMARY_HOST" --no-password --username=postgres --command="select now();" &>/dev/null && break
  echo "Attempting query on primary"
  sleep 2
done

if [ ! -e "$PGDATA/PG_VERSION" ]; then
  echo "asdfasdfsdf"
  take_pg_basebackup
else
  # pg_rewind. https://www.postgresql.org/docs/9.6/app-pgrewind.html

  # Why pg_rewind? refs:
  # - Resolves conflict of different timelines. ref:
  # 1. http://hlinnaka.iki.fi/presentations/NordicPGDay2015-pg_rewind.pdf
  # 2. part(1 of 3) https://blog.2ndquadrant.com/introduction-to-pgrewind/
  # 3. part(2 of 3) https://blog.2ndquadrant.com/pgrewind-and-pg95/
  # 4. part(3 of 3) https://blog.2ndquadrant.com/back-to-the-future-part-3-pg_rewind-with-postgresql-9-6/

  # Why don't just pull all WAL file?
  # - Doesn't solve conflict between different timelines, (mostly, in failover scenario, where a standby node becomes primary)
  # So, after pulling wal files, it is required to run pg_rewind.

  # > pw_rewind. Possible error:
  # 1. target server must be shut down cleanly
  # 2. could not find previous WAL record at 0/30000F8
  # 3. could not find common ancestor of the source and target cluster's timelines
  # 4. target server needs to use either data checksums or "wal_log_hints = on"
  # 5. None of the above. Example: "source and target clusters are from different systems"

  EXIT_CODE=0
  PG_REWIND_OUTPUT=$(pg_rewind --source-server="host=$PRIMARY_HOST user=postgres port=5432 dbname=postgres" --target-pgdata=$PGDATA) || EXIT_CODE=$?
  echo "${PG_REWIND_OUTPUT}"

  # Target database (localhost) must be shutdown cleanly to perform pg_rewind.
  # So, check and if necessary, re-stop the database gracefully.
  if [[ "$EXIT_CODE" != "0" ]] && [[ $(echo $PG_REWIND_OUTPUT | grep -c "target server must be shut down cleanly") -gt 0 ]]; then
    setup_postgresql_config
    gracefully_shutdown_host

    EXIT_CODE=0
    PG_REWIND_OUTPUT=$(pg_rewind --source-server="host=$PRIMARY_HOST user=postgres port=5432 dbname=postgres" --target-pgdata=$PGDATA) || EXIT_CODE=$?
    echo ${PG_REWIND_OUTPUT}
  fi

  if
    ([[ "$EXIT_CODE" != "0" ]] && [[ $(echo $PG_REWIND_OUTPUT | grep -c "could not find previous WAL record") -gt 0 ]]) ||
      # If the server diverged from primary and the diverged WAL doesn't exist anymore,
      # pg_rewind will throw an error similar to "could not find previous WAL record at 0/30000F8".
      # We have to manually fetch WALs starting from the missing point.
      # At this point, we will take pg_basebackup.
      # TODO: for wal-g or other kind of wal-archiving, fetch missing WALs from archive storage (may be). Then, run pg_rewind again
    ([[ "$EXIT_CODE" != "0" ]] && [[ $(echo $PG_REWIND_OUTPUT | grep -c "could not find common ancestor") -gt 0 ]]) ||
      # Since 9.6, pg_rewind is very powerful to find common ancestor while running on non-promoted master.
      # ref: https://www.enterprisedb.com/blog/pgrewind-improvements-postgresql-96
      # Yet, if the error shows up, taking pg_basebackup is a must.
    ([[ "$EXIT_CODE" != "0" ]] && [[ $(echo $PG_REWIND_OUTPUT | grep -c "server needs to use either data checksums") -gt 0 ]])
      # In case of upgrade from previous database version, where 'wal_log_hints' was not turned on, this error may occur.
      # But, will not occur again after adding 'wal_log_hints = on' on config file.
      # We could have skipped here and manually pull WAL files so that this node can redo wal files.
      # But, again, that will not resolve conflict between timelines.
      # So, take base_backup and continue
  then
    take_pg_basebackup

  elif [[ "$EXIT_CODE" != "0" ]]; then
    # In another scenario, pg_rewind is failing and the reason is not 'non-existing WAL' or 'no common ancestor'.
    # One example is, "source and target clusters are from different systems". this error happens when,
    # the primary starts from scratch (when user forces to do, ie deleting pvc manually). It is safe to exit in these similar cases
    # without processing further, because accidental behavior of user can result into data loss.
    echo "pg_rewind is failing and the reason is: $PG_REWIND_OUTPUT"
    exit 1
  fi
fi

setup_postgresql_config

exec postgres