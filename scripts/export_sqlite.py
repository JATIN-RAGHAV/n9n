"""Export a stopped legacy SQLite database for import into an empty n9n Postgres schema.

Usage: python3 scripts/export_sqlite.py /secure/backup/n9n.db > /secure/backup/import.sql
Then: psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f /secure/backup/import.sql
The export contains private data. Protect it like the original database.
"""
import argparse
import sqlite3
from pathlib import Path

TABLES = ('users', 'sessions', 'workflows', 'versions', 'credentials', 'runs',
          'steps', 'trigger_events', 'checkpoints', 'oauth_states')


def literal(value):
    if value is None:
        return 'NULL'
    if isinstance(value, bytes):
        return "decode('" + value.hex() + "','hex')"
    if isinstance(value, (int, float)):
        return str(value)
    return "'" + str(value).replace("'", "''") + "'"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('database', type=Path)
    args = parser.parse_args()
    db = sqlite3.connect(args.database.resolve().as_uri() + '?mode=ro', uri=True)
    if db.execute('PRAGMA integrity_check').fetchone()[0] != 'ok':
        raise RuntimeError('SQLite integrity check failed')
    print('BEGIN;\nSET standard_conforming_strings = on;')
    print('LOCK TABLE ' + ', '.join(TABLES) + ' IN ACCESS EXCLUSIVE MODE;')
    for table in TABLES:
        print("DO $$ BEGIN IF EXISTS (SELECT 1 FROM " + table + ") THEN RAISE EXCEPTION 'Destination must be empty'; END IF; END $$;")
    for table in TABLES:
        cursor = db.execute('SELECT * FROM ' + table)
        columns = [column[0] for column in cursor.description]
        for row in cursor:
            values = list(row)
            if table == 'workflows':
                values[columns.index('active')] = bool(values[columns.index('active')])
            formatted = [('TRUE' if value else 'FALSE') if isinstance(value, bool) else literal(value) for value in values]
            print('INSERT INTO ' + table + ' (' + ','.join('"' + col + '"' for col in columns) + ') VALUES (' + ','.join(formatted) + ');')
    print("SELECT setval(pg_get_serial_sequence('steps','id'), COALESCE(MAX(id),1), MAX(id) IS NOT NULL) FROM steps;")
    print('COMMIT;')


if __name__ == '__main__':
    main()
