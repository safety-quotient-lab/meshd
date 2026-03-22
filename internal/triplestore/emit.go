package triplestore

import (
	"fmt"
	"time"
)

// EmitAgent produces static triples for an agent from registry data.
// Returns triples for the agent-registry graph.
func EmitAgent(agentID, name, version, role, cardURL, statusURL, repoURL string, skillCount int, available bool) []Triple {
	subject := "agent:" + agentID
	availStr := "false"
	if available {
		availStr = "true"
	}

	triples := []Triple{
		{Subject: subject, Predicate: "rdf:type", Object: "schema:SoftwareApplication", ObjectType: "uri", Graph: "agent-registry", Temporal: "static"},
		{Subject: subject, Predicate: "schema:name", Object: name, Datatype: "xsd:string", Graph: "agent-registry", Temporal: "static"},
		{Subject: subject, Predicate: "schema:version", Object: version, Datatype: "xsd:string", Graph: "agent-registry", Temporal: "static"},
		{Subject: subject, Predicate: "mesh:available", Object: availStr, Datatype: "xsd:boolean", Graph: "agent-registry", Temporal: "dynamic"},
	}

	if role != "" {
		triples = append(triples, Triple{
			Subject: subject, Predicate: "schema:roleName", Object: role,
			Datatype: "xsd:string", Graph: "agent-registry", Temporal: "static",
		})
	}
	if cardURL != "" {
		triples = append(triples, Triple{
			Subject: subject, Predicate: "schema:url", Object: cardURL,
			ObjectType: "uri", Graph: "agent-registry", Temporal: "static",
		})
	}
	if statusURL != "" {
		// EntryPoint for status API
		entryID := subject + "/status"
		triples = append(triples, Triple{
			Subject: entryID, Predicate: "rdf:type", Object: "schema:EntryPoint",
			ObjectType: "uri", Graph: "agent-registry", Temporal: "static",
		})
		triples = append(triples, Triple{
			Subject: entryID, Predicate: "schema:urlTemplate", Object: statusURL,
			ObjectType: "uri", Graph: "agent-registry", Temporal: "static",
		})
		triples = append(triples, Triple{
			Subject: subject, Predicate: "schema:potentialAction", Object: entryID,
			ObjectType: "uri", Graph: "agent-registry", Temporal: "static",
		})
	}
	if repoURL != "" {
		triples = append(triples, Triple{
			Subject: subject, Predicate: "schema:codeRepository", Object: repoURL,
			ObjectType: "uri", Graph: "agent-registry", Temporal: "static",
		})
	}
	if skillCount > 0 {
		triples = append(triples, Triple{
			Subject: subject, Predicate: "schema:numberOfItems", Object: fmt.Sprintf("%d", skillCount),
			Datatype: "xsd:integer", Graph: "agent-registry", Temporal: "static",
		})
	}

	return triples
}

// EmitObservation produces a sosa:Observation triple set for a measured value.
// Returns triples for the specified graph (typically agent-status or mesh-state).
func EmitObservation(sensorID, observedProperty string, value float64, graph string) []Triple {
	now := time.Now().UTC().Format(time.RFC3339)
	obsID := fmt.Sprintf("_:obs-%s-%s-%d", sensorID, observedProperty, time.Now().UnixMilli())

	return []Triple{
		{Subject: obsID, Predicate: "rdf:type", Object: "sosa:Observation", ObjectType: "uri", Graph: graph, Temporal: "dynamic"},
		{Subject: obsID, Predicate: "sosa:madeBySensor", Object: sensorID, ObjectType: "uri", Graph: graph, Temporal: "dynamic"},
		{Subject: obsID, Predicate: "sosa:observedProperty", Object: observedProperty, ObjectType: "uri", Graph: graph, Temporal: "dynamic"},
		{Subject: obsID, Predicate: "sosa:hasSimpleResult", Object: fmt.Sprintf("%.4f", value), Datatype: "xsd:decimal", Graph: graph, Temporal: "dynamic"},
		{Subject: obsID, Predicate: "sosa:resultTime", Object: now, Datatype: "xsd:dateTime", Graph: graph, Temporal: "dynamic"},
	}
}

// EmitObservationString produces a sosa:Observation for a string-valued result
// (e.g., affect category, flow state).
func EmitObservationString(sensorID, observedProperty, value, graph string) []Triple {
	now := time.Now().UTC().Format(time.RFC3339)
	obsID := fmt.Sprintf("_:obs-%s-%s-%d", sensorID, observedProperty, time.Now().UnixMilli())

	return []Triple{
		{Subject: obsID, Predicate: "rdf:type", Object: "sosa:Observation", ObjectType: "uri", Graph: graph, Temporal: "dynamic"},
		{Subject: obsID, Predicate: "sosa:madeBySensor", Object: sensorID, ObjectType: "uri", Graph: graph, Temporal: "dynamic"},
		{Subject: obsID, Predicate: "sosa:observedProperty", Object: observedProperty, ObjectType: "uri", Graph: graph, Temporal: "dynamic"},
		{Subject: obsID, Predicate: "sosa:hasSimpleResult", Object: value, Datatype: "xsd:string", Graph: graph, Temporal: "dynamic"},
		{Subject: obsID, Predicate: "sosa:resultTime", Object: now, Datatype: "xsd:dateTime", Graph: graph, Temporal: "dynamic"},
	}
}

