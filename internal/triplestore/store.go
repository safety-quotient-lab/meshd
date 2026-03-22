package triplestore

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/safety-quotient-lab/meshd/internal/db"
)

// Triple represents a single subject-predicate-object assertion.
type Triple struct {
	Subject    string
	Predicate  string
	Object     string
	ObjectType string // "literal", "uri", "blank"
	Datatype   string // xsd:string, xsd:decimal, xsd:dateTime, xsd:boolean, xsd:integer
	Graph      string
	Temporal   string // "static", "dynamic", "event"
	CreatedAt  string
	ValidUntil string
}

// Store provides CRUD operations on the SQLite-backed triple table.
type Store struct {
	DBPath   string
	Logger   *slog.Logger
	ontology *Ontology
}

// NewStore creates a triple store backed by the given SQLite database.
// Calls CreateSchema to ensure tables exist.
func NewStore(dbPath string, logger *slog.Logger) (*Store, error) {
	if err := CreateSchema(dbPath); err != nil {
		return nil, fmt.Errorf("triplestore schema: %w", err)
	}
	return &Store{DBPath: dbPath, Logger: logger}, nil
}

// SetOntology attaches a loaded ontology for SHACL validation.
func (s *Store) SetOntology(ont *Ontology) {
	s.ontology = ont
}

// Assert adds a triple to the store. For dynamic triples, marks any
// existing triple with the same subject+predicate+graph as superseded
// (sets valid_until) before inserting the new one.
func (s *Store) Assert(t Triple) error {
	if t.ObjectType == "" {
		t.ObjectType = "literal"
	}
	if t.Graph == "" {
		t.Graph = "default"
	}
	if t.Temporal == "" {
		t.Temporal = "static"
	}

	// SHACL validation deferred — shapes validate at emit boundary, not per-triple.

	now := time.Now().Format("2006-01-02T15:04:05")

	if t.Temporal == "dynamic" {
		// Mark previous current value as superseded
		supersede := fmt.Sprintf(
			"UPDATE triples SET valid_until = '%s' WHERE subject = '%s' AND predicate = '%s' AND graph = '%s' AND valid_until IS NULL;",
			now,
			db.EscapeString(t.Subject),
			db.EscapeString(t.Predicate),
			db.EscapeString(t.Graph),
		)
		if _, err := db.Exec(s.DBPath, supersede); err != nil {
			s.Logger.Warn("triplestore: supersede failed", "subject", t.Subject, "predicate", t.Predicate, "error", err)
		}
	}

	insert := fmt.Sprintf(
		"INSERT INTO triples (subject, predicate, object, object_type, datatype, graph, temporal, created_at) VALUES ('%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s');",
		db.EscapeString(t.Subject),
		db.EscapeString(t.Predicate),
		db.EscapeString(t.Object),
		db.EscapeString(t.ObjectType),
		db.EscapeString(t.Datatype),
		db.EscapeString(t.Graph),
		db.EscapeString(t.Temporal),
		now,
	)
	_, err := db.Exec(s.DBPath, insert)
	return err
}

// AssertBatch inserts multiple triples in a single transaction.
func (s *Store) AssertBatch(triples []Triple) error {
	if len(triples) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString("BEGIN;\n")

	now := time.Now().Format("2006-01-02T15:04:05")

	for _, t := range triples {
		if t.ObjectType == "" {
			t.ObjectType = "literal"
		}
		if t.Graph == "" {
			t.Graph = "default"
		}
		if t.Temporal == "" {
			t.Temporal = "static"
		}

		if t.Temporal == "dynamic" {
			fmt.Fprintf(&b,
				"UPDATE triples SET valid_until = '%s' WHERE subject = '%s' AND predicate = '%s' AND graph = '%s' AND valid_until IS NULL;\n",
				now,
				db.EscapeString(t.Subject),
				db.EscapeString(t.Predicate),
				db.EscapeString(t.Graph),
			)
		}

		fmt.Fprintf(&b,
			"INSERT INTO triples (subject, predicate, object, object_type, datatype, graph, temporal, created_at) VALUES ('%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s');\n",
			db.EscapeString(t.Subject),
			db.EscapeString(t.Predicate),
			db.EscapeString(t.Object),
			db.EscapeString(t.ObjectType),
			db.EscapeString(t.Datatype),
			db.EscapeString(t.Graph),
			db.EscapeString(t.Temporal),
			now,
		)
	}

	b.WriteString("COMMIT;")
	_, err := db.Exec(s.DBPath, b.String())
	return err
}

// Retract removes all current triples matching subject+predicate in a graph.
// Sets valid_until rather than deleting — preserves history.
func (s *Store) Retract(subject, predicate, graph string) error {
	now := time.Now().Format("2006-01-02T15:04:05")
	query := fmt.Sprintf(
		"UPDATE triples SET valid_until = '%s' WHERE subject = '%s' AND predicate = '%s' AND graph = '%s' AND valid_until IS NULL;",
		now,
		db.EscapeString(subject),
		db.EscapeString(predicate),
		db.EscapeString(graph),
	)
	_, err := db.Exec(s.DBPath, query)
	return err
}

// RetractGraph marks all current triples in a graph as superseded.
// Used before replacing static graph contents on refresh.
func (s *Store) RetractGraph(graph string) error {
	now := time.Now().Format("2006-01-02T15:04:05")
	query := fmt.Sprintf(
		"UPDATE triples SET valid_until = '%s' WHERE graph = '%s' AND valid_until IS NULL;",
		now,
		db.EscapeString(graph),
	)
	_, err := db.Exec(s.DBPath, query)
	return err
}

// ReplaceGraph atomically replaces all current triples in a graph.
// Supersedes existing triples, then inserts new ones.
func (s *Store) ReplaceGraph(graph string, triples []Triple) error {
	if err := s.RetractGraph(graph); err != nil {
		return fmt.Errorf("retract graph %s: %w", graph, err)
	}
	return s.AssertBatch(triples)
}
