// Package triplestore — sparql.go provides a lightweight SPARQL subset
// parser and SQL translator for the SQLite triple store.
//
// Supports: SELECT, WHERE (triple patterns with ?variables), FILTER
// (basic comparisons), ORDER BY, LIMIT, prefix declarations.
// Does NOT support: OPTIONAL, UNION, subqueries, property paths,
// aggregates (COUNT/SUM/AVG), CONSTRUCT, DESCRIBE, ASK.
//
// Design: translates SPARQL → SQL JOINs on the triples table.
// Each triple pattern in WHERE becomes a self-join. Variables bind
// across patterns via shared column references.
package triplestore

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/safety-quotient-lab/meshd/internal/db"
)

// SparqlQuery represents a parsed SPARQL SELECT query.
type SparqlQuery struct {
	Prefixes   map[string]string // prefix → URI
	SelectVars []string          // ?var names to return
	Patterns   []TriplePattern   // WHERE clause patterns
	Filters    []string          // FILTER expressions (raw SQL conditions)
	OrderBy    string            // ORDER BY column
	OrderDesc  bool
	Limit      int
	Graph      string // FROM <graph> — optional named graph filter
}

// TriplePattern represents a single triple pattern in a WHERE clause.
type TriplePattern struct {
	Subject   string // ?var or URI
	Predicate string // ?var or URI
	Object    string // ?var or literal or URI
}

// SparqlResult holds the query output.
type SparqlResult struct {
	Variables []string              `json:"head"`
	Bindings  []map[string]string   `json:"results"`
	SQL       string                `json:"sql,omitempty"`
	Error     string                `json:"error,omitempty"`
}

var (
	prefixRe  = regexp.MustCompile(`(?i)PREFIX\s+(\w+):\s*<([^>]+)>`)
	selectRe  = regexp.MustCompile(`(?i)SELECT\s+((?:\?\w+\s*)+|\*)`)
	whereRe   = regexp.MustCompile(`(?is)WHERE\s*\{(.*?)\}`)
	patternRe = regexp.MustCompile(`(\?\w+|<[^>]+>|[\w]+:[\w/.-]+|"[^"]*"(?:\^\^<[^>]+>)?)\s+(\?\w+|<[^>]+>|[\w]+:[\w/.-]+)\s+(\?\w+|<[^>]+>|[\w]+:[\w/.-]+|"[^"]*"(?:\^\^<[^>]+>)?)\s*\.`)
	filterRe  = regexp.MustCompile(`(?i)FILTER\s*\(([^)]+)\)`)
	limitRe   = regexp.MustCompile(`(?i)LIMIT\s+(\d+)`)
	orderRe   = regexp.MustCompile(`(?i)ORDER\s+BY\s+(DESC\s*\()?\s*(\?\w+)\s*\)?`)
	fromRe    = regexp.MustCompile(`(?i)FROM\s*<([^>]+)>`)
)

