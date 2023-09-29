// Copyright 2023 canonical.

package names

import cofga "github.com/canonical/ofga"

var (
	AllRelations = allRelations
)

func NewTag(id, kind, relation string) *Tag {
	return &Tag{
		ID:       id,
		Relation: cofga.Relation(relation),
		Kind:     cofga.Kind(kind),
	}
}
