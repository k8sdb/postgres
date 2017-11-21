#!/usr/bin/env bash

reset_owner() {
	chown -R postgres:postgres "$PGDATA"
	chmod 0700 -R "$PGDATA"
	chmod g+s /var/run/postgresql
	chown -R postgres /var/run/postgresql
}

initialize() {
	mkdir -p "$PGDATA"
	rm -rf "$PGDATA"/*
	reset_owner
	initdb "$PGDATA"
}

load_password() {
    PASSWORD_PATH='/srv/postgres/secrets/.admin'
	###### get postgres user password ######
	if [ -f "$PASSWORD_PATH" ]; then
		POSTGRES_PASSWORD=$(cat "$PASSWORD_PATH/POSTGRES_PASSWORD")
	else
		echo
		echo 'Missing environment file '${PASSWORD_PATH}'. Using default password.'
		echo
		POSTGRES_PASSWORD=${POSTGRES_PASSWORD:-postgres}
	fi
}

set_password() {
	load_password
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
    CRED_PATH='/srv/wal-g/secrets'
    if [ -d "$CRED_PATH" ]; then
        WALE_S3_PREFIX_PATH="$CRED_PATH/WALE_S3_PREFIX"
        if [ -f "$WALE_S3_PREFIX_PATH" ]; then
            export WALE_S3_PREFIX=$(cat "$WALE_S3_PREFIX_PATH")
        fi
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
    echo "Creating wal directory at " "$PGWAL"
    mkdir -p "$PGWAL"
    chmod 0700 "$PGWAL"

    # Adding additional configuration in /tmp/postgresql.conf
    echo "# ====== Archiving ======" >> /tmp/postgresql.conf
    echo "archive_mode = always" >> /tmp/postgresql.conf
    echo "archive_timeout = 0" >> /tmp/postgresql.conf
    echo "# ====== Archiving ======" >> /tmp/postgresql.conf
    echo "# ====== WRITE AHEAD LOG ======" >> /tmp/postgresql.conf
    echo "wal_level = $1" >> /tmp/postgresql.conf
    echo "max_wal_senders = 99" >> /tmp/postgresql.conf
    echo "wal_keep_segments = 32" >> /tmp/postgresql.conf

    if [[ -v STREAMING ]]; then
        if [ "$STREAMING" == "synchronous" ]; then
            echo "synchronous_commit = on" >> /tmp/postgresql.conf
            #
            echo "synchronous_standby_names = '3 (*)'" >> /tmp/postgresql.conf
        fi
    fi

    if [[ -v ARCHIVE ]]; then
        if [ "$ARCHIVE" == "wal-g" ]; then
            set_walg_env
            echo "archive_timeout = 60" >> /tmp/postgresql.conf
            echo "archive_command = 'wal-g wal-push %p'" >> /tmp/postgresql.conf
        fi
    fi

    echo "# ====== WRITE AHEAD LOG ======" >> /tmp/postgresql.conf
}

configure_primary_postgres() {
    if [[ -v STANDBY ]]; then
        if [ "$STANDBY" == "warm" ]; then
            use_standby "archive"
        elif [ "$STANDBY" == "hot" ]; then
            use_standby "hot_standby"
        fi
    fi

    if [ -s /tmp/postgresql.conf ]; then
        cat /tmp/postgresql.conf >> "$PGDATA/postgresql.conf"
    fi
}

configure_replica_postgres() {
    if [[ -v STANDBY ]]; then
        if [ "$STANDBY" == "hot" ]; then
            echo "hot_standby = on" >> /tmp/postgresql.conf
        fi
    fi

    if [ -s /tmp/postgresql.conf ]; then
        cat /tmp/postgresql.conf >> "$PGDATA/postgresql.conf"
    fi
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

    ls -la "$PGDATA"

    cp /scripts/replica/recovery.conf /tmp
    echo "archive_cleanup_command = 'pg_archivecleanup $PGWAL %r'" >> /tmp/recovery.conf
    # primary_conninfo is used for streaming replication
    echo "primary_conninfo = 'application_name=$HOSTNAME host=$PRIMARY_HOST'" >> /tmp/recovery.conf

    if [[ -v ARCHIVE ]]; then
        if [ "$ARCHIVE" == "wal-g" ]; then
            set_walg_env
            echo "restore_command = 'wal-g wal-fetch %f %p'" >> /tmp/recovery.conf
        fi
    fi

    cp /tmp/recovery.conf "$PGDATA/recovery.conf"
}

init_database() {

    create_pgpass_file
    psql=( psql -v ON_ERROR_STOP=1 --username "postgres" --dbname "postgres" )

    pg_ctl -D "$PGDATA"  -w start

    for f in "$INITDB/*"; do
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

            set_walg_env
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
    mkdir -p "$PGDATA"
	rm -rf "$PGDATA"/*
	reset_owner

	set_walg_env

	wal-g backup-fetch "$PGDATA" LATEST >/dev/null

	configure_replica_postgres

	mkdir -p "$PGDATA"/{pg_tblspc,pg_twophase,pg_stat,pg_commit_ts}/
	mkdir -p "$PGDATA"/pg_logical/{snapshots,mappings}/

	cp /scripts/replica/recovery.conf /tmp
	echo "restore_command = 'wal-g wal-fetch %f %p'" >> /tmp/recovery.conf
    cp /tmp/recovery.conf "$PGDATA/recovery.conf"

    touch '/tmp/pg-failover-trigger'

    check_recovery_done &
}


check_recovery_done() {
    while [ -f "/tmp/pg-failover-trigger" ]
    do
        echo "Waiting for archive recovery complete"
        sleep 2
    done

    create_pgpass_file

    PGHOST="127.0.0.1" PGPORT="5432" PGUSER="postgres" wal-g backup-push "$PGDATA" >/dev/null

    echo "Successfully pushed backup"
}
