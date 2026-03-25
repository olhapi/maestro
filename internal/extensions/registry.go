package extensions

import (
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
	TimeoutSec         int                    `json:"timeout_sec,omitempty"`
	Allowed            *bool                  `json:"allowed,omitempty"`
	WorkingDir         string                 `json:"working_dir,omitempty"`
	RequireArgs        bool                   `json:"require_args,omitempty"`
	DenyEnvPassthrough bool                   `json:"deny_env_passthrough,omitempty"`
}

type schemaValidator func(interface{}) error

type Registry struct {
	order     []string
	baseDir   string
	tools     map[string]Tool
	validators map[string]schemaValidator
}

func EmptyRegistry() *Registry {
	return &Registry{
		tools:      map[string]Tool{},
		validators: map[string]schemaValidator{},
	}
}

func LoadFile(path string) (*Registry, error) {
	if strings.TrimSpace(path) == "" {
		return EmptyRegistry(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
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
	baseDir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	return newRegistry(defs, baseDir), nil
}

func NewRegistry(defs []Tool) *Registry {
	return newRegistry(defs, "")
}

func newRegistry(defs []Tool, baseDir string) *Registry {
	reg := EmptyRegistry()
	reg.baseDir = baseDir
	for _, def := range defs {
		name := strings.TrimSpace(def.Name)
		if name == "" || strings.TrimSpace(def.Command) == "" {
			continue
		}
		if def.TimeoutSec <= 0 {
			def.TimeoutSec = 15
		}
		def.Name = name
		def.InputSchema = cloneMap(def.InputSchema)
		if validator, err := compileInputSchemaValidator(def.Name, def.InputSchema); err == nil {
			reg.validators[name] = validator
		} else if err != nil {
			reg.validators[name] = func(interface{}) error { return err }
		}
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
	if validator := r.validators[tool.Name]; validator != nil {
		if err := validator(args); err != nil {
			return "", fmt.Errorf("extension tool %s invalid args: %w", name, err)
		}
	}

	argsJSON, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("extension tool %s could not encode args: %w", name, err)
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(tool.TimeoutSec)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "sh", "-c", tool.Command)
	if tool.WorkingDir != "" {
		if wd := r.resolveWorkingDir(tool.WorkingDir); wd != "" {
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

func (r *Registry) resolveWorkingDir(workingDir string) string {
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		return ""
	}
	baseDir := r.baseDir
	if baseDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			baseDir = cwd
		}
	}
	if baseDir != "" && !filepath.IsAbs(workingDir) {
		workingDir = filepath.Join(baseDir, workingDir)
	}
	if abs, err := filepath.Abs(workingDir); err == nil {
		return abs
	}
	return workingDir
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
	_, err := compileInputSchemaValidator(tool.Name, tool.InputSchema)
	return err
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

func cloneMap(src map[string]interface{}) map[string]interface{} {
	if src == nil {
		return nil
	}
	dst := make(map[string]interface{}, len(src))
	for key, value := range src {
		dst[key] = cloneJSONValue(value)
	}
	return dst
}

func cloneJSONValue(value interface{}) interface{} {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		return value
	}
	return out
}

func compileInputSchemaValidator(toolName string, schema map[string]interface{}) (schemaValidator, error) {
	if len(schema) == 0 {
		return nil, nil
	}
	node, err := compileSchemaNode(toolName, schema, "input_schema", true)
	if err != nil {
		return nil, err
	}
	return node.validate, nil
}

type compiledSchema struct {
	toolName               string
	path                   string
	typ                    string
	properties             map[string]*compiledSchema
	required               map[string]struct{}
	additionalAllowed      bool
	additionalSchema       *compiledSchema
	hasAdditionalField     bool
	items                  *compiledSchema
}

func compileSchemaNode(toolName string, schema map[string]interface{}, path string, requireObject bool) (*compiledSchema, error) {
	typ, ok := schema["type"].(string)
	if !ok || strings.TrimSpace(typ) == "" {
		return nil, fmt.Errorf("extension tool %q has invalid %s: type must be set to object", toolName, path)
	}
	if requireObject && typ != "object" {
		return nil, fmt.Errorf("extension tool %q has invalid %s: type must be object", toolName, path)
	}
	if !requireObject && typ != "object" && typ != "array" && typ != "string" && typ != "number" && typ != "integer" && typ != "boolean" {
		return nil, fmt.Errorf("extension tool %q has invalid %s: unsupported type %q", toolName, path, typ)
	}
	node := &compiledSchema{
		toolName: toolName,
		path:     path,
		typ:      typ,
	}
	if rawRequired, ok := schema["required"]; ok && rawRequired != nil {
		required, err := stringSliceValue(rawRequired)
		if err != nil {
			return nil, fmt.Errorf("extension tool %q has invalid %s: required must be an array of strings", toolName, path)
		}
		node.required = make(map[string]struct{}, len(required))
		for _, name := range required {
			node.required[name] = struct{}{}
		}
	}
		if rawDefs, ok := schema["$defs"]; ok && rawDefs != nil {
			if defs, ok := rawDefs.(map[string]interface{}); ok {
				for defName, rawDef := range defs {
					defMap, ok := rawDef.(map[string]interface{})
					if !ok {
						return nil, fmt.Errorf("extension tool %q has invalid %s.$defs.%s: must be an object", toolName, path, defName)
					}
					if _, err := compileSchemaNode(toolName, defMap, path+".$defs."+defName, false); err != nil {
						return nil, err
					}
				}
			} else {
				return nil, fmt.Errorf("extension tool %q has invalid %s: $defs must be an object", toolName, path)
		}
	}
	switch typ {
	case "object":
		if rawProps, ok := schema["properties"]; ok && rawProps != nil {
			properties, ok := rawProps.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("extension tool %q has invalid %s: properties must be an object", toolName, path)
			}
			node.properties = make(map[string]*compiledSchema, len(properties))
			for propName, rawProp := range properties {
				propMap, ok := rawProp.(map[string]interface{})
				if !ok {
					return nil, fmt.Errorf("extension tool %q has invalid %s.properties.%s: must be an object", toolName, path, propName)
				}
				child, err := compileSchemaNode(toolName, propMap, path+".properties."+propName, false)
				if err != nil {
					return nil, err
				}
				node.properties[propName] = child
			}
		}
		if rawAdditional, ok := schema["additionalProperties"]; ok && rawAdditional != nil {
			switch typed := rawAdditional.(type) {
			case bool:
				node.hasAdditionalField = true
				node.additionalAllowed = typed
			case map[string]interface{}:
				child, err := compileSchemaNode(toolName, typed, path+".additionalProperties", false)
				if err != nil {
					return nil, err
				}
				node.hasAdditionalField = true
				node.additionalSchema = child
			default:
				return nil, fmt.Errorf("extension tool %q has invalid %s: additionalProperties must be a boolean or object", toolName, path)
			}
		}
	case "array":
		if rawItems, ok := schema["items"]; ok && rawItems != nil {
			itemMap, ok := rawItems.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("extension tool %q has invalid %s: items must be an object", toolName, path)
			}
			child, err := compileSchemaNode(toolName, itemMap, path+".items", false)
			if err != nil {
				return nil, err
			}
			node.items = child
		}
	}
	return node, nil
}

