#!/bin/sh

set -e

echo "run db migration"
# required to update variables when loading from secrets manager
# source /app/app.env
/app/migrate -path /app/db/migration -database "$DB_SOURCE" -verbose up

echo "start the app"
exec "$@"