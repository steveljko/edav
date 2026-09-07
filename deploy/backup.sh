#!/bin/sh
# Back up the database while the server is running.
#
# Copying the file directly is not safe: WAL mode keeps recent writes in a
# separate file, so a plain copy can miss them or catch a torn page. VACUUM
# INTO takes a consistent snapshot and compacts it, without stopping anything.
#
#   ./backup.sh /var/backups/edav
#
# For the Docker deployment, run it against the volume:
#
#   docker compose exec edav /dav -healthcheck   # confirm it is up first
#   docker run --rm -v edav_edav-data:/data -v "$PWD":/out alpine \
#       sh -c 'apk add --no-cache sqlite >/dev/null && \
#              sqlite3 /data/edav.db "VACUUM INTO \"/out/edav-$(date +%F).db\""'

set -eu

DEST="${1:?usage: backup.sh <destination directory>}"
SOURCE="${EDAV_DB_PATH:-/var/lib/edav/edav.db}"
KEEP="${KEEP:-14}"

if [ ! -f "$SOURCE" ]; then
	echo "no database at $SOURCE" >&2
	exit 1
fi

mkdir -p "$DEST"
OUT="$DEST/edav-$(date +%Y%m%d-%H%M%S).db"

sqlite3 "$SOURCE" "VACUUM INTO '$OUT'"
chmod 600 "$OUT"

# A backup that cannot be read is not a backup.
if ! sqlite3 "$OUT" "PRAGMA integrity_check" | grep -q '^ok$'; then
	echo "integrity check failed for $OUT" >&2
	exit 1
fi

echo "$OUT"

# Keep the most recent few, drop the rest.
ls -1t "$DEST"/edav-*.db 2>/dev/null | tail -n "+$((KEEP + 1))" | while read -r old; do
	rm -f "$old"
done
