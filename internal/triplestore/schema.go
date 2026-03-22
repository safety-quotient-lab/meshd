// Package triplestore provides an RDF triple store backed by SQLite.
//
// Zero CGO — wraps the existing db.Exec()/db.QueryJSON() pattern.
// Ontology-driven: predicates and SHACL shapes load from
// ns/mesh/ontology.jsonld at runtime. New predicates require no recompile.
//
// Named graphs map to Plan 9 namespaces — composed at query time,
// not navigated at storage time.
package triplestore

import (
	"fmt"
	"os/exec"
	"strings"
)

// CreateSchema initializes the triples and prefixes tables with indexes.
// Safe to call repeatedly — uses IF NOT EXISTS throughout.
func CreateSchema(dbPath string) error {
	schema := `.timeout 5000
BEGIN;
CREATE TABLE IF NOT EXISTS triples (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    subject     TEXT NOT NULL,
    predicate   TEXT NOT NULL,
    object      TEXT NOT NULL,
    object_type TEXT NOT NULL DEFAULT 'literal',
    datatype    TEXT,
    graph       TEXT NOT NULL DEFAULT 'default',
    temporal    TEXT NOT NULL DEFAULT 'static',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now', 'localtime')),
    valid_until TEXT
);

CREATE TABLE IF NOT EXISTS prefixes (
    prefix TEXT PRIMARY KEY,
    uri    TEXT NOT NULL UNIQUE
);

-- Primary lookup patterns
CREATE INDEX IF NOT EXISTS idx_triple_spo ON triples (subject, predicate, object);
CREATE INDEX IF NOT EXISTS idx_triple_sp  ON triples (subject, predicate);
CREATE INDEX IF NOT EXISTS idx_triple_po  ON triples (predicate, object);
CREATE INDEX IF NOT EXISTS idx_triple_os  ON triples (object, subject) WHERE object_type = 'uri';

-- Temporal partitioning
CREATE INDEX IF NOT EXISTS idx_triple_temporal ON triples (temporal, created_at);
CREATE INDEX IF NOT EXISTS idx_triple_graph ON triples (graph);

-- Fast rdf:type queries
CREATE INDEX IF NOT EXISTS idx_triple_type ON triples (object) WHERE predicate = 'rdf:type';

-- Current-state filter for dynamic triples
CREATE INDEX IF NOT EXISTS idx_triple_current ON triples (subject, predicate) WHERE valid_until IS NULL;

-- Seed namespace prefixes
INSERT OR IGNORE INTO prefixes (prefix, uri) VALUES
    ('rdf',       'http://www.w3.org/1999/02/22-rdf-syntax-ns#'),
    ('schema',    'https://schema.org/'),
    ('skos',      'http://www.w3.org/2004/02/skos/core#'),
    ('sosa',      'http://www.w3.org/ns/sosa/'),
    ('prov',      'http://www.w3.org/ns/prov#'),
    ('as',        'https://www.w3.org/ns/activitystreams#'),
    ('sh',        'http://www.w3.org/ns/shacl#'),
    ('dcterms',   'http://purl.org/dc/terms/'),
    ('xsd',       'http://www.w3.org/2001/XMLSchema#'),
    ('mesh',      'https://safety-quotient.dev/ns/mesh/'),
    ('agent',     'https://safety-quotient.dev/ns/agent/'),
    ('transport', 'https://safety-quotient.dev/ns/transport/'),
    ('vocab',     'https://psychology-agent.safety-quotient.dev/vocab/');
COMMIT;`
	return execPiped(dbPath, schema)
}

// execPiped runs SQL through sqlite3 via stdin pipe rather than command-line
// argument. Required because sqlite3 dot-commands (.timeout) only work
// when reading from stdin, not from CLI arguments.
func execPiped(dbPath, sql string) error {
	cmd := exec.Command("sqlite3", dbPath)
	cmd.Stdin = strings.NewReader(sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sqlite3: %w: %s", err, string(out))
	}
	return nil
}
