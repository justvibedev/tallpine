package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type generator struct {
	version string
	defs    map[string]*schema // merged definitions, by name
	methods []method
}

// unionFiles map a published union file to the direction it describes.
var unionFiles = map[string]string{
	"ClientRequest.json":      "ClientRequest",
	"ClientNotification.json": "ClientNotification",
	"ServerRequest.json":      "ServerRequest",
	"ServerNotification.json": "ServerNotification",
}

func load(dir string) (*generator, error) {
	g := &generator{defs: map[string]*schema{}}

	// The v2 bundle carries every type including the response shapes, which the
	// per-direction union files omit.
	bundle := filepath.Join(dir, "codex_app_server_protocol.v2.schemas.json")
	if err := g.mergeDefs(bundle); err != nil {
		return nil, err
	}

	names := make([]string, 0, len(unionFiles))
	for f := range unionFiles {
		names = append(names, f)
	}
	sort.Strings(names)

	for _, file := range names {
		path := filepath.Join(dir, file)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if err := g.mergeDefs(path); err != nil {
			return nil, err
		}
		if err := g.collectMethods(path, unionFiles[file]); err != nil {
			return nil, err
		}
	}
	if len(g.methods) == 0 {
		return nil, fmt.Errorf("no methods found in %s; is this a codex schema directory?", dir)
	}
	sort.Slice(g.methods, func(i, j int) bool {
		if g.methods[i].Direction != g.methods[j].Direction {
			return g.methods[i].Direction < g.methods[j].Direction
		}
		return g.methods[i].Name < g.methods[j].Name
	})
	return g, nil
}

// mergeDefs folds one file's definitions into the shared namespace. The union
// files repeat types the bundle already declares; a genuine disagreement is a
// generator-stopping error rather than a silently chosen winner.
func (g *generator) mergeDefs(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	var doc schema
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	for name, def := range doc.Definitions {
		existing, ok := g.defs[name]
		if !ok {
			g.defs[name] = def
			continue
		}
		if !sameShape(existing, def) {
			return fmt.Errorf("definition %q differs between files (%s); the generator cannot pick a winner",
				name, filepath.Base(path))
		}
	}
	return nil
}

// sameShape compares what the generator reads and ignores what it only echoes.
// The bundle carries `title` and `$schema` that the per-direction files omit, so
// comparing documentation would report a conflict on every shared definition.
func sameShape(a, b *schema) bool {
	ja, err1 := json.Marshal(stripDocs(a))
	jb, err2 := json.Marshal(stripDocs(b))
	return err1 == nil && err2 == nil && bytes.Equal(ja, jb)
}

func stripDocs(s *schema) *schema {
	if s == nil {
		return nil
	}
	out := *s
	out.Title = ""
	out.Description = ""
	if s.Properties != nil {
		out.Properties = make(map[string]*schema, len(s.Properties))
		for k, v := range s.Properties {
			out.Properties[k] = stripDocs(v)
		}
	}
	if s.Definitions != nil {
		out.Definitions = make(map[string]*schema, len(s.Definitions))
		for k, v := range s.Definitions {
			out.Definitions[k] = stripDocs(v)
		}
	}
	out.Items = stripDocs(s.Items)
	for _, list := range []struct {
		src *[]*schema
		dst *[]*schema
	}{{&s.OneOf, &out.OneOf}, {&s.AnyOf, &out.AnyOf}, {&s.AllOf, &out.AllOf}} {
		if *list.src == nil {
			continue
		}
		copied := make([]*schema, len(*list.src))
		for i, v := range *list.src {
			copied[i] = stripDocs(v)
		}
		*list.dst = copied
	}
	return &out
}

// collectMethods reads a direction's `oneOf`, where each arm is an envelope whose
// `method` is a single-value enum and whose `params` points at the payload.
func (g *generator) collectMethods(path, direction string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc schema
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	for _, arm := range doc.OneOf {
		methodProp, ok := arm.Properties["method"]
		if !ok || len(methodProp.Enum) != 1 {
			continue
		}
		name, ok := methodProp.Enum[0].(string)
		if !ok {
			continue
		}
		m := method{Name: name, Direction: direction, Doc: arm.Description}
		if params, ok := arm.Properties["params"]; ok {
			m.Params = refName(params.base().Ref)
		}
		// Requests answer with `<Params-stem>Response` when the bundle declares one.
		if strings.HasSuffix(direction, "Request") && m.Params != "" {
			if stem, ok := strings.CutSuffix(m.Params, "Params"); ok {
				if _, exists := g.defs[stem+"Response"]; exists {
					m.Result = stem + "Response"
				}
			}
		}
		g.methods = append(g.methods, m)
	}
	return nil
}

func refName(ref string) string {
	if ref == "" {
		return ""
	}
	return exportName(ref[strings.LastIndex(ref, "/")+1:])
}

/* ---- Go type mapping ---------------------------------------------------- */