// ParseSparql parses a SPARQL SELECT query string into a SparqlQuery.
func ParseSparql(query string) (*SparqlQuery, error) {
	q := &SparqlQuery{
		Prefixes: make(map[string]string),
	}

	// Default prefixes (match ontology.jsonld)
	q.Prefixes["rdf"] = "rdf:"
	q.Prefixes["schema"] = "schema:"
	q.Prefixes["mesh"] = "mesh:"
	q.Prefixes["sosa"] = "sosa:"
	q.Prefixes["prov"] = "prov:"
	q.Prefixes["as"] = "as:"
	q.Prefixes["skos"] = "skos:"
	q.Prefixes["agent"] = "agent:"
	q.Prefixes["transport"] = "transport:"
	q.Prefixes["vocab"] = "vocab:"

	// Parse PREFIX declarations (override defaults)
	for _, m := range prefixRe.FindAllStringSubmatch(query, -1) {
		q.Prefixes[m[1]] = m[2]
	}

	// Parse SELECT variables
	selMatch := selectRe.FindStringSubmatch(query)
	if selMatch == nil {
		return nil, fmt.Errorf("no SELECT clause found")
	}
	if strings.TrimSpace(selMatch[1]) == "*" {
		q.SelectVars = []string{"*"}
	} else {
		for _, v := range strings.Fields(selMatch[1]) {
			if strings.HasPrefix(v, "?") {
				q.SelectVars = append(q.SelectVars, v)
			}
		}
	}

	// Parse FROM <graph>
	if fromMatch := fromRe.FindStringSubmatch(query); fromMatch != nil {
		q.Graph = fromMatch[1]
	}

	// Parse WHERE { ... }
	whereMatch := whereRe.FindStringSubmatch(query)
	if whereMatch == nil {
		return nil, fmt.Errorf("no WHERE clause found")
	}
	body := whereMatch[1]

	// Extract FILTER expressions from within WHERE
	for _, fm := range filterRe.FindAllStringSubmatch(body, -1) {
		q.Filters = append(q.Filters, fm[1])
	}
	// Remove FILTERs before parsing triple patterns
	cleanBody := filterRe.ReplaceAllString(body, "")

	// Parse triple patterns
	for _, pm := range patternRe.FindAllStringSubmatch(cleanBody, -1) {
		q.Patterns = append(q.Patterns, TriplePattern{
			Subject:   pm[1],
			Predicate: pm[2],
			Object:    pm[3],
		})
	}

	if len(q.Patterns) == 0 {
		return nil, fmt.Errorf("no triple patterns found in WHERE clause")
	}

	// Parse ORDER BY
	if orderMatch := orderRe.FindStringSubmatch(query); orderMatch != nil {
		q.OrderBy = orderMatch[2]
		q.OrderDesc = strings.TrimSpace(orderMatch[1]) != ""
	}

	// Parse LIMIT
	if limitMatch := limitRe.FindStringSubmatch(query); limitMatch != nil {
		q.Limit, _ = strconv.Atoi(limitMatch[1])
	}

	return q, nil
}

// ToSQL translates the parsed SPARQL query to a SQL query against the triples table.
func (q *SparqlQuery) ToSQL() (string, error) {
	// Each triple pattern becomes a self-join on the triples table.
	// Pattern 0 → t0, Pattern 1 → t1, etc.
	// Variables that appear in multiple patterns create JOIN conditions.

	varBindings := make(map[string]string) // ?var → "tN.column"
	var fromClauses []string
	var whereClauses []string

	for i, pat := range q.Patterns {
		alias := fmt.Sprintf("t%d", i)
		fromClauses = append(fromClauses, fmt.Sprintf("triples %s", alias))

		// Current-only filter
		whereClauses = append(whereClauses, fmt.Sprintf("%s.valid_until IS NULL", alias))

		// Graph filter
		if q.Graph != "" {
			whereClauses = append(whereClauses, fmt.Sprintf("%s.graph = '%s'", alias, escapeSql(q.Graph)))
		}

		// Subject
		whereClauses = append(whereClauses, bindTerm(pat.Subject, alias, "subject", varBindings)...)
		// Predicate
		whereClauses = append(whereClauses, bindTerm(pat.Predicate, alias, "predicate", varBindings)...)
		// Object
		whereClauses = append(whereClauses, bindTerm(pat.Object, alias, "object", varBindings)...)
	}

	// Build SELECT columns from variable bindings
	var selectCols []string
	if len(q.SelectVars) == 1 && q.SelectVars[0] == "*" {
		// SELECT * — return all bound variables
		for varName, col := range varBindings {
			selectCols = append(selectCols, fmt.Sprintf("%s AS '%s'", col, varName))
		}
	} else {
		for _, v := range q.SelectVars {
			col, ok := varBindings[v]
			if !ok {
				return "", fmt.Errorf("variable %s not bound in WHERE clause", v)
			}
			selectCols = append(selectCols, fmt.Sprintf("%s AS '%s'", col, v))
		}
	}

	if len(selectCols) == 0 {
		return "", fmt.Errorf("no columns to select")
	}

	// Translate FILTER expressions
	for _, filter := range q.Filters {
		sqlFilter := translateFilter(filter, varBindings)
		if sqlFilter != "" {
			whereClauses = append(whereClauses, sqlFilter)
		}
	}

	sql := fmt.Sprintf("SELECT DISTINCT %s FROM %s WHERE %s",
		strings.Join(selectCols, ", "),
		strings.Join(fromClauses, ", "),
		strings.Join(whereClauses, " AND "),
	)

	// ORDER BY
	if q.OrderBy != "" {
		if col, ok := varBindings[q.OrderBy]; ok {
			direction := "ASC"
			if q.OrderDesc {
				direction = "DESC"
			}
			sql += fmt.Sprintf(" ORDER BY %s %s", col, direction)
		}
	}

	// LIMIT
	if q.Limit > 0 {
		sql += fmt.Sprintf(" LIMIT %d", q.Limit)
	} else {
		sql += " LIMIT 200" // safety cap
	}

	return sql + ";", nil
}