func (s *compiledSchema) validate(value interface{}) error {
	return s.validateAt(value, s.path)
}

func (s *compiledSchema) validateAt(value interface{}, path string) error {
	if s == nil {
		return nil
	}
	switch s.typ {
	case "object":
		obj, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%s must be an object", path)
		}
		for name := range s.required {
			if _, ok := obj[name]; !ok {
				return fmt.Errorf("%s is missing required property %q", path, name)
			}
		}
		for name, child := range s.properties {
			childValue, ok := obj[name]
			if !ok {
				continue
			}
			if err := child.validateAt(childValue, path+"."+name); err != nil {
				return err
			}
		}
		for name, childValue := range obj {
			if _, ok := s.properties[name]; ok {
				continue
			}
			if s.additionalSchema != nil {
				if err := s.additionalSchema.validateAt(childValue, path+"."+name); err != nil {
					return err
				}
				continue
			}
			if s.hasAdditionalField && s.additionalAllowed {
				continue
			}
			if s.hasAdditionalField && !s.additionalAllowed {
				return fmt.Errorf("%s does not allow additional property %q", path, name)
			}
		}
	case "array":
		items, ok := value.([]interface{})
		if !ok {
			return fmt.Errorf("%s must be an array", path)
		}
		if s.items == nil {
			return nil
		}
		for i, item := range items {
			if err := s.items.validateAt(item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s must be a string", path)
		}
	case "number":
		if !isJSONNumber(value) {
			return fmt.Errorf("%s must be a number", path)
		}
	case "integer":
		if !isJSONInteger(value) {
			return fmt.Errorf("%s must be an integer", path)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be a boolean", path)
		}
	default:
		return fmt.Errorf("%s has unsupported type %q", path, s.typ)
	}
	return nil
}

func stringSliceValue(value interface{}) ([]string, error) {
	switch typed := value.(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if strings.TrimSpace(item) == "" {
				return nil, fmt.Errorf("non-string required field")
			}
			out = append(out, item)
		}
		return out, nil
	case []interface{}:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("non-string required field")
			}
			out = append(out, text)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected array of strings")
	}
}

func isJSONNumber(value interface{}) bool {
	switch typed := value.(type) {
	case int, int8, int16, int32, int64:
		return true
	case uint, uint8, uint16, uint32, uint64:
		return true
	case float32, float64:
		return true
	case json.Number:
		return true
	default:
		_ = typed
		return false
	}
}

func isJSONInteger(value interface{}) bool {
	switch typed := value.(type) {
	case int, int8, int16, int32, int64:
		return true
	case uint, uint8, uint16, uint32, uint64:
		return true
	case float32:
		return float32(int64(typed)) == typed
	case float64:
		return float64(int64(typed)) == typed
	case json.Number:
		if strings.ContainsRune(typed.String(), '.') {
			return false
		}
		return true
	default:
		return false
	}
}
