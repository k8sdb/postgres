#!/usr/bin/env bash

reset_owner() {
    mkdir -p "$PGDATA"
    rm -rf "$PGDATA"/*
    chmod 0700 "$PGDATA"
}

initialize() {
    reset_owner
    initdb "$PGDATA"
}

set_default_password() {
    POSTGRES_PASSWORD=${POSTGRES_PASSWORD:-postgres}
}

set_password() {
    set_default_password
    pg_ctl -D "$PGDATA"  -w start

    psql --username postgres <<-EOSQL
ALTER USER postgres WITH SUPERUSER PASSWORD '$POSTGRES_PASSWORD';
EOSQL
    pg_ctl -D "$PGDATA" -m fast -w stop
}

configure_pghba() {
    { echo; echo 'local all         all                         trust'; }   >> "$PGDATA/pg_hba.conf"
    {       echo 'host  all         all         127.0.0.1/32    trust'; }   >> "$PGDATA/pg_hba.conf"
    {       echo 'host  all         all         0.0.0.0/0       md5'; }     >> "$PGDATA/pg_hba.conf"
    {       echo 'host  replication postgres    0.0.0.0/0       md5'; }     >> "$PGDATA/pg_hba.conf"
}

set_walg_env() {
    CRED_PATH="$1"
    if [ -d "$CRED_PATH" ]; then
        AWS_ACCESS_KEY_ID_PATH="$CRED_PATH/AWS_ACCESS_KEY_ID"
        if [ -f "$AWS_ACCESS_KEY_ID_PATH" ]; then
            export AWS_ACCESS_KEY_ID=$(cat "$AWS_ACCESS_KEY_ID_PATH")
        fi
        AWS_SECRET_ACCESS_KEY_PATH="$CRED_PATH/AWS_SECRET_ACCESS_KEY"
        if [ -f "$AWS_SECRET_ACCESS_KEY_PATH" ]; then
            export AWS_SECRET_ACCESS_KEY=$(cat "$AWS_SECRET_ACCESS_KEY_PATH")
        fi
    fi
}

use_standby() {
    # Adding additional configuration in /tmp/postgresql.conf
    echo "# ====== Archiving ======" >> /tmp/postgresql.conf
    echo "archive_mode = always" >> /tmp/postgresql.conf

    archive_command="'test ! -f $PGWAL/%f && cp %p $PGWAL/%f'"
    archive_timeout=0

    if [[ -v ARCHIVE ]]; then
        if [ "$ARCHIVE" == "wal-g" ]; then
            export WALE_S3_PREFIX=$(echo "$ARCHIVE_S3_PREFIX")
            set_walg_env "/srv/wal-g/archive/secrets"
            archive_timeout=60
            archive_command="'wal-g wal-push %p'"
        fi
    fi

    echo "archive_command = $archive_command" >> /tmp/postgresql.conf
    echo "archive_timeout = $archive_timeout" >> /tmp/postgresql.conf

    echo "# ====== Archiving ======" >> /tmp/postgresql.conf

    echo "# ====== WRITE AHEAD LOG ======" >> /tmp/postgresql.conf
    echo "max_wal_senders = 99" >> /tmp/postgresql.conf
    echo "wal_keep_segments = 32" >> /tmp/postgresql.conf
    echo "# ====== WRITE AHEAD LOG ======" >> /tmp/postgresql.conf
}

configure_primary_postgres() {
    cp /scripts/primary/postgresql.conf /tmp
    use_standby
    cp /tmp/postgresql.conf "$PGDATA/postgresql.conf"
}

configure_replica_postgres() {
    cp /scripts/primary/postgresql.conf /tmp
    use_standby
    if [ "$STANDBY" == "hot" ]; then
        echo "hot_standby = on" >> /tmp/postgresql.conf
    fi
    cp /tmp/postgresql.conf "$PGDATA/postgresql.conf"
}

create_pgpass_file() {
    rm -rf /tmp/.pgpass
    cat >> "/tmp/.pgpass" <<-EOF
*:*:*:*:${POSTGRES_PASSWORD}
EOF
    chmod 0600 "/tmp/.pgpass"
    export PGPASSFILE=/tmp/.pgpass
}

wait_for_running() {
    while true; do
        pg_isready --host="$PRIMARY_HOST" --timeout=2 &>/dev/null && break
        echo "Attempting pg_isready on primary"
        sleep 2
    done

    while true; do
        psql -h "$PRIMARY_HOST" --no-password --command="select now();" &>/dev/null && break
        echo "Attempting query on primary"
        sleep 2
    done
}

base_backup() {
    pg_basebackup -X fetch --no-password --pgdata "$PGDATA" --host="$PRIMARY_HOST"

    cp /scripts/replica/recovery.conf /tmp
    echo "recovery_target_timeline = 'latest'" >> /tmp/recovery.conf
    echo "archive_cleanup_command = 'pg_archivecleanup $PGWAL %r'" >> /tmp/recovery.conf
    # primary_conninfo is used for streaming replication
    echo "primary_conninfo = 'application_name=$HOSTNAME host=$PRIMARY_HOST'" >> /tmp/recovery.conf

    if [[ -v ARCHIVE ]]; then
        if [ "$ARCHIVE" == "wal-g" ]; then
            export WALE_S3_PREFIX=$(echo "$ARCHIVE_S3_PREFIX")
            set_walg_env "/srv/wal-g/archive/secrets"
            echo "restore_command = 'wal-g wal-fetch %f %p'" >> /tmp/recovery.conf
        fi
    fi

    cp /tmp/recovery.conf "$PGDATA/recovery.conf"
}

init_database() {
    create_pgpass_file
    psql=( psql -v ON_ERROR_STOP=1 --username "postgres" --dbname "postgres" )

    pg_ctl -D "$PGDATA" -w start

    for f in "$INITDB"/*; do
        case "$f" in
            *.sh)     echo "$0: running $f"; . "$f" ;;
            *.sql)    echo "$0: running $f"; "${psql[@]}" -f "$f"; echo ;;
            *.sql.gz) echo "$0: running $f"; gunzip -c "$f" | "${psql[@]}"; echo ;;
            *)        echo "$0: ignoring $f" ;;
        esac
        echo
    done

    pg_ctl -D "$PGDATA" -m fast -w stop
}

push_backup() {
    if [[ -v ARCHIVE ]]; then
        if [ "$ARCHIVE" == "wal-g" ]; then

            echo "Pushing base backup"

            export WALE_S3_PREFIX=$(echo "$ARCHIVE_S3_PREFIX")
            set_walg_env "/srv/wal-g/archive/secrets"
            create_pgpass_file

            PGHOST="127.0.0.1"
            if [ "$MODE" == "replica" ]; then
                PGHOST="$PRIMARY_HOST"
            fi

            pg_ctl -D "$PGDATA"  -w start
            PGPORT="5432" PGUSER="postgres" wal-g backup-push "$PGDATA" >/dev/null
            pg_ctl -D "$PGDATA" -m fast -w stop

            echo "Successfully pushed backup"
        fi
    fi
}

restore_from_walg() {
    reset_owner
    # Restore from wal archive
    export WALE_S3_PREFIX=$(echo "$RESTORE_S3_PREFIX")
    set_walg_env "/srv/wal-g/restore/secrets"

    wal-g backup-fetch "$PGDATA" "$BACKUP_NAME" >/dev/null

    mkdir -p "$PGDATA"/{pg_tblspc,pg_twophase,pg_stat,pg_commit_ts}/
    mkdir -p "$PGDATA"/pg_logical/{snapshots,mappings}/

    cp /scripts/replica/recovery.conf /tmp
    echo "recovery_target_timeline = '$PITR'" >> /tmp/recovery.conf
    echo "restore_command = 'wal-g wal-fetch %f %p'" >> /tmp/recovery.conf
    cp /tmp/recovery.conf "$PGDATA/recovery.conf"

    pg_ctl -D "$PGDATA" -W start >/dev/null

    touch '/tmp/pg-failover-trigger'

    # This will hold until restore completed
    while [ ! -e "$PGDATA/recovery.done" ]
    do
        sleep 2
    done

    postmaster -D "$PGDATA" >/dev/null

    pg_ctl -D "$PGDATA" -w stop >/dev/null

    configure_primary_postgres

    push_backup
}
