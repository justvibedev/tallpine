package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/service/settingsview"
	"tallpine.eu/tallpine/backend/internal/service/settingswrite"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

// SettingsValues reads effective setting values. settingsview.Service satisfies it.
type SettingsValues interface {
	App(ctx context.Context) ([]settingsview.Value, error)
	Project(ctx context.Context, id domain.ProjectID) ([]settingsview.Value, error)
}

// SettingsWriter applies a settings change. settingswrite.Writer satisfies it.
type SettingsWriter interface {
	Apply(ctx context.Context, ch settingswrite.Change) error
}

// SettingsRegistryController serves the settings registry and effective values (B202).
type SettingsRegistryController struct {
	Values SettingsValues
	Writer SettingsWriter
}

// SettingDefinition is one registered setting.
type SettingDefinition struct {
	Key         string   `json:"key"`
	Scope       string   `json:"scope" enum:"app,project,policy"`
	Kind        string   `json:"kind" enum:"bool,int,enum,string,text,stringList,stringMap,object"`
	Default     any      `json:"default"`
	Enum        []string `json:"enum,omitempty"`
	Min         *int64   `json:"min,omitempty"`
	Max         *int64   `json:"max,omitempty"`
	MaxBytes    int      `json:"maxBytes,omitempty"`
	Description string   `json:"description"`
	Env         []string `json:"env,omitempty" description:"Environment variables that can decide the value; a variable that forces it locks it."`
	MDMLockable bool     `json:"mdmLockable" description:"Managed config may set and lock the value."`
	Nullable    bool     `json:"nullable" description:"null clears the value."`
	Sensitive   bool     `json:"sensitive" description:"Redacted in the settings change log."`
	Writers     []string `json:"writers" description:"Actors that may change the value when nothing locks it."`
}

// SettingsSchemaResponse lists every registered setting in registry order.
type SettingsSchemaResponse struct {
	Settings []SettingDefinition `json:"settings"`
}

// SettingsValuesQuery documents GET /settings/values.
type SettingsValuesQuery struct {
	Project string `query:"project" description:"Project whose settings to read; empty reads app-wide settings."`
}

// Register mounts the registry routes.
func (c *SettingsRegistryController) Register(r chi.Router) {
	r.Get("/settings/schema", c.schema)
	r.Get("/settings/values", c.values)
	r.Patch("/settings/values", c.update)
}

// maxSettingsBody bounds PATCH /settings/values (rules text is the largest value).
const maxSettingsBody = 256 << 10

func (c *SettingsRegistryController) update(w http.ResponseWriter, r *http.Request) {
	if c.Writer == nil || c.Values == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/settings/values")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSettingsBody)
	var req UpdateSettingsValuesRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	ch := settingswrite.Change{Project: domain.ProjectID(req.Project), Set: map[settingsreg.Key]json.RawMessage{}}
	for k, v := range req.Set {
		raw, err := json.Marshal(v)
		if err != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid value for "+k, nil)
			return
		}
		ch.Set[settingsreg.Key(k)] = raw
	}
	for _, k := range req.Unset {
		ch.Unset = append(ch.Unset, settingsreg.Key(k))
	}
	var refused *settingswrite.Error
	if err := c.Writer.Apply(r.Context(), ch); errors.As(err, &refused) {
		status, kind := http.StatusBadRequest, "validation"
		switch refused.Code {
		case settingswrite.CodeLocked:
			status, kind = http.StatusConflict, "conflict"
		case settingswrite.CodeNotPermitted, settingswrite.CodeDesktopOnly:
			status, kind = http.StatusForbidden, "forbidden"
		}
		var details map[string]any
		if len(refused.Keys) > 0 {
			details = map[string]any{"keys": refused.Keys}
		}
		envelope.WriteAPIError(w, r, status, kind, refused.Code, refused.Message, details)
		return
	} else if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	c.writeValues(w, r, req.Project)
}

func (c *SettingsRegistryController) schema(w http.ResponseWriter, _ *http.Request) {
	defs := settingsreg.Default().All()
	out := SettingsSchemaResponse{Settings: make([]SettingDefinition, 0, len(defs))}
	for _, d := range defs {
		out.Settings = append(out.Settings, SettingDefinition{
			Key: string(d.Key), Scope: string(d.Scope), Kind: string(d.Kind), Default: decodeAny(d.Default),
			Enum: d.Enum, Min: d.Min, Max: d.Max, MaxBytes: d.MaxBytes, Description: d.Description, Env: d.Env,
			MDMLockable: d.MDMLockable, Nullable: d.Nullable, Sensitive: d.Sensitive, Writers: actorNames(d.Writers),
		})
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *SettingsRegistryController) values(w http.ResponseWriter, r *http.Request) {
	if c.Values == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/settings/values")
		return
	}
	project := r.URL.Query().Get("project")
	if refuseOtherProject(w, r, project) {
		return
	}
	c.writeValues(w, r, project)
}

// refuseOtherProject answers 403 when an agent asks for another project's
// settings; app-wide reads and its own project pass (B202).
func refuseOtherProject(w http.ResponseWriter, r *http.Request, project string) bool {
	agent, ok := requestscope.AgentFrom(r.Context())
	if !ok || project == "" || agent.MayActOn(domain.ProjectID(project)) {
		return false
	}
	envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", settingswrite.CodeNotPermitted,
		"an agent may read only its own project's settings (project "+string(agent.ProjectID)+")", nil)
	return true
}

// agentRedacted is the value an agent sees for a sensitive setting such as
// project.env.
const agentRedacted = "<redacted>"

// writeValues answers with the effective values of the app or a project.
func (c *SettingsRegistryController) writeValues(w http.ResponseWriter, r *http.Request, project string) {
	var values []settingsview.Value
	var err error
	if project == "" {
		values, err = c.Values.App(r.Context())
	} else {
		values, err = c.Values.Project(r.Context(), domain.ProjectID(project))
	}
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	_, isAgent := requestscope.AgentFrom(r.Context())
	out := SettingsValuesResponse{Project: project, Values: make([]SettingValue, 0, len(values))}
	for _, v := range values {
		value := decodeAny(v.Value)
		if def, ok := settingsreg.Default().Lookup(v.Key); isAgent && ok && def.Sensitive && v.Source != settingsview.SourceDefault {
			value = agentRedacted
		}
		out.Values = append(out.Values, SettingValue{
			Key: string(v.Key), Value: value, Source: v.Source, Locked: v.Locked, LockedBy: v.LockedBy,
			Writable: actorNames(v.Writable),
		})
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func actorNames(actors []settingsreg.Actor) []string {
	out := make([]string, 0, len(actors))
	for _, a := range actors {
		out = append(out, string(a))
	}
	return out
}
