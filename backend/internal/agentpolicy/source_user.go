package agentpolicy

import (
	"context"
	"encoding/json"
	"time"
)

// UserStore persists the user layer, one JSON document in app settings.
type UserStore interface {
	AgentPolicy(ctx context.Context) ([]byte, error)
	SetAgentPolicy(ctx context.Context, data []byte, now time.Time) error
}

// UserDocVersion is the version of the stored user document.
const UserDocVersion = 2

// userDoc is the stored document: global settings and per-project overrides,
// both nested rule -> field -> value. Version 1 was the flat shape
// ({"maxPermission": "auto"}); it is read through each field's LegacyKey and
// rewritten as version 2 on the next write.
type userDoc struct {
	Version  int                            `json:"version"`
	Rules    map[string]Settings            `json:"rules,omitempty"`
	Projects map[string]map[string]Settings `json:"projects,omitempty"`
}

func newUserDoc() userDoc {
	return userDoc{Version: UserDocVersion, Rules: map[string]Settings{}, Projects: map[string]map[string]Settings{}}
}

// parseUserDoc reads either document version. Values are not validated here
// (the stored document was validated on write); readLayer drops invalid ones.
func parseUserDoc(rules []Rule, data []byte) userDoc {
	doc := newUserDoc()
	if len(data) == 0 {
		return doc
	}
	var probe map[string]json.RawMessage
	if json.Unmarshal(data, &probe) != nil {
		return doc
	}
	if _, v2 := probe["version"]; v2 {
		var parsed userDoc
		if json.Unmarshal(data, &parsed) == nil {
			if parsed.Rules != nil {
				doc.Rules = parsed.Rules
			}
			if parsed.Projects != nil {
				doc.Projects = parsed.Projects
			}
		}
		return doc
	}
	for _, r := range rules {
		for _, f := range r.Schema().Fields {
			if raw, ok := probe[f.LegacyKey]; ok && f.LegacyKey != "" {
				setValue(values(doc.Rules), r.Name(), f.Name, raw)
			}
		}
	}
	return doc
}

// layerFor extracts the validated layer for a project scope: project=="" is the
// global user layer.
func (d userDoc) layerFor(rules []Rule, project string) layer {
	source := d.Rules
	if project != "" {
		source = d.Projects[project]
	}
	l := layer{values: values{}}
	for _, r := range rules {
		for _, f := range r.Schema().Fields {
			if f.ManagedOnly {
				continue
			}
			if raw, ok := source[r.Name()][f.Name]; ok {
				if value, err := f.Parse(raw); err == nil {
					setValue(l.values, r.Name(), f.Name, value)
				}
			}
		}
	}
	return l
}

func (d userDoc) marshal() ([]byte, error) {
	d.Version = UserDocVersion
	return json.Marshal(d)
}
