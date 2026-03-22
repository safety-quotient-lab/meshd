package triplestore

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
)

// Ontology holds the parsed ontology definition loaded from ontology.jsonld.
// Provides SHACL shape validation and predicate metadata lookup.
type Ontology struct {
	mu         sync.RWMutex
	Context    map[string]string        // prefix → URI
	Properties []OntologyProperty       // custom mesh: properties
	Shapes     []Shape                  // SHACL node shapes
	Raw        map[string]any           // full parsed JSON-LD
	logger     *slog.Logger
}

// OntologyProperty describes a predicate defined in the ontology.
type OntologyProperty struct {
	ID            string   // e.g. "mesh:sessionState"
	Name          string
	Description   string
	DomainIncludes string
	RangeIncludes  string
	TemporalClass  string
	ValidValues    []string // for enum-constrained properties
}

// Shape describes a SHACL NodeShape for validation.
type Shape struct {
	ID          string
	TargetClass string
	Properties  []ShapeProperty
}

// ShapeProperty describes a single property constraint within a shape.
type ShapeProperty struct {
	Path     string
	Name     string
	MinCount int
	MaxCount int // 0 means unconstrained
	Datatype string
	NodeKind string // "sh:IRI", "sh:Literal", "sh:BlankNode"
	In       []string // allowed values (sh:in)
}

// LoadOntology reads and parses an ontology.jsonld file.
func LoadOntology(path string, logger *slog.Logger) (*Ontology, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read ontology: %w", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse ontology JSON: %w", err)
	}

	ont := &Ontology{Raw: raw, logger: logger}

	// Parse @context
	ont.Context = parseContext(raw)

	// Parse @graph entries
	graph, ok := raw["@graph"].([]any)
	if !ok {
		return ont, nil // valid but empty ontology
	}

	for _, entry := range graph {
		node, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		nodeType := stringVal(node, "@type")

		switch nodeType {
		case "rdf:Property":
			ont.Properties = append(ont.Properties, parseProperty(node))
		case "sh:NodeShape":
			ont.Shapes = append(ont.Shapes, parseShape(node))
		}
	}

	logger.Info("ontology loaded",
		"properties", len(ont.Properties),
		"shapes", len(ont.Shapes),
		"prefixes", len(ont.Context),
	)

	return ont, nil
}

// Reload re-reads the ontology file. Thread-safe.
func (o *Ontology) Reload(path string) error {
	fresh, err := LoadOntology(path, o.logger)
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.Context = fresh.Context
	o.Properties = fresh.Properties
	o.Shapes = fresh.Shapes
	o.Raw = fresh.Raw
	return nil
}

// ValidateAgainst checks whether a set of triples for a given rdf:type
// satisfies the corresponding SHACL shape. Returns nil if valid or no
// shape matches. Returns an error describing the first violation found.
func (o *Ontology) ValidateAgainst(rdfType string, triples []Triple) error {
	o.mu.RLock()
	defer o.mu.RUnlock()

	for _, shape := range o.Shapes {
		if shape.TargetClass != rdfType {
			continue
		}
		return validateShape(shape, triples)
	}
	return nil // no shape defined for this type — pass
}

func validateShape(shape Shape, triples []Triple) error {
	predicateCount := make(map[string]int)
	for _, t := range triples {
		predicateCount[t.Predicate]++
	}

	for _, prop := range shape.Properties {
		count := predicateCount[prop.Path]
		if prop.MinCount > 0 && count < prop.MinCount {
			return fmt.Errorf("SHACL violation [%s]: %s requires minCount %d, found %d",
				shape.ID, prop.Path, prop.MinCount, count)
		}
		if prop.MaxCount > 0 && count > prop.MaxCount {
			return fmt.Errorf("SHACL violation [%s]: %s allows maxCount %d, found %d",
				shape.ID, prop.Path, prop.MaxCount, count)
		}
	}
	return nil
}

// --- parsing helpers ---

func parseContext(raw map[string]any) map[string]string {
	ctx, ok := raw["@context"].(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]string, len(ctx))
	for k, v := range ctx {
		if s, ok := v.(string); ok {
			result[k] = s
		}
	}
	return result
}

func parseProperty(node map[string]any) OntologyProperty {
	prop := OntologyProperty{
		ID:             stringVal(node, "@id"),
		Name:           stringVal(node, "schema:name"),
		Description:    stringVal(node, "schema:description"),
		DomainIncludes: stringVal(node, "schema:domainIncludes"),
		RangeIncludes:  stringVal(node, "schema:rangeIncludes"),
		TemporalClass:  stringVal(node, "mesh:temporalClass"),
	}

	if vals, ok := node["mesh:validValues"].([]any); ok {
		for _, v := range vals {
			if s, ok := v.(string); ok {
				prop.ValidValues = append(prop.ValidValues, s)
			}
		}
	}
	return prop
}

func parseShape(node map[string]any) Shape {
	shape := Shape{
		ID:          stringVal(node, "@id"),
		TargetClass: extractID(node["sh:targetClass"]),
	}

	props, ok := node["sh:property"].([]any)
	if !ok {
		return shape
	}

	for _, p := range props {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		sp := ShapeProperty{
			Path:     extractID(pm["sh:path"]),
			Name:     stringVal(pm, "sh:name"),
			MinCount: intVal(pm, "sh:minCount"),
			MaxCount: intVal(pm, "sh:maxCount"),
			Datatype: extractID(pm["sh:datatype"]),
			NodeKind: extractID(pm["sh:nodeKind"]),
		}

		// Parse sh:in list
		if inList, ok := pm["sh:in"].(map[string]any); ok {
			if list, ok := inList["@list"].([]any); ok {
				for _, v := range list {
					if s, ok := v.(string); ok {
						sp.In = append(sp.In, s)
					}
				}
			}
		}

		shape.Properties = append(shape.Properties, sp)
	}
	return shape
}

func stringVal(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func extractID(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case map[string]any:
		if id, ok := val["@id"].(string); ok {
			return id
		}
	}
	return ""
}

func intVal(m map[string]any, key string) int {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	}
	return 0
}