// bindTerm processes a single term (subject, predicate, or object) from a triple pattern.
// Returns WHERE clause conditions and updates variable bindings.
func bindTerm(term, alias, column string, bindings map[string]string) []string {
	var conditions []string

	if strings.HasPrefix(term, "?") {
		// Variable — check if already bound
		if existing, ok := bindings[term]; ok {
			// Join condition: this column must equal the previously bound column
			conditions = append(conditions, fmt.Sprintf("%s.%s = %s", alias, column, existing))
		} else {
			// First occurrence — record binding
			bindings[term] = fmt.Sprintf("%s.%s", alias, column)
		}
	} else {
		// Concrete value — equality condition
		value := resolveValue(term)
		conditions = append(conditions, fmt.Sprintf("%s.%s = '%s'", alias, column, escapeSql(value)))
	}

	return conditions
}

// resolveValue strips angle brackets from URIs and quotes from literals.
func resolveValue(term string) string {
	// <uri> → uri
	if strings.HasPrefix(term, "<") && strings.HasSuffix(term, ">") {
		return term[1 : len(term)-1]
	}
	// "literal"^^<datatype> → literal
	if strings.HasPrefix(term, `"`) {
		end := strings.Index(term[1:], `"`)
		if end >= 0 {
			return term[1 : end+1]
		}
	}
	// prefix:local — already in compact form, matches our stored predicates
	return term
}

// translateFilter converts a SPARQL FILTER expression to SQL.
// Supports basic comparisons: ?var > value, ?var < value, ?var = value.
func translateFilter(expr string, bindings map[string]string) string {
	expr = strings.TrimSpace(expr)

	// Replace ?variables with their SQL column references
	for varName, col := range bindings {
		expr = strings.ReplaceAll(expr, varName, col)
	}

	// Replace SPARQL string comparison operators
	expr = strings.ReplaceAll(expr, "&&", "AND")
	expr = strings.ReplaceAll(expr, "||", "OR")

	// Handle STRSTARTS(?var, "prefix") → column LIKE 'prefix%'
	strStartsRe := regexp.MustCompile(`(?i)STRSTARTS\(([^,]+),\s*"([^"]+)"\)`)
	expr = strStartsRe.ReplaceAllString(expr, "$1 LIKE '$2%'")

	// Handle STR(?var) — just use the column directly
	strRe := regexp.MustCompile(`(?i)STR\(([^)]+)\)`)
	expr = strRe.ReplaceAllString(expr, "$1")

	return expr
}

func escapeSql(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// ExecuteSparql parses and executes a SPARQL query against the store.
func (s *Store) ExecuteSparql(query string) SparqlResult {
	parsed, err := ParseSparql(query)
	if err != nil {
		return SparqlResult{Error: fmt.Sprintf("parse error: %v", err)}
	}

	sql, err := parsed.ToSQL()
	if err != nil {
		return SparqlResult{Error: fmt.Sprintf("translation error: %v", err)}
	}

	rows, err := db.QueryJSON(s.DBPath, sql)
	if err != nil {
		return SparqlResult{Error: fmt.Sprintf("execution error: %v", err), SQL: sql}
	}

	result := SparqlResult{
		Variables: parsed.SelectVars,
		Bindings:  rows,
		SQL:       sql,
	}

	return result
}

