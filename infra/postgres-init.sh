#!/bin/sh
set -eu
psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --set=ON_ERROR_STOP=1 --set=csf_password="$CSF_DB_PASSWORD" <<'SQL'
CREATE USER brain PASSWORD :'csf_password';
CREATE DATABASE brain OWNER brain;
SQL
