package schema

import (
	"regexp"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// Organization holds the schema definition for the Organization entity.
type Organization struct {
	ent.Schema
}

// Fields of the Organization.
func (Organization) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			Match(regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)).
			MaxLen(63).
			Immutable(),
		field.String("display_name").
			MaxLen(100),
		field.String("description").
			Default("").
			MaxLen(500),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Edges of the Organization.
func (Organization) Edges() []ent.Edge {
	return nil
}
