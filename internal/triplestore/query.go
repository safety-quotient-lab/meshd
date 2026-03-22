package triplestore

import (
	"fmt"
	"strings"

	"github.com/safety-quotient-lab/meshd/internal/db"
)

// QueryResult holds triples returned from a query.
type QueryResult struct {
	Triples []Triple
	Count   int
}

// QueryCurrent returns all current (non-superseded) triples matching the
// given filters. Empty string filters match any value.
func (s *Store) QueryCurrent(subject, predicate, object, graph string) (QueryResult, error) {
	return s.query(subject, predicate, object, graph, true, 0)
}

// QueryHistory returns all triples (including superseded) matching filters.
// Ordered by created_at descending.
func (s *Store) QueryHistory(subject, predicate, graph string, limit int) (QueryResult, error) {
	return s.query(subject, predicate, "", graph, false, limit)
}

// QueryGraph returns all current triples in a named graph.
func (s *Store) QueryGraph(graph string) (QueryResult, error) {
	return s.query("", "", "", graph, true, 0)
}

// CountByGraph returns triple counts per named graph (current only).
func (s *Store) CountByGraph() (map[string]int, error) {
	query := ".timeout 5000\nSELECT graph, COUNT(*) as cnt FROM triples WHERE valid_until IS NULL GROUP BY graph ORDER BY cnt DESC;"
	rows, err := db.QueryJSON(s.DBPath, query)
	if err != nil {
		return nil, err
	}
	result := make(map[string]int, len(rows))
	for _, row := range rows {
		var cnt int
		fmt.Sscanf(row["cnt"], "%d", &cnt)
		result[row["graph"]] = cnt
	}
	return result, nil
}

// CountTotal returns the total number of current triples.
func (s *Store) CountTotal() int {
	return db.QueryScalar(s.DBPath, "SELECT COUNT(*) FROM triples WHERE valid_until IS NULL;")
}

func (s *Store) query(subject, predicate, object, graph string, currentOnly bool, limit int) (QueryResult, error) {
	var conditions []string

	if subject != "" {
		conditions = append(conditions, fmt.Sprintf("subject = '%s'", db.EscapeString(subject)))
	}
	if predicate != "" {
		conditions = append(conditions, fmt.Sprintf("predicate = '%s'", db.EscapeString(predicate)))
	}
	if object != "" {
		conditions = append(conditions, fmt.Sprintf("object = '%s'", db.EscapeString(object)))
	}
	if graph != "" {
		conditions = append(conditions, fmt.Sprintf("graph = '%s'", db.EscapeString(graph)))
	}
	if currentOnly {
		conditions = append(conditions, "valid_until IS NULL")
	}

	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}

	query := ".timeout 5000\nSELECT subject, predicate, object, object_type, datatype, graph, temporal, created_at, COALESCE(valid_until, '') as valid_until FROM triples" + where + " ORDER BY created_at DESC"

	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	query += ";"

	rows, err := db.QueryJSON(s.DBPath, query)
	if err != nil {
		return QueryResult{}, err
	}

	triples := make([]Triple, 0, len(rows))
	for _, row := range rows {
		triples = append(triples, Triple{
			Subject:    row["subject"],
			Predicate:  row["predicate"],
			Object:     row["object"],
			ObjectType: row["object_type"],
			Datatype:   row["datatype"],
			Graph:      row["graph"],
			Temporal:   row["temporal"],
			CreatedAt:  row["created_at"],
			ValidUntil: row["valid_until"],
		})
	}

	return QueryResult{Triples: triples, Count: len(triples)}, nil
}
