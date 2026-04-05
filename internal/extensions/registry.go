package extensions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Tool struct {
	Name               string                 `json:"name"`
	Description        string                 `json:"description"`
	Command            string                 `json:"command"`
	InputSchema        map[string]interface{} `json:"input_schema,omitempty"`
	Annotations        ToolAnnotations        `json:"annotations,omitempty"`
	TimeoutSec         int                    `json:"timeout_sec,omitempty"`
	Allowed            *bool                  `json:"allowed,omitempty"`
	WorkingDir         string                 `json:"working_dir,omitempty"`
	RequireArgs        bool                   `json:"require_args,omitempty"`
	DenyEnvPassthrough bool                   `json:"deny_env_passthrough,omitempty"`
}

type ToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"read_only_hint,omitempty"`
	DestructiveHint *bool  `json:"destructive_hint,omitempty"`
	IdempotentHint  *bool  `json:"idempotent_hint,omitempty"`
	OpenWorldHint   *bool  `json:"open_world_hint,omitempty"`
}

type Registry struct {
	order []string
	tools map[string]Tool
}

func EmptyRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

func LoadFile(path string) (*Registry, error) {
	if strings.TrimSpace(path) == "" {
		return EmptyRegistry(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := validateAnnotationsPayload(data); err != nil {
		return nil, err
	}
	var defs []Tool
	if err := json.Unmarshal(data, &defs); err != nil {
		return nil, err
	}
	for _, def := range defs {
		if err := validateInputSchema(def); err != nil {
			return nil, err
		}
	}
	return NewRegistry(defs), nil
}

func NewRegistry(defs []Tool) *Registry {
	reg := EmptyRegistry()
	for _, def := range defs {
		name := strings.TrimSpace(def.Name)
		if name == "" || strings.TrimSpace(def.Command) == "" {
			continue
		}
		if def.TimeoutSec <= 0 {
			def.TimeoutSec = 15
		}
		def.Name = name
		reg.order = append(reg.order, name)
		reg.tools[name] = def
	}
	return reg
}

func (r *Registry) Names() []string {
	if r == nil || len(r.order) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, name)
	}
	return out
}

func (r *Registry) Specs() []map[string]interface{} {
	if r == nil || len(r.order) == 0 {
		return nil
	}
	specs := make([]map[string]interface{}, 0, len(r.order))
	for _, name := range r.order {
		tool := r.tools[name]
		specs = append(specs, map[string]interface{}{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": inputSchemaForTool(tool),
			"annotations": annotationSpecForTool(tool),
		})
	}
	return specs
}

func (r *Registry) HasTools() bool {
	return r != nil && len(r.order) > 0
}

func (r *Registry) Execute(ctx context.Context, name string, args interface{}) (string, error) {
	if r == nil {
		return "", fmt.Errorf("unsupported dynamic tool: %q", name)
	}
	tool, ok := r.tools[strings.TrimSpace(name)]
	if !ok {
		return "", fmt.Errorf("unsupported dynamic tool: %q", name)
	}
	if tool.Allowed != nil && !*tool.Allowed {
		return "", fmt.Errorf("extension tool %s is disabled by policy", name)
	}
	if tool.RequireArgs && isEmptyArgs(args) {
		return "", fmt.Errorf("extension tool %s requires args object", name)
	}

	argsJSON, _ := json.Marshal(args)
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(tool.TimeoutSec)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "sh", "-c", tool.Command)
	if tool.WorkingDir != "" {
		if wd, err := filepath.Abs(tool.WorkingDir); err == nil {
			cmd.Dir = wd
		}
	}
	if tool.DenyEnvPassthrough {
		cmd.Env = []string{"MAESTRO_ARGS_JSON=" + string(argsJSON), "MAESTRO_TOOL_NAME=" + name}
	} else {
		cmd.Env = append(os.Environ(), "MAESTRO_ARGS_JSON="+string(argsJSON), "MAESTRO_TOOL_NAME="+name)
	}

	out, err := cmd.CombinedOutput()
	if runCtx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("extension tool %s timed out after %ds", name, tool.TimeoutSec)
	}
	if err != nil {
		return "", fmt.Errorf("extension tool %s failed: %v\n%s", name, err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

func isEmptyArgs(args interface{}) bool {
	if args == nil {
		return true
	}
	if m, ok := args.(map[string]interface{}); ok {
		if raw, has := m["args"]; has {
			if nested, ok := raw.(map[string]interface{}); ok {
				return len(nested) == 0
			}
			return raw == nil
		}
		return len(m) == 0
	}
	return false
}

func validateInputSchema(tool Tool) error {
	if len(tool.InputSchema) == 0 {
		return nil
	}
	typ, ok := tool.InputSchema["type"].(string)
	if !ok || strings.TrimSpace(typ) == "" {
		return fmt.Errorf("extension tool %q has invalid input_schema: type must be set to object", tool.Name)
	}
	if typ != "object" {
		return fmt.Errorf("extension tool %q has invalid input_schema: type must be object", tool.Name)
	}
	if raw, ok := tool.InputSchema["properties"]; ok && raw != nil {
		if _, ok := raw.(map[string]interface{}); !ok {
			return fmt.Errorf("extension tool %q has invalid input_schema: properties must be an object", tool.Name)
		}
	}
	return nil
}

func validateAnnotationsPayload(data []byte) error {
	var defs []map[string]json.RawMessage
	if err := json.Unmarshal(data, &defs); err != nil {
		return nil
	}
	for _, raw := range defs {
		annotationBody, ok := raw["annotations"]
		if !ok || len(bytes.TrimSpace(annotationBody)) == 0 || bytes.Equal(bytes.TrimSpace(annotationBody), []byte("null")) {
			continue
		}
		name := annotationToolName(raw["name"])
		decoder := json.NewDecoder(bytes.NewReader(annotationBody))
		decoder.DisallowUnknownFields()
		var annotations ToolAnnotations
		if err := decoder.Decode(&annotations); err != nil {
			return fmt.Errorf("extension tool %q has invalid annotations: %w", name, err)
		}
	}
	return nil
}

func annotationToolName(raw json.RawMessage) string {
	var name string
	if err := json.Unmarshal(raw, &name); err != nil {
		return ""
	}
	return strings.TrimSpace(name)
}

func inputSchemaForTool(tool Tool) map[string]interface{} {
	if len(tool.InputSchema) != 0 {
		return cloneMap(tool.InputSchema)
	}
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"args": map[string]interface{}{
				"type":        "object",
				"description": "Extension arguments object; passed to the tool command via MAESTRO_ARGS_JSON.",
			},
		},
	}
}

func annotationSpecForTool(tool Tool) map[string]interface{} {
	return map[string]interface{}{
		"title":           strings.TrimSpace(tool.Annotations.Title),
		"readOnlyHint":    boolValueOrDefault(tool.Annotations.ReadOnlyHint, false),
		"destructiveHint": boolValueOrDefault(tool.Annotations.DestructiveHint, true),
		"idempotentHint":  boolValueOrDefault(tool.Annotations.IdempotentHint, false),
		"openWorldHint":   boolValueOrDefault(tool.Annotations.OpenWorldHint, true),
	}
}

func boolValueOrDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func cloneMap(src map[string]interface{}) map[string]interface{} {
	if src == nil {
		return nil
	}
	dst := make(map[string]interface{}, len(src))
	for key, value := range src {
		if nested, ok := value.(map[string]interface{}); ok {
			dst[key] = cloneMap(nested)
			continue
		}
		dst[key] = value
	}
	return dst
}