// EmitMessage produces triples for a transport message.
// Returns triples for the transport graph.
func EmitMessage(messageCID, sender, recipient, subject, sessionName string, turn int, dateSent, messageType, urgency string) []Triple {
	msgSubject := "transport:msg/" + messageCID
	sessionSubject := "transport:session/" + sessionName

	triples := []Triple{
		{Subject: msgSubject, Predicate: "rdf:type", Object: "schema:Message", ObjectType: "uri", Graph: "transport", Temporal: "event"},
		{Subject: msgSubject, Predicate: "schema:sender", Object: "agent:" + sender, ObjectType: "uri", Graph: "transport", Temporal: "event"},
		{Subject: msgSubject, Predicate: "schema:recipient", Object: "agent:" + recipient, ObjectType: "uri", Graph: "transport", Temporal: "event"},
		{Subject: msgSubject, Predicate: "schema:dateSent", Object: dateSent, Datatype: "xsd:dateTime", Graph: "transport", Temporal: "event"},
		{Subject: msgSubject, Predicate: "schema:position", Object: fmt.Sprintf("%d", turn), Datatype: "xsd:integer", Graph: "transport", Temporal: "event"},
		{Subject: msgSubject, Predicate: "schema:isPartOf", Object: sessionSubject, ObjectType: "uri", Graph: "transport", Temporal: "event"},
		{Subject: msgSubject, Predicate: "schema:identifier", Object: messageCID, Datatype: "xsd:string", Graph: "transport", Temporal: "event"},
	}

	if subject != "" {
		triples = append(triples, Triple{
			Subject: msgSubject, Predicate: "schema:about", Object: subject,
			Datatype: "xsd:string", Graph: "transport", Temporal: "event",
		})
	}
	if messageType != "" {
		triples = append(triples, Triple{
			Subject: msgSubject, Predicate: "schema:additionalType", Object: messageType,
			Datatype: "xsd:string", Graph: "transport", Temporal: "event",
		})
	}
	if urgency != "" && urgency != "normal" {
		triples = append(triples, Triple{
			Subject: msgSubject, Predicate: "mesh:urgency", Object: urgency,
			Datatype: "xsd:string", Graph: "transport", Temporal: "event",
		})
	}

	// Ensure session exists as a node
	triples = append(triples, Triple{
		Subject: sessionSubject, Predicate: "rdf:type", Object: "schema:Event",
		ObjectType: "uri", Graph: "transport", Temporal: "event",
	})
	triples = append(triples, Triple{
		Subject: sessionSubject, Predicate: "schema:name", Object: sessionName,
		Datatype: "xsd:string", Graph: "transport", Temporal: "event",
	})
	triples = append(triples, Triple{
		Subject: sessionSubject, Predicate: "schema:participant", Object: "agent:" + sender,
		ObjectType: "uri", Graph: "transport", Temporal: "event",
	})
	triples = append(triples, Triple{
		Subject: sessionSubject, Predicate: "schema:participant", Object: "agent:" + recipient,
		ObjectType: "uri", Graph: "transport", Temporal: "event",
	})

	return triples
}

// EmitMeshState produces triples for mesh-level emergent properties.
// Returns triples for the mesh-state graph.
func EmitMeshState(agentsReporting int, affectCategory string, coherence, intelligence, coordination, immune float64, bottleneckAgentID string) []Triple {
	subject := "mesh:state/current"

	triples := []Triple{
		{Subject: subject, Predicate: "rdf:type", Object: "schema:Dataset", ObjectType: "uri", Graph: "mesh-state", Temporal: "dynamic"},
		{Subject: subject, Predicate: "schema:numberOfItems", Object: fmt.Sprintf("%d", agentsReporting), Datatype: "xsd:integer", Graph: "mesh-state", Temporal: "dynamic"},
		{Subject: subject, Predicate: "mesh:collectiveCoherence", Object: fmt.Sprintf("%.4f", coherence), Datatype: "xsd:decimal", Graph: "mesh-state", Temporal: "dynamic"},
		{Subject: subject, Predicate: "mesh:collectiveIntelligence", Object: fmt.Sprintf("%.4f", intelligence), Datatype: "xsd:decimal", Graph: "mesh-state", Temporal: "dynamic"},
	}

	if affectCategory != "" {
		triples = append(triples, EmitObservationString(subject, "vocab:affect-state", affectCategory, "mesh-state")...)
	}
	if bottleneckAgentID != "" {
		triples = append(triples, Triple{
			Subject: subject, Predicate: "mesh:bottleneckAgent", Object: "agent:" + bottleneckAgentID,
			ObjectType: "uri", Graph: "mesh-state", Temporal: "dynamic",
		})
	}

	return triples
}
